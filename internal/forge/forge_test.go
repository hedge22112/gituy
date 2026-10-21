package forge

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/hedge22112/gituy/internal/git"
)

// The test binary doubles as a fake gh/glab: copied onto PATH under that
// name, it answers from a JSON file of canned responses (args -> output).
func TestMain(m *testing.M) {
	if responses := os.Getenv("GITUY_FAKE_CLI"); responses != "" {
		os.Exit(fakeCLI(responses))
	}
	os.Exit(m.Run())
}

type response struct {
	Stdout string `json:"stdout"`
	Stderr string `json:"stderr"`
	Code   int    `json:"code"`
}

func fakeCLI(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 2
	}
	var rs map[string]response
	json.Unmarshal(data, &rs)
	key := strings.Join(os.Args[1:], " ")
	r, ok := rs[key]
	if !ok {
		fmt.Fprintf(os.Stderr, "fake: unexpected command %q\n", key)
		return 3
	}
	if os.Getenv("GH_PROMPT_DISABLED") != "1" || os.Getenv("NO_PROMPT") != "true" {
		fmt.Fprintln(os.Stderr, "fake: prompts not disabled")
		return 4
	}
	fmt.Fprint(os.Stdout, r.Stdout)
	fmt.Fprint(os.Stderr, r.Stderr)
	return r.Code
}

// fakeTool installs a fake CLI named name answering with responses, and
// puts only it (plus git) on PATH.
func fakeTool(t *testing.T, name string, responses map[string]response) {
	t.Helper()
	dir := t.TempDir()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(dir, name)
	if runtime.GOOS == "windows" {
		target += ".exe"
	}
	copyFile(t, exe, target)
	data, _ := json.Marshal(responses)
	respFile := filepath.Join(dir, "responses.json")
	os.WriteFile(respFile, data, 0o644)
	t.Setenv("GITUY_FAKE_CLI", respFile)

	path := dir
	if g, err := exec.LookPath("git"); err == nil {
		path += string(os.PathListSeparator) + filepath.Dir(g)
	}
	t.Setenv("PATH", path)
}

func copyFile(t *testing.T, from, to string) {
	t.Helper()
	in, err := os.Open(from)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.OpenFile(to, os.O_CREATE|os.O_WRONLY, 0o755)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	out.Close()
}

func TestHostOf(t *testing.T) {
	for in, want := range map[string]string{
		"https://github.com/org/repo.git":         "github.com",
		"https://user@GitLab.example.com/g/r.git": "gitlab.example.com",
		"ssh://git@github.com:22/org/repo":        "github.com",
		"git@github.com:org/repo.git":             "github.com",
		"gitlab.com:group/sub/repo.git":           "gitlab.com",
		"/srv/git/repo.git":                       "",
		"../other":                                "",
		`C:\repos\thing`:                          "",
		"file:///tmp/repo.git":                    "",
	} {
		if got := HostOf(in); got != want {
			t.Errorf("HostOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestKindOf(t *testing.T) {
	over := map[string]string{"code.corp.example": "gitlab"}
	for host, want := range map[string]Kind{
		"github.com": GitHub, "github.corp.example": GitHub, "gitlab.com": GitLab,
		"gitlab.corp.example": GitLab, "code.corp.example": GitLab, "bitbucket.org": None,
	} {
		if got := KindOf(host, over); got != want {
			t.Errorf("KindOf(%q) = %q, want %q", host, got, want)
		}
	}
}

func readFixture(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestParseGHPR(t *testing.T) {
	cases := map[string]PR{
		"gh_pr_failing.json": {Number: 42, Title: "Add login", URL: "https://github.com/o/r/pull/42", State: "open",
			Review: "changes_requested", Checks: "failing", Base: "main"},
		"gh_pr_ready.json": {Number: 7, Title: "Fix typo", URL: "https://github.com/o/r/pull/7", State: "open",
			Review: "approved", Checks: "passing", Base: "main"},
		"gh_pr_draft_pending.json": {Number: 9, Title: "WIP", URL: "https://github.com/o/r/pull/9", State: "open",
			Draft: true, Review: "review_required", Checks: "pending", Behind: true, Base: "develop"},
		"gh_pr_merged.json": {Number: 3, Title: "Done", URL: "https://github.com/o/r/pull/3", State: "merged",
			Checks: "none", Conflicts: false, Base: "main"},
	}
	for file, want := range cases {
		got, err := parseGHPR([]byte(readFixture(t, file)))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if *got != want {
			t.Errorf("%s:\n got  %+v\n want %+v", file, *got, want)
		}
	}
}

func TestParseGLabMR(t *testing.T) {
	cases := map[string]PR{
		"glab_mr_open.json": {Number: 15, Title: "Draft: New API", URL: "https://gitlab.com/g/r/-/merge_requests/15",
			State: "open", Draft: true, Review: "review_required", Checks: "failing", Behind: false, Base: "main"},
		"glab_mr_mergeable.json": {Number: 16, Title: "Ready", URL: "https://gitlab.com/g/r/-/merge_requests/16",
			State: "open", Review: "approved", Checks: "passing", Base: "main"},
		"glab_mr_rebase.json": {Number: 17, Title: "Old", URL: "https://gitlab.com/g/r/-/merge_requests/17",
			State: "open", Checks: "pending", Behind: true, Conflicts: true, Base: "main"},
	}
	for file, want := range cases {
		got, err := parseGLabMR([]byte(readFixture(t, file)))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		if *got != want {
			t.Errorf("%s:\n got  %+v\n want %+v", file, *got, want)
		}
	}
}

func TestParseChoices(t *testing.T) {
	gh := `[{"number":5,"title":"Fix","headRefName":"fix-it"},{"number":6,"title":"More"}]`
	runs := `[{"databaseId":991,"displayTitle":"CI","status":"completed","conclusion":"failure"}]`
	glab := `[{"iid":12,"title":"MR","source_branch":"feat"}]`
	for in, want := range map[string]string{
		gh:   "5=Fix  [fix-it];6=More",
		runs: "991=CI  [failure]",
		glab: "12=MR  [feat]",
	} {
		cs, err := parseChoices([]byte(in))
		if err != nil {
			t.Fatal(err)
		}
		var parts []string
		for _, c := range cs {
			parts = append(parts, c.Value+"="+c.Label)
		}
		if got := strings.Join(parts, ";"); got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestStatusGitHub(t *testing.T) {
	fakeTool(t, "gh", map[string]response{
		"auth status --hostname github.com": {Stdout: "Logged in"},
		"pr view --json " + ghPRFields:      {Stdout: readFixture(t, "gh_pr_ready.json")},
		"pr list --search review-requested:@me --json number,title,headRefName --limit 50": {Stdout: `[{"number":1,"title":"a"},{"number":2,"title":"b"}]`},
	})
	c := &Client{Kind: GitHub, Host: "github.com", Dir: t.TempDir()}
	st := c.Status(context.Background(), true)
	if !st.Ready() || st.Err != nil || st.PR == nil || st.PR.Number != 7 || st.ReviewQueue != 2 {
		t.Fatalf("status = %+v pr = %+v", st, st.PR)
	}
}

func TestStatusNoPRAndNotLoggedIn(t *testing.T) {
	fakeTool(t, "gh", map[string]response{
		"auth status --hostname github.com": {Stdout: ""},
		"pr view --json " + ghPRFields:      {Stderr: `no pull requests found for branch "feat"`, Code: 1},
	})
	c := &Client{Kind: GitHub, Host: "github.com", Dir: t.TempDir()}
	if st := c.Status(context.Background(), false); !st.Ready() || st.PR != nil || st.Err != nil {
		t.Fatalf("no PR: %+v", st)
	}

	fakeTool(t, "gh", map[string]response{
		"auth status --hostname github.com": {Stderr: "You are not logged into any GitHub hosts. To log in, run: gh auth login", Code: 1},
	})
	c = &Client{Kind: GitHub, Host: "github.com", Dir: t.TempDir()}
	if st := c.Status(context.Background(), false); !st.Installed || st.Authed || st.Ready() {
		t.Fatalf("logged out: %+v", st)
	}
}

func TestStatusGitLabNoMR(t *testing.T) {
	fakeTool(t, "glab", map[string]response{
		"auth status --hostname gitlab.com": {},
		"mr view --output json":             {Stderr: `no open merge request available for "feat"`, Code: 1},
	})
	c := &Client{Kind: GitLab, Host: "gitlab.com", Dir: t.TempDir()}
	if st := c.Status(context.Background(), false); !st.Ready() || st.PR != nil || st.Err != nil {
		t.Fatalf("status = %+v", st)
	}
}

func TestNotInstalled(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	c := &Client{Kind: GitHub, Host: "github.com"}
	if st := c.Status(context.Background(), true); st.Installed || st.Ready() {
		t.Fatalf("status = %+v", st)
	}
}

func TestDetect(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not installed")
	}
	dir := t.TempDir()
	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
	}
	run("init", "-q")
	run("remote", "add", "origin", "git@gitlab.example.com:team/app.git")
	run("remote", "add", "mirror", "/srv/mirror.git")
	r := git.Runner{Dir: dir}
	ctx := context.Background()

	c := Detect(ctx, r, "origin", nil)
	if c == nil || c.Kind != GitLab || c.Host != "gitlab.example.com" {
		t.Fatalf("detect origin = %+v", c)
	}
	if c := Detect(ctx, r, "mirror", nil); c != nil {
		t.Errorf("local remote detected as %+v", c)
	}

	// An unremarkable self-hosted name is resolved by asking the CLI.
	run("remote", "set-url", "origin", "https://code.corp.example/team/app.git")
	fakeTool(t, "gh", map[string]response{"auth status --hostname code.corp.example": {}})
	if c := Detect(ctx, r, "origin", nil); c == nil || c.Kind != GitHub {
		t.Errorf("self-hosted = %+v", c)
	}
}
