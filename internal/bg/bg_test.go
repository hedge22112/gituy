package bg

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
)

func sh(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1",
		"GIT_AUTHOR_NAME=t", "GIT_AUTHOR_EMAIL=t@t", "GIT_COMMITTER_NAME=t", "GIT_COMMITTER_EMAIL=t@t")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestSupervisorFetchesOnStart sets up a clone whose remote has a new
// commit, and checks the supervisor fetches it and reports the change.
func TestSupervisorFetchesOnStart(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	root := t.TempDir()
	remote, a, b := filepath.Join(root, "r.git"), filepath.Join(root, "a"), filepath.Join(root, "b")
	sh(t, root, "init", "-q", "--bare", "-b", "main", remote)
	sh(t, root, "clone", "-q", remote, a)
	sh(t, a, "commit", "-q", "--allow-empty", "-m", "one")
	sh(t, a, "push", "-q", "origin", "main")
	sh(t, root, "clone", "-q", remote, b)
	sh(t, a, "commit", "-q", "--allow-empty", "-m", "two")
	sh(t, a, "push", "-q", "origin", "main")

	var mu sync.Mutex
	var msgs []any
	got := make(chan FetchMsg, 1)
	cfg := config.Default()
	cfg.Status.PollInterval = 0
	s := &Supervisor{
		Git: git.Runner{Dir: b, Background: true},
		Cfg: cfg,
		Send: func(m any) {
			mu.Lock()
			msgs = append(msgs, m)
			mu.Unlock()
			if f, ok := m.(FetchMsg); ok {
				got <- f
			}
		},
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()

	select {
	case f := <-got:
		if f.Err != nil {
			t.Fatal(f.Err)
		}
		if len(f.Changes) != 1 || f.Changes[0].Ref != "origin/main" {
			t.Fatalf("changes = %+v", f.Changes)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("no fetch reported")
	}
	if s.LastFetched().IsZero() {
		t.Error("LastFetched not set")
	}

	// the refresh after the fetch should show the branch is behind
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		mu.Lock()
		for _, m := range msgs {
			if st, ok := m.(StateMsg); ok && st.State.Behind == 1 {
				mu.Unlock()
				return
			}
		}
		mu.Unlock()
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("no state refresh showing behind=1")
}

func TestBackoff(t *testing.T) {
	cfg := config.Default()
	cfg.Fetch.Interval = time.Minute
	s := &Supervisor{Cfg: cfg}
	s.failures = 3
	if d := s.nextInterval(); d != 8*time.Minute {
		t.Fatalf("backoff = %v, want 8m", d)
	}
	s.failures = 50
	if d := s.nextInterval(); d > 4*time.Hour {
		t.Fatalf("backoff unbounded: %v", d)
	}
}
