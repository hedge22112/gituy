package git

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Op is an operation git is in the middle of.
type Op string

const (
	OpNone       Op = ""
	OpMerge      Op = "merge"
	OpRebase     Op = "rebase"
	OpCherryPick Op = "cherry-pick"
	OpRevert     Op = "revert"
)

// State is a snapshot of everything gituy uses to make suggestions.
type State struct {
	Branch     string // empty when detached
	Detached   bool
	HasCommits bool
	Upstream   string
	Ahead      int
	Behind     int
	Staged     []string
	Unstaged   []string
	Untracked  []string
	Conflicted []string
	Op         Op
	StashCount int
	HeadPushed bool
	Remotes    []string
	Branches   []string
	RemoteRefs []string

	NotRepo        bool     // gituy was started outside a repository
	UpstreamGone   bool     // the upstream branch was deleted on the remote
	LastAction     string   // newest HEAD reflog subject, e.g. "rebase (finish): ..."
	DefaultBranch  string   // e.g. "main", from origin/HEAD or local main/master
	MergedBranches []string // local branches merged into HEAD (only on the default branch)
	GoneBranches   []string // other local branches whose upstream was deleted
}

// Clean reports whether there is nothing to commit.
func (s State) Clean() bool {
	return len(s.Staged)+len(s.Unstaged)+len(s.Untracked)+len(s.Conflicted) == 0
}

// ReadState collects a full State snapshot.
func (r Runner) ReadState(ctx context.Context) (State, error) {
	var st State
	out, err := r.Run(ctx, "--no-optional-locks", "status", "--porcelain=v2", "--branch", "-z")
	if err != nil {
		return st, err
	}
	parseStatus(out, &st)

	if gitDir, err := r.GitDir(ctx); err == nil {
		st.Op = detectOp(gitDir)
	}
	if out, err := r.Run(ctx, "stash", "list", "--format=%gd"); err == nil {
		st.StashCount = len(lines(out))
	}
	if out, err := r.Run(ctx, "remote"); err == nil {
		st.Remotes = lines(out)
	}
	if out, err := r.Run(ctx, "for-each-ref", "--format=%(refname:short)%09%(upstream:track)", "refs/heads"); err == nil {
		for _, l := range lines(out) {
			name, track, _ := strings.Cut(l, "\t")
			st.Branches = append(st.Branches, name)
			if track == "[gone]" && name != st.Branch {
				st.GoneBranches = append(st.GoneBranches, name)
			}
		}
	}
	if out, err := r.Run(ctx, "for-each-ref", "--format=%(refname:short)", "refs/remotes"); err == nil {
		for _, ref := range lines(out) {
			// skip symbolic "origin" (origin/HEAD) entries
			if strings.Contains(ref, "/") {
				st.RemoteRefs = append(st.RemoteRefs, ref)
			}
		}
	}
	if st.HasCommits {
		if out, err := r.Run(ctx, "branch", "-r", "--contains", "HEAD"); err == nil {
			st.HeadPushed = strings.TrimSpace(out) != ""
		}
		if out, err := r.Run(ctx, "reflog", "-1", "--format=%gs"); err == nil {
			st.LastAction = strings.TrimSpace(out)
		}
	}
	st.DefaultBranch = r.defaultBranch(ctx, st)
	if st.Branch != "" && st.Branch == st.DefaultBranch {
		if out, err := r.Run(ctx, "branch", "--merged", "HEAD", "--format=%(refname:short)"); err == nil {
			for _, b := range lines(out) {
				if b != st.Branch {
					st.MergedBranches = append(st.MergedBranches, b)
				}
			}
		}
	}
	return st, nil
}

// defaultBranch is the remote's default branch (origin/HEAD), falling
// back to a local main or master.
func (r Runner) defaultBranch(ctx context.Context, st State) string {
	remote := ""
	for _, rm := range st.Remotes {
		if rm == "origin" || remote == "" {
			remote = rm
		}
	}
	if remote != "" {
		out, err := r.Run(ctx, "symbolic-ref", "--quiet", "--short", "refs/remotes/"+remote+"/HEAD")
		if err == nil {
			if _, name, ok := strings.Cut(strings.TrimSpace(out), "/"); ok {
				return name
			}
		}
	}
	for _, b := range []string{"main", "master"} {
		for _, have := range st.Branches {
			if have == b {
				return b
			}
		}
	}
	return ""
}

// parseStatus parses `git status --porcelain=v2 --branch -z`.
func parseStatus(out string, st *State) {
	fields := strings.Split(out, "\x00")
	for i := 0; i < len(fields); i++ {
		f := fields[i]
		if f == "" {
			continue
		}
		switch f[0] {
		case '#':
			parseHeader(f, st)
		case '1':
			// 1 XY sub mH mI mW hH hI path
			parts := strings.SplitN(f, " ", 9)
			if len(parts) == 9 {
				addChange(st, parts[1], parts[8])
			}
		case '2':
			// 2 XY sub mH mI mW hH hI Xscore path, then NUL origPath
			parts := strings.SplitN(f, " ", 10)
			if len(parts) == 10 {
				addChange(st, parts[1], parts[9])
			}
			i++ // skip original path
		case 'u':
			// u XY sub m1 m2 m3 mW h1 h2 h3 path
			parts := strings.SplitN(f, " ", 11)
			if len(parts) == 11 {
				st.Conflicted = append(st.Conflicted, parts[10])
			}
		case '?':
			st.Untracked = append(st.Untracked, f[2:])
		}
	}
}

func parseHeader(f string, st *State) {
	parts := strings.Fields(f)
	if len(parts) < 3 {
		return
	}
	switch parts[1] {
	case "branch.oid":
		st.HasCommits = parts[2] != "(initial)"
	case "branch.head":
		if parts[2] == "(detached)" {
			st.Detached = true
		} else {
			st.Branch = parts[2]
		}
	case "branch.upstream":
		st.Upstream = parts[2]
		st.UpstreamGone = true // until branch.ab shows the upstream exists
	case "branch.ab":
		st.UpstreamGone = false
		if len(parts) == 4 {
			st.Ahead, _ = strconv.Atoi(strings.TrimPrefix(parts[2], "+"))
			st.Behind, _ = strconv.Atoi(strings.TrimPrefix(parts[3], "-"))
		}
	}
}

func addChange(st *State, xy, path string) {
	if len(xy) != 2 {
		return
	}
	if xy[0] != '.' {
		st.Staged = append(st.Staged, path)
	}
	if xy[1] != '.' {
		st.Unstaged = append(st.Unstaged, path)
	}
}

func detectOp(gitDir string) Op {
	exists := func(name string) bool {
		_, err := os.Stat(filepath.Join(gitDir, name))
		return err == nil
	}
	switch {
	case exists("rebase-merge"), exists("rebase-apply"):
		return OpRebase
	case exists("MERGE_HEAD"):
		return OpMerge
	case exists("CHERRY_PICK_HEAD"):
		return OpCherryPick
	case exists("REVERT_HEAD"):
		return OpRevert
	}
	return OpNone
}
