package bg

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watch modes.
const (
	WatchFull    = "full"     // working tree and git directory
	WatchGitOnly = "git-only" // git directory only; working tree is polled
	WatchOff     = "off"      // polling only
)

// WatchMsg reports what the file watcher is covering.
type WatchMsg struct {
	Mode   string
	Dirs   int    // directories watched
	Reason string // why the mode isn't full
}

// debounce is how long to wait after the first event of a burst before
// refreshing. An editor save or a checkout fires many events at once.
const debounce = 250 * time.Millisecond

// watcher watches the working tree and git directory and asks the
// supervisor to refresh state when something changes.
type watcher struct {
	s         *Supervisor
	w         *fsnotify.Watcher
	top       string // working tree root
	gitDir    string
	commonDir string // shared git dir (differs from gitDir in linked worktrees)
	max       int    // watch budget, see cost
	used      int
	watched   map[string]bool
	worktree  bool // working tree is being watched
}

// kqueue (macOS, BSD) needs a file descriptor for every file in a watched
// directory, so files count against the budget there, not just directories.
var kqueue = map[string]bool{"darwin": true, "freebsd": true, "openbsd": true, "netbsd": true, "dragonfly": true}[runtime.GOOS]

func (s *Supervisor) watchLoop(ctx context.Context) {
	wt, mode, reason := s.startWatcher(ctx)
	if wt == nil {
		s.Send(WatchMsg{Mode: WatchOff, Reason: reason})
		return
	}
	defer wt.w.Close()
	s.setWatching(mode == WatchFull)
	defer s.setWatching(false)
	s.Send(WatchMsg{Mode: mode, Dirs: len(wt.watched), Reason: reason})

	var pending <-chan time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-wt.w.Events:
			if !ok {
				return
			}
			if !wt.relevant(ev) {
				continue
			}
			if ev.Has(fsnotify.Create) && wt.worktree {
				if err := wt.addNewDir(ctx, ev.Name); err != nil {
					wt.worktree = false
					s.setWatching(false)
					s.Send(WatchMsg{Mode: WatchGitOnly, Dirs: len(wt.watched), Reason: err.Error()})
				}
			}
			if pending == nil {
				pending = time.After(debounce)
			}
		case err, ok := <-wt.w.Errors:
			if !ok {
				return
			}
			// Overflow means events were dropped: refresh to be safe.
			if errors.Is(err, fsnotify.ErrEventOverflow) {
				s.Refresh()
			}
		case <-pending:
			pending = nil
			s.Refresh()
		}
	}
}

func (s *Supervisor) startWatcher(ctx context.Context) (*watcher, string, string) {
	top, err1 := s.Git.Run(ctx, "rev-parse", "--show-toplevel")
	gitDir, err2 := s.Git.GitDir(ctx)
	common, err3 := s.Git.Run(ctx, "rev-parse", "--git-common-dir")
	if err := errors.Join(err1, err2, err3); err != nil {
		return nil, "", err.Error()
	}
	common = strings.TrimSpace(common)
	if !filepath.IsAbs(common) {
		base, _ := filepath.Abs(s.Git.Dir)
		common = filepath.Join(base, common)
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, "", "can't watch files: " + err.Error()
	}
	wt := &watcher{
		s: s, w: w, top: filepath.Clean(strings.TrimSpace(top)),
		gitDir: filepath.Clean(gitDir), commonDir: filepath.Clean(common),
		max: s.Cfg.Status.MaxWatches, watched: map[string]bool{},
	}

	// The git directory comes first: HEAD, index, MERGE_HEAD, rebase
	// state and refs are what most suggestions depend on.
	if err := wt.addGitDirs(); err != nil {
		w.Close()
		return nil, "", err.Error()
	}
	if err := wt.addWorktree(ctx); err != nil {
		return wt, WatchGitOnly, err.Error()
	}
	wt.worktree = true
	return wt, WatchFull, ""
}

func (wt *watcher) add(dir string) error {
	if wt.watched[dir] {
		return nil
	}
	if err := wt.w.Add(dir); err != nil {
		if errors.Is(err, syscall.ENOSPC) {
			return errors.New("inotify watch limit reached (raise fs.inotify.max_user_watches)")
		}
		if errors.Is(err, syscall.EMFILE) {
			return errors.New("too many open files to watch this repository")
		}
		return err
	}
	wt.watched[dir] = true
	return nil
}

func (wt *watcher) addGitDirs() error {
	dirs := []string{wt.gitDir}
	if wt.commonDir != wt.gitDir {
		dirs = append(dirs, wt.commonDir)
	}
	// refs/ and its subdirectories (heads, remotes/<name>, tags, ...)
	refs := filepath.Join(wt.commonDir, "refs")
	filepath.WalkDir(refs, func(p string, d fs.DirEntry, err error) error {
		if err == nil && d.IsDir() {
			dirs = append(dirs, p)
		}
		return nil
	})
	for _, d := range dirs {
		if err := wt.add(d); err != nil {
			return err
		}
	}
	return nil
}

// addWorktree watches every directory that holds a tracked or
// non-ignored untracked file, so ignored trees like node_modules are
// never watched.
func (wt *watcher) addWorktree(ctx context.Context) error {
	out, err := wt.s.Git.Run(ctx, "ls-files", "-z", "--cached", "--others", "--exclude-standard")
	if err != nil {
		return err
	}
	dirs := map[string]bool{wt.top: true}
	files := 0
	for _, f := range strings.Split(out, "\x00") {
		if f == "" {
			continue
		}
		files++
		for d := filepath.Dir(filepath.FromSlash(f)); d != "." && !dirs[filepath.Join(wt.top, d)]; d = filepath.Dir(d) {
			dirs[filepath.Join(wt.top, d)] = true
		}
	}
	cost := len(dirs)
	if kqueue {
		cost += files
	}
	if wt.max > 0 && wt.used+cost > wt.max {
		return fmt.Errorf("repository too large to watch (%d > max_watches %d)", cost, wt.max)
	}
	wt.used += cost
	for d := range dirs {
		if err := wt.add(d); err != nil {
			return err
		}
	}
	return nil
}

// addNewDir starts watching a directory created after startup (and any
// subdirectories already in it), unless git ignores it.
func (wt *watcher) addNewDir(ctx context.Context, path string) error {
	info, err := os.Lstat(path)
	if err != nil || !info.IsDir() || wt.inGitDir(path) {
		return nil
	}
	if wt.ignored(ctx, path) {
		return nil
	}
	if wt.max > 0 && wt.used >= wt.max {
		return fmt.Errorf("repository too large to watch (max_watches %d)", wt.max)
	}
	wt.used++
	if err := wt.add(path); err != nil {
		return err
	}
	entries, _ := os.ReadDir(path)
	for _, e := range entries {
		if e.IsDir() && e.Name() != ".git" {
			if err := wt.addNewDir(ctx, filepath.Join(path, e.Name())); err != nil {
				return err
			}
		}
	}
	return nil
}

func (wt *watcher) ignored(ctx context.Context, path string) bool {
	rel, err := filepath.Rel(wt.top, path)
	if err != nil {
		return true
	}
	// exit status 0 means ignored
	_, err = wt.s.Git.Run(ctx, "check-ignore", "-q", "--", filepath.ToSlash(rel)+"/")
	return err == nil
}

func (wt *watcher) inGitDir(path string) bool {
	return within(path, wt.gitDir) || within(path, wt.commonDir)
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, dir+string(filepath.Separator))
}

// relevant filters out events that can't change what gituy shows.
func (wt *watcher) relevant(ev fsnotify.Event) bool {
	if ev.Op == fsnotify.Chmod {
		return false // permission/metadata noise from indexers and antivirus
	}
	return relevantName(filepath.Base(ev.Name))
}

func relevantName(base string) bool {
	switch {
	case strings.HasSuffix(base, ".lock"): // git's lock files; the rename that follows matters
		return false
	case strings.HasSuffix(base, "~"),
		strings.HasSuffix(base, ".swp"), strings.HasSuffix(base, ".swx"), strings.HasSuffix(base, ".swpx"),
		strings.HasPrefix(base, ".#"), base == "4913": // editor temp files
		return false
	}
	return true
}

func (s *Supervisor) setWatching(on bool) { s.watching.Store(on) }

// pollInterval is the status poll interval: slow when the watcher covers
// the working tree (polling is only a safety net), normal otherwise.
func (s *Supervisor) pollInterval() time.Duration {
	if s.watching.Load() {
		return s.Cfg.Status.WatchPollInterval
	}
	return s.Cfg.Status.PollInterval
}
