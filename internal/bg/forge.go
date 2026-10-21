package bg

import (
	"context"
	"strings"
	"time"

	"github.com/hedge22112/gituy/internal/forge"
)

// ForgeMsg carries the GitHub/GitLab status for the current branch.
// Client is set when a forge was detected, for pickers that list PRs,
// issues and CI runs.
type ForgeMsg struct {
	Status forge.Status
	Client *forge.Client
	At     time.Time
}

// ForgeStatuser is what the forge loop needs from a forge client.
type ForgeStatuser interface {
	Status(ctx context.Context, withQueue bool) forge.Status
}

// queueEvery limits how often the review queue (a search across the
// repository) is refreshed; the branch's own PR is checked every interval.
const queueEvery = 5 * time.Minute

// SetFocused tells the supervisor whether the terminal has focus. Forge
// checks pause while it doesn't, and catch up as soon as it does.
func (s *Supervisor) SetFocused(focused bool) {
	s.unfocused.Store(!focused)
	if focused && s.forgeStale.Swap(false) {
		s.ForgeNow()
	}
}

// ForgeNow requests an immediate forge check.
func (s *Supervisor) ForgeNow() {
	select {
	case s.forgeNow <- struct{}{}:
	default:
	}
}

func (s *Supervisor) detectForge(ctx context.Context) (ForgeStatuser, *forge.Client) {
	if s.NewForge != nil {
		return s.NewForge(ctx)
	}
	out, err := s.Git.Run(ctx, "remote")
	if err != nil {
		return nil, nil
	}
	remote := ""
	for _, r := range strings.Fields(out) {
		if r == "origin" || remote == "" {
			remote = r
		}
	}
	c := forge.Detect(ctx, s.Git, remote, s.Cfg.Forge.Hosts)
	if c == nil {
		return nil, nil
	}
	return c, c
}

func (s *Supervisor) forgeLoop(ctx context.Context) {
	client, concrete := s.detectForge(ctx)
	if client == nil {
		return // no GitHub/GitLab remote: nothing to show
	}
	interval := s.Cfg.Forge.Interval
	if interval <= 0 {
		interval = time.Minute
	}
	var lastQueue time.Time
	queue, failures := 0, 0
	var next time.Duration
	for {
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		case <-s.forgeNow:
			timer.Stop()
		}
		next = interval
		if s.unfocused.Load() {
			s.forgeStale.Store(true)
			continue
		}

		withQueue := s.Cfg.Forge.ReviewQueue && time.Since(lastQueue) >= queueEvery
		st := client.Status(ctx, withQueue)
		if ctx.Err() != nil {
			return
		}
		if withQueue && st.Err == nil && st.Ready() {
			lastQueue, queue = time.Now(), st.ReviewQueue
		} else {
			st.ReviewQueue = queue
		}
		if st.Err != nil {
			failures++
			for i := 0; i < failures && next < 30*time.Minute; i++ {
				next *= 2
			}
		} else {
			failures = 0
		}
		s.Send(ForgeMsg{Status: st, Client: concrete, At: time.Now()})
	}
}
