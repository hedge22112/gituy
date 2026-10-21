// Package bg runs gituy's background tasks: refreshing repository state and
// fetching remotes. Tasks report through a send function (the TUI's
// Program.Send) and never touch the working tree.
package bg

import (
	"context"
	"sync"
	"time"

	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
)

// StateMsg carries a fresh repository snapshot.
type StateMsg struct {
	State git.State
	Err   error
}

// FetchStartMsg is sent when a background fetch begins.
type FetchStartMsg struct{}

// FetchMsg reports a finished background fetch.
type FetchMsg struct {
	Args    []string
	Changes []git.RefChange
	At      time.Time
	Dur     time.Duration
	Err     error
}

// Supervisor owns the background goroutines.
type Supervisor struct {
	Git  git.Runner // should have Background set
	Cfg  config.Config
	Send func(any)

	// LastFetch lets a new session skip the on-start fetch if one ran
	// recently (gituy restarts its UI after running a command).
	LastFetch time.Time

	refresh  chan struct{}
	fetchNow chan struct{}
	mu       sync.Mutex
	failures int
}

// Run starts the background tasks and blocks until ctx is cancelled.
func (s *Supervisor) Run(ctx context.Context) {
	s.refresh = make(chan struct{}, 1)
	s.fetchNow = make(chan struct{}, 1)

	go s.pollLoop(ctx)
	if s.Cfg.Fetch.Enabled {
		go s.fetchLoop(ctx)
	}
	s.Refresh()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.refresh:
			st, err := s.Git.ReadState(ctx)
			if ctx.Err() != nil {
				return
			}
			s.Send(StateMsg{State: st, Err: err})
		}
	}
}

// Refresh requests a state refresh. Bursts collapse into one.
func (s *Supervisor) Refresh() {
	select {
	case s.refresh <- struct{}{}:
	default:
	}
}

// FetchNow requests an immediate fetch, resetting any failure backoff.
func (s *Supervisor) FetchNow() {
	s.mu.Lock()
	s.failures = 0
	s.mu.Unlock()
	select {
	case s.fetchNow <- struct{}{}:
	default:
	}
}

func (s *Supervisor) pollLoop(ctx context.Context) {
	if s.Cfg.Status.PollInterval <= 0 {
		return
	}
	t := time.NewTicker(s.Cfg.Status.PollInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.Refresh()
		}
	}
}

// never is a wait that doesn't end.
const never = time.Duration(1<<63 - 1)

func (s *Supervisor) fetchLoop(ctx context.Context) {
	f := s.Cfg.Fetch
	var next time.Duration
	switch {
	case !s.LastFetch.IsZero(): // UI restarted: continue the schedule
		next = never
		if f.Interval > 0 {
			next = time.Until(s.LastFetch.Add(f.Interval))
		}
	case f.OnStart:
		next = 0
	case f.Interval > 0:
		next = f.Interval
	default:
		next = never
	}
	for {
		timer := time.NewTimer(max(next, 0))
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.fetchNow:
			timer.Stop()
		}
		s.fetch(ctx)
		next = s.nextInterval()
	}
}

// LastFetched returns when the last background fetch finished.
func (s *Supervisor) LastFetched() time.Time {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.LastFetch
}

// nextInterval backs off exponentially after failures (e.g. auth errors)
// so a broken remote isn't retried every few minutes.
func (s *Supervisor) nextInterval() time.Duration {
	base := s.Cfg.Fetch.Interval
	if base <= 0 {
		return never
	}
	s.mu.Lock()
	n := s.failures
	s.mu.Unlock()
	d := base
	for i := 0; i < n && d < 2*time.Hour; i++ {
		d *= 2
	}
	return d
}

func (s *Supervisor) fetch(ctx context.Context) {
	st, err := s.Git.ReadState(ctx)
	if err != nil || len(st.Remotes) == 0 {
		return
	}
	// Don't move remote refs underneath an in-progress operation.
	if st.Op != git.OpNone {
		return
	}
	remotes := s.Cfg.Fetch.FetchRemotes(st.Remotes)
	if remotes != nil && len(remotes) == 0 {
		return
	}
	args := git.FetchArgs(git.FetchOptions{Remotes: remotes, Prune: s.Cfg.Fetch.Prune, Tags: s.Cfg.Fetch.Tags})

	s.Send(FetchStartMsg{})
	before, _ := s.Git.RemoteRefs(ctx)
	fctx, cancel := context.WithTimeout(ctx, s.Cfg.Fetch.Timeout)
	start := time.Now()
	_, err = s.Git.Run(fctx, args...)
	cancel()
	if ctx.Err() != nil {
		return
	}
	after, _ := s.Git.RemoteRefs(ctx)

	s.mu.Lock()
	if err != nil {
		s.failures++
	} else {
		s.failures = 0
	}
	s.LastFetch = time.Now()
	at := s.LastFetch
	s.mu.Unlock()

	s.Send(FetchMsg{
		Args: append([]string{"git"}, args...), Changes: git.DiffRefs(before, after),
		At: at, Dur: time.Since(start), Err: err,
	})
	s.Refresh()
}
