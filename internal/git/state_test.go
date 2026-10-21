package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestParseStatus(t *testing.T) {
	out := strings.Join([]string{
		"# branch.oid 1234567890abcdef",
		"# branch.head main",
		"# branch.upstream origin/main",
		"# branch.ab +2 -3",
		"1 M. N... 100644 100644 100644 aaa bbb staged.go",
		"1 .M N... 100644 100644 100644 aaa bbb has space.go",
		"1 MM N... 100644 100644 100644 aaa bbb both.go",
		"2 R. N... 100644 100644 100644 aaa bbb R100 new.go", "old.go",
		"u UU N... 100644 100644 100644 100644 aaa bbb ccc conflict.go",
		"? untracked.txt",
		"",
	}, "\x00")
	var st State
	parseStatus(out, &st)

	want := State{
		Branch: "main", HasCommits: true, Upstream: "origin/main", Ahead: 2, Behind: 3,
		Staged:     []string{"staged.go", "both.go", "new.go"},
		Unstaged:   []string{"has space.go", "both.go"},
		Conflicted: []string{"conflict.go"},
		Untracked:  []string{"untracked.txt"},
	}
	if !reflect.DeepEqual(st, want) {
		t.Fatalf("got  %+v\nwant %+v", st, want)
	}
}

func TestParseStatusDetachedInitial(t *testing.T) {
	var st State
	parseStatus("# branch.oid (initial)\x00# branch.head (detached)\x00", &st)
	if st.HasCommits || !st.Detached || st.Branch != "" {
		t.Fatalf("got %+v", st)
	}
}

func TestParseVersion(t *testing.T) {
	for in, want := range map[string][2]int{
		"git version 2.43.0\n":                 {2, 43},
		"git version 2.39.3 (Apple Git-145)\n": {2, 39},
		"git version 2.45.1.windows.1\n":       {2, 45},
	} {
		major, minor, err := parseVersion(in)
		if err != nil || major != want[0] || minor != want[1] {
			t.Errorf("parseVersion(%q) = %d.%d, %v", in, major, minor, err)
		}
	}
}

func TestFetchArgs(t *testing.T) {
	base := []string{"-c", "gc.auto=0", "fetch", "--quiet", "--no-auto-maintenance"}
	cases := []struct {
		o    FetchOptions
		tail []string
	}{
		{FetchOptions{Prune: true}, []string{"--prune", "--no-tags", "--all"}},
		{FetchOptions{Remotes: []string{"origin"}, Tags: true}, []string{"origin"}},
		{FetchOptions{Remotes: []string{"a", "b"}}, []string{"--no-tags", "--multiple", "a", "b"}},
	}
	for _, c := range cases {
		got := FetchArgs(c.o)
		want := append(append([]string{}, base...), c.tail...)
		if !reflect.DeepEqual(got, want) {
			t.Errorf("FetchArgs(%+v) = %v, want %v", c.o, got, want)
		}
	}
}

func TestDiffRefs(t *testing.T) {
	before := map[string]string{"origin/main": "a", "origin/gone": "b", "origin/same": "c"}
	after := map[string]string{"origin/main": "z", "origin/new": "d", "origin/same": "c"}
	got := DiffRefs(before, after)
	want := []RefChange{
		{Ref: "origin/gone", Old: "b"},
		{Ref: "origin/main", Old: "a", New: "z"},
		{Ref: "origin/new", New: "d"},
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

// --- tests against real repositories ---

type repo struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T, dir string) *repo {
	t.Helper()
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	r := &repo{t: t, dir: dir}
	r.git("init", "-q", "-b", "main")
	r.git("config", "user.email", "test@example.com")
	r.git("config", "user.name", "Test")
	r.git("config", "commit.gpgsign", "false")
	return r
}

func (r *repo) git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return string(out)
}

func (r *repo) gitMayFail(args ...string) {
	cmd := exec.Command("git", args...)
	cmd.Dir = r.dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_NOSYSTEM=1")
	_ = cmd.Run()
}

func (r *repo) write(name, content string) {
	r.t.Helper()
	if err := os.WriteFile(filepath.Join(r.dir, name), []byte(content), 0o644); err != nil {
		r.t.Fatal(err)
	}
}

func (r *repo) commit(name, content, msg string) {
	r.write(name, content)
	r.git("add", name)
	r.git("commit", "-q", "-m", msg)
}

func (r *repo) state() State {
	r.t.Helper()
	st, err := Runner{Dir: r.dir, Background: true}.ReadState(context.Background())
	if err != nil {
		r.t.Fatal(err)
	}
	return st
}

func TestStateMergeConflict(t *testing.T) {
	r := newRepo(t, t.TempDir())
	r.commit("f.txt", "base\n", "base")
	r.git("switch", "-q", "-c", "other")
	r.commit("f.txt", "other\n", "other")
	r.git("switch", "-q", "main")
	r.commit("f.txt", "main\n", "main")
	r.gitMayFail("merge", "other")

	st := r.state()
	if st.Op != OpMerge {
		t.Errorf("op = %q, want merge", st.Op)
	}
	if !reflect.DeepEqual(st.Conflicted, []string{"f.txt"}) {
		t.Errorf("conflicted = %v", st.Conflicted)
	}
}

func TestStateRebaseConflict(t *testing.T) {
	r := newRepo(t, t.TempDir())
	r.commit("f.txt", "base\n", "base")
	r.git("switch", "-q", "-c", "other")
	r.commit("f.txt", "other\n", "other")
	r.git("switch", "-q", "main")
	r.commit("f.txt", "main\n", "main")
	r.gitMayFail("rebase", "other")

	if st := r.state(); st.Op != OpRebase || len(st.Conflicted) != 1 {
		t.Errorf("got op=%q conflicted=%v", st.Op, st.Conflicted)
	}
}

func TestStateDivergedFromRemote(t *testing.T) {
	root := t.TempDir()
	remote := filepath.Join(root, "remote.git")
	newRepo(t, remote).git("config", "core.bare", "true")

	a := newRepo(t, filepath.Join(root, "a"))
	a.commit("f.txt", "1\n", "one")
	a.git("remote", "add", "origin", remote)
	a.git("push", "-q", "-u", "origin", "main")

	b := newRepo(t, filepath.Join(root, "b"))
	b.git("remote", "add", "origin", remote)
	b.git("fetch", "-q", "origin")
	b.git("switch", "-q", "-c", "main", "--track", "origin/main")
	b.commit("g.txt", "theirs\n", "theirs")
	b.git("push", "-q")

	a.commit("h.txt", "mine\n", "mine")
	a.write("dirty.txt", "x")

	// a background fetch then sees both sides moved
	runner := Runner{Dir: a.dir, Background: true}
	before, _ := runner.RemoteRefs(context.Background())
	if _, err := runner.Run(context.Background(), FetchArgs(FetchOptions{Prune: true})...); err != nil {
		t.Fatal(err)
	}
	after, _ := runner.RemoteRefs(context.Background())
	if ch := DiffRefs(before, after); len(ch) != 1 || ch[0].Ref != "origin/main" {
		t.Errorf("changes = %+v", ch)
	}

	st := a.state()
	if st.Ahead != 1 || st.Behind != 1 || st.Upstream != "origin/main" {
		t.Errorf("ahead/behind = %d/%d upstream=%q", st.Ahead, st.Behind, st.Upstream)
	}
	if st.HeadPushed {
		t.Error("HEAD should not be pushed")
	}
	if !reflect.DeepEqual(st.Untracked, []string{"dirty.txt"}) || !reflect.DeepEqual(st.Remotes, []string{"origin"}) {
		t.Errorf("untracked=%v remotes=%v", st.Untracked, st.Remotes)
	}
}

func TestStateDetachedWithStash(t *testing.T) {
	r := newRepo(t, t.TempDir())
	r.commit("f.txt", "1\n", "one")
	r.commit("f.txt", "2\n", "two")
	r.write("f.txt", "3\n")
	r.git("stash", "-q")
	r.git("switch", "-q", "--detach", "HEAD~1")

	st := r.state()
	if !st.Detached || st.StashCount != 1 || !st.Clean() {
		t.Errorf("got %+v", st)
	}
}
