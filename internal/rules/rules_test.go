package rules

import (
	"strings"
	"testing"

	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/forge"
	"github.com/hedge22112/gituy/internal/git"
	"github.com/hedge22112/gituy/internal/spec"
)

func engine(t *testing.T) *Engine {
	t.Helper()
	specs, err := spec.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	e, err := Builtin(specs)
	if err != nil {
		t.Fatal(err)
	}
	return e
}

func commands(sugs []Suggestion) []string {
	var out []string
	for _, s := range sugs {
		argv, _ := builder.Build(s.Command, s.Selection)
		out = append(out, strings.Join(argv, " "))
	}
	return out
}

func TestSuggestions(t *testing.T) {
	e := engine(t)
	cases := []struct {
		name  string
		state git.State
		first string   // expected top suggestion
		has   []string // must appear somewhere
		not   []string // must not appear
	}{
		{
			name:  "merge conflict",
			state: git.State{Branch: "main", HasCommits: true, Op: git.OpMerge, Conflicted: []string{"a.go", "b.go"}},
			first: "git add -- a.go",
			has:   []string{"git add -- b.go", "git checkout --ours -- a.go b.go", "git merge --abort"},
			not:   []string{"git merge --continue", "git add --all"},
		},
		{
			name:  "merge resolved",
			state: git.State{Branch: "main", HasCommits: true, Op: git.OpMerge},
			first: "git merge --continue",
		},
		{
			name:  "rebase resolved",
			state: git.State{Branch: "main", HasCommits: true, Op: git.OpRebase},
			first: "git rebase --continue",
			has:   []string{"git rebase --abort"},
		},
		{
			name:  "diverged",
			state: git.State{Branch: "main", HasCommits: true, Upstream: "origin/main", Ahead: 2, Behind: 1, Remotes: []string{"origin"}},
			first: "git pull --rebase",
			has:   []string{"git pull --no-rebase"},
			not:   []string{"git push"},
		},
		{
			name:  "behind only",
			state: git.State{Branch: "main", HasCommits: true, HeadPushed: true, Upstream: "origin/main", Behind: 3, Remotes: []string{"origin"}},
			first: "git pull --ff-only",
		},
		{
			name:  "ahead only",
			state: git.State{Branch: "main", HasCommits: true, Upstream: "origin/main", Ahead: 1, Remotes: []string{"origin"}},
			first: "git push",
			has:   []string{"git reset --soft HEAD~1"},
		},
		{
			name:  "no upstream prefers origin",
			state: git.State{Branch: "feat", HasCommits: true, Remotes: []string{"fork", "origin"}},
			has:   []string{"git push --set-upstream origin feat"},
		},
		{
			name:  "staged and unstaged",
			state: git.State{Branch: "main", HasCommits: true, HeadPushed: true, Staged: []string{"a"}, Unstaged: []string{"b"}},
			first: "git commit", // -m without a message yet
			has:   []string{"git add --all", "git restore --staged", "git stash push --include-untracked"},
		},
		{
			name:  "clean with stash",
			state: git.State{Branch: "main", HasCommits: true, HeadPushed: true, StashCount: 2},
			first: "git stash pop",
		},
		{
			name: "rebased after pushing",
			state: git.State{Branch: "feat", HasCommits: true, Upstream: "origin/feat", Ahead: 3, Behind: 2,
				Remotes: []string{"origin"}, LastAction: "rebase (finish): returning to refs/heads/feat"},
			first: "git push --force-with-lease",
			has:   []string{"git pull --rebase"},
		},
		{
			name: "amended after pushing",
			state: git.State{Branch: "feat", HasCommits: true, Upstream: "origin/feat", Ahead: 1, Behind: 1,
				Remotes: []string{"origin"}, LastAction: "commit (amend): fix"},
			first: "git push --force-with-lease",
		},
		{
			name: "diverged without rewriting",
			state: git.State{Branch: "feat", HasCommits: true, Upstream: "origin/feat", Ahead: 1, Behind: 1,
				Remotes: []string{"origin"}, LastAction: "commit: more work"},
			first: "git pull --rebase",
			not:   []string{"git push --force-with-lease"},
		},
		{
			name: "upstream deleted",
			state: git.State{Branch: "feat", HasCommits: true, HeadPushed: true, Upstream: "origin/feat", UpstreamGone: true,
				DefaultBranch: "main", Remotes: []string{"origin"}},
			first: "git switch main",
			has:   []string{"git branch --unset-upstream"},
			not:   []string{"git push --set-upstream origin feat"},
		},
		{
			name: "housekeeping on default branch",
			state: git.State{Branch: "main", HasCommits: true, HeadPushed: true, DefaultBranch: "main",
				MergedBranches: []string{"done"}, GoneBranches: []string{"old-pr"}},
			has: []string{"git branch -d done", "git branch -d old-pr"},
		},
		{
			name: "merged branches ignored off the default branch",
			state: git.State{Branch: "feat", HasCommits: true, HeadPushed: true, DefaultBranch: "main",
				MergedBranches: []string{"main"}},
			not: []string{"git branch -d main"},
		},
		{
			name:  "not a repository",
			state: git.State{NotRepo: true},
			first: "git init",
			has:   []string{"git clone"},
		},
		{
			name:  "detached",
			state: git.State{Detached: true, HasCommits: true, HeadPushed: true},
			first: "git switch --create",
			has:   []string{"git switch -"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := commands(e.Suggest(c.state))
			if c.first != "" && (len(got) == 0 || got[0] != c.first) {
				t.Errorf("first = %q, want %q (all: %q)", first(got), c.first, got)
			}
			for _, h := range c.has {
				if !contains(got, h) {
					t.Errorf("missing %q in %q", h, got)
				}
			}
			for _, n := range c.not {
				if contains(got, n) {
					t.Errorf("unexpected %q in %q", n, got)
				}
			}
		})
	}
}

func TestExplainIsTemplated(t *testing.T) {
	st := git.State{Branch: "feat", HasCommits: true, Upstream: "origin/feat", UpstreamGone: true, DefaultBranch: "main"}
	for _, s := range engine(t).Suggest(st) {
		if strings.Contains(s.Explain, "{{") {
			t.Errorf("%s: explain not expanded: %q", s.ID, s.Explain)
		}
		if s.ID == "upstream-gone-switch" && !strings.Contains(s.Explain, "Switch to main") {
			t.Errorf("explain = %q", s.Explain)
		}
	}
}

func TestSuggestionsDoNotShareSelections(t *testing.T) {
	e := engine(t)
	st := git.State{Op: git.OpMerge, Conflicted: []string{"a", "b"}}
	sugs := e.Suggest(st)
	sugs[0].Selection.Args["paths"][0] = "changed"
	again := e.Suggest(st)
	if again[0].Selection.Args["paths"][0] != "a" {
		t.Fatal("suggestions share state between calls")
	}
}

func TestLoadRejectsUnknownFlag(t *testing.T) {
	specs, _ := spec.Builtin()
	_, err := Load([]byte(`[{id: x, when: "true", title: t, command: push, preset: {flags: [--nope]}}]`), specs)
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func first(s []string) string {
	if len(s) == 0 {
		return ""
	}
	return s[0]
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

func TestForgeSuggestions(t *testing.T) {
	e := engine(t)
	pushed := git.State{Branch: "feat", HasCommits: true, HeadPushed: true, Upstream: "origin/feat",
		DefaultBranch: "main", Remotes: []string{"origin"}}
	gh := func(pr *forge.PR) forge.Status {
		return forge.Status{Kind: forge.GitHub, Host: "github.com", Installed: true, Authed: true, PR: pr}
	}
	glab := func(pr *forge.PR) forge.Status {
		return forge.Status{Kind: forge.GitLab, Host: "gitlab.com", Installed: true, Authed: true, PR: pr}
	}
	open := func(mod func(*forge.PR)) *forge.PR {
		p := &forge.PR{Number: 42, State: "open", Checks: "passing", Base: "main"}
		mod(p)
		return p
	}
	cases := []struct {
		name  string
		st    git.State
		fs    forge.Status
		first string
		has   []string
		not   []string
	}{
		{name: "pushed, no PR", st: pushed, fs: gh(nil), first: "gh pr create --fill"},
		{name: "pushed, no MR", st: pushed, fs: glab(nil), first: "glab mr create --fill"},
		{name: "not pushed yet: push first", st: git.State{Branch: "feat", HasCommits: true, DefaultBranch: "main", Remotes: []string{"origin"}},
			fs: gh(nil), first: "git push --set-upstream origin feat", not: []string{"gh pr create --fill"}},
		{name: "on default branch: no PR suggestion", st: git.State{Branch: "main", HasCommits: true, HeadPushed: true,
			Upstream: "origin/main", DefaultBranch: "main", Remotes: []string{"origin"}},
			fs: gh(nil), has: []string{"gh issue develop --checkout"}, not: []string{"gh pr create --fill"}},
		{name: "failing checks", st: pushed, fs: gh(open(func(p *forge.PR) { p.Checks = "failing" })),
			first: "gh pr checks", has: []string{"gh run view --log-failed"}, not: []string{"gh pr merge --squash --delete-branch"}},
		{name: "changes requested", st: pushed, fs: gh(open(func(p *forge.PR) { p.Review = "changes_requested" })),
			first: "gh pr view --comments"},
		{name: "approved and green", st: pushed, fs: gh(open(func(p *forge.PR) { p.Review = "approved" })),
			first: "gh pr merge --squash --delete-branch", has: []string{"gh pr view --web"}},
		{name: "approved but unpushed commits", st: func() git.State { s := pushed; s.Ahead = 1; return s }(),
			fs: gh(open(func(p *forge.PR) { p.Review = "approved" })), first: "git push",
			not: []string{"gh pr merge --squash --delete-branch"}},
		{name: "draft", st: pushed, fs: gh(open(func(p *forge.PR) { p.Draft = true; p.Review = "approved" })),
			has: []string{"gh pr ready"}, not: []string{"gh pr merge --squash --delete-branch"}},
		{name: "behind base", st: pushed, fs: gh(open(func(p *forge.PR) { p.Behind = true })), first: "gh pr update-branch"},
		{name: "conflicts", st: pushed, fs: gh(open(func(p *forge.PR) { p.Conflicts = true; p.Behind = true })),
			first: "git pull --rebase origin main", not: []string{"gh pr update-branch"}},
		{name: "pending", st: pushed, fs: gh(open(func(p *forge.PR) { p.Checks = "pending" })), has: []string{"gh pr checks --watch"}},
		{name: "merged: one switch suggestion, not two",
			st: func() git.State { s := pushed; s.UpstreamGone = true; return s }(),
			fs: gh(&forge.PR{Number: 42, State: "merged"}), first: "git switch main"},
		{name: "closed", st: pushed, fs: gh(&forge.PR{Number: 42, State: "closed"}), has: []string{"gh pr reopen"}},
		{name: "review queue", st: pushed, fs: func() forge.Status { s := gh(open(func(*forge.PR) {})); s.ReviewQueue = 2; return s }(),
			has: []string{"gh pr checkout"}},
		{name: "logged out", st: pushed, fs: forge.Status{Kind: forge.GitHub, Host: "ghe.corp", Installed: true},
			has: []string{"gh auth login --hostname ghe.corp"}, not: []string{"gh pr create --fill"}},
		{name: "not installed: nothing forge-related", st: pushed, fs: forge.Status{Kind: forge.GitHub, Host: "github.com"},
			not: []string{"gh auth login --hostname github.com", "gh pr create --fill"}},
		{name: "glab failing pipeline", st: pushed, fs: glab(open(func(p *forge.PR) { p.Checks = "failing" })),
			first: "glab ci status", has: []string{"glab ci trace"}},
		{name: "glab ready to merge", st: pushed, fs: glab(open(func(p *forge.PR) { p.Review = "approved" })),
			first: "glab mr merge --squash --remove-source-branch"},
		{name: "glab needs rebase", st: pushed, fs: glab(open(func(p *forge.PR) { p.Behind = true })), first: "glab mr rebase"},
		{name: "glab draft", st: pushed, fs: glab(open(func(p *forge.PR) { p.Draft = true })), has: []string{"glab mr update --ready"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			sugs := e.SuggestWith(c.st, c.fs)
			got := commands(sugs)
			if c.first != "" && first(got) != c.first {
				t.Errorf("first = %q, want %q (all: %q)", first(got), c.first, got)
			}
			for _, h := range c.has {
				if !contains(got, h) {
					t.Errorf("missing %q in %q", h, got)
				}
			}
			for _, n := range c.not {
				if contains(got, n) {
					t.Errorf("unexpected %q in %q", n, got)
				}
			}
			seen := map[string]bool{}
			for _, g := range got {
				if seen[g] {
					t.Errorf("duplicate suggestion %q", g)
				}
				seen[g] = true
			}
		})
	}
}
