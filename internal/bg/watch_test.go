package bg

import (
	"bytes"
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/pprof"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
)

// watchRepo creates a repo with a tracked file in sub/ and an ignored
// build/ directory.
func watchRepo(t *testing.T) string {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	sh(t, dir, "init", "-q", "-b", "main")
	os.MkdirAll(filepath.Join(dir, "sub"), 0o755)
	os.MkdirAll(filepath.Join(dir, "build", "deep"), 0o755)
	os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("1\n"), 0o644)
	os.WriteFile(filepath.Join(dir, ".gitignore"), []byte("build/\n"), 0o644)
	sh(t, dir, "add", ".")
	sh(t, dir, "commit", "-q", "-m", "init")
	return dir
}

type harness struct {
	t    *testing.T
	msgs chan any
}

// startWatching runs a supervisor with polling and fetching off, so every
// StateMsg after startup comes from the watcher.
func startWatching(t *testing.T, dir string, maxWatches int) *harness {
	t.Helper()
	cfg := config.Default()
	cfg.Fetch.Enabled = false
	cfg.Status.PollInterval = 0
	cfg.Status.WatchPollInterval = 0
	cfg.Status.MaxWatches = maxWatches
	h := &harness{t: t, msgs: make(chan any, 1000)}
	s := &Supervisor{Git: git.Runner{Dir: dir, Background: true}, Cfg: cfg, Send: func(m any) { h.msgs <- m }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { s.Run(ctx); close(done) }()
	// Stop watching before t.TempDir is removed: Windows can't delete
	// directories that are still being watched.
	t.Cleanup(func() { cancel(); waitStopped(t, done) })
	return h
}

func (h *harness) waitWatch() WatchMsg {
	h.t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case m := <-h.msgs:
			if w, ok := m.(WatchMsg); ok {
				return w
			}
		case <-timeout:
			h.t.Fatal("no WatchMsg")
		}
	}
}

// settle drains messages until things go quiet.
func (h *harness) settle() {
	for {
		select {
		case <-h.msgs:
		case <-time.After(debounce * 3):
			return
		}
	}
}

// nextState waits for a StateMsg matching ok.
func (h *harness) nextState(within time.Duration, ok func(git.State) bool) bool {
	timeout := time.After(within)
	for {
		select {
		case m := <-h.msgs:
			if s, isState := m.(StateMsg); isState && (ok == nil || ok(s.State)) {
				return true
			}
		case <-timeout:
			return false
		}
	}
}

func TestWatcher(t *testing.T) {
	dir := watchRepo(t)
	h := startWatching(t, dir, 8000)
	if w := h.waitWatch(); w.Mode != WatchFull {
		t.Fatalf("mode = %s (%s)", w.Mode, w.Reason)
	}
	h.settle()

	t.Run("edit in subdirectory", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "sub", "a.txt"), []byte("2\n"), 0o644)
		if !h.nextState(5*time.Second, func(s git.State) bool { return slices.Contains(s.Unstaged, "sub/a.txt") }) {
			t.Fatal("edit not noticed")
		}
		h.settle()
	})

	t.Run("git operation", func(t *testing.T) {
		sh(t, dir, "add", "sub/a.txt")
		if !h.nextState(5*time.Second, func(s git.State) bool { return slices.Contains(s.Staged, "sub/a.txt") }) {
			t.Fatal("index change not noticed")
		}
		h.settle()
	})

	t.Run("directory created later", func(t *testing.T) {
		os.MkdirAll(filepath.Join(dir, "late", "nested"), 0o755)
		h.settle() // the mkdir itself causes a refresh; wait for it to pass
		os.WriteFile(filepath.Join(dir, "late", "nested", "x.txt"), []byte("x"), 0o644)
		if !h.nextState(5*time.Second, nil) {
			t.Fatal("file in new nested directory not noticed")
		}
		h.settle()
	})

	t.Run("ignored directory is not watched", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "build", "deep", "out.bin"), []byte("x"), 0o644)
		if h.nextState(debounce*4, nil) {
			t.Fatal("refreshed for a change in an ignored directory")
		}
	})

	t.Run("editor temp files are ignored", func(t *testing.T) {
		os.WriteFile(filepath.Join(dir, "sub", ".a.txt.swp"), []byte("x"), 0o644)
		if h.nextState(debounce*4, nil) {
			t.Fatal("refreshed for an editor swap file")
		}
	})
}

func TestWatcherFallsBackWhenTooLarge(t *testing.T) {
	dir := watchRepo(t)
	h := startWatching(t, dir, 1)
	w := h.waitWatch()
	if w.Mode != WatchGitOnly || w.Reason == "" {
		t.Fatalf("mode = %s reason = %q", w.Mode, w.Reason)
	}
	h.settle()
	// .git is still watched in git-only mode
	sh(t, dir, "commit", "-q", "--allow-empty", "-m", "two")
	if !h.nextState(5*time.Second, nil) {
		t.Fatal("commit not noticed in git-only mode")
	}
}

// waitStopped fails (instead of hanging the test binary) if the
// supervisor doesn't stop, and shows where its goroutines are stuck.
func waitStopped(t *testing.T, done <-chan struct{}) {
	t.Helper()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		var buf bytes.Buffer
		pprof.Lookup("goroutine").WriteTo(&buf, 1)
		var stuck []string
		for _, l := range strings.Split(buf.String(), "\n") {
			if strings.Contains(l, "gituy/internal") || strings.Contains(l, "fsnotify") {
				stuck = append(stuck, strings.TrimSpace(l))
			}
		}
		t.Fatalf("supervisor did not stop; stuck in:\n%s", strings.Join(stuck, "\n"))
	}
}

func TestRelevantName(t *testing.T) {
	for name, want := range map[string]bool{
		"main.go": true, "index": true, "HEAD": true, "MERGE_HEAD": true,
		"index.lock": false, "main.go~": false, ".main.go.swp": false, "4913": false, ".#main.go": false,
	} {
		if got := relevantName(name); got != want {
			t.Errorf("relevantName(%q) = %v, want %v", name, got, want)
		}
	}
}
