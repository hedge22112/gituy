package builder

import (
	"reflect"
	"testing"

	"github.com/hedge22112/gituy/internal/spec"
)

func specs(t *testing.T) *spec.Set {
	t.Helper()
	s, err := spec.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestBuild(t *testing.T) {
	set := specs(t)
	cases := []struct {
		name     string
		id       string
		flags    []string
		values   map[string]string
		args     map[string][]string
		want     []string
		problems int
	}{
		{name: "plain", id: "status", want: []string{"git", "status"}},
		{name: "pull rebase", id: "pull", flags: []string{"--rebase", "--autostash"},
			args: map[string][]string{"remote": {"origin"}, "branch": {"main"}},
			want: []string{"git", "pull", "--rebase", "--autostash", "origin", "main"}},
		{name: "commit message", id: "commit", flags: []string{"-m"}, values: map[string]string{"-m": "fix: it"},
			want: []string{"git", "commit", "-m", "fix: it"}},
		{name: "commit missing message", id: "commit", flags: []string{"-m"},
			want: []string{"git", "commit"}, problems: 1},
		{name: "paths after dashdash", id: "add", args: map[string][]string{"paths": {"a.go", "-weird"}},
			want: []string{"git", "add", "--", "a.go", "-weird"}},
		{name: "required arg", id: "restore", want: []string{"git", "restore"}, problems: 1},
		{name: "subcommand + fixed", id: "merge-continue", want: []string{"git", "merge", "--continue"}},
		{name: "default flag", id: "branch-delete", args: map[string][]string{"branch": {"old"}},
			want: []string{"git", "branch", "--delete", "old"}},
		{name: "stash", id: "stash-push", flags: []string{"--include-untracked"},
			want: []string{"git", "stash", "push", "--include-untracked"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			cmd := set.Get(c.id)
			sel := NewSelection(cmd)
			for _, f := range c.flags {
				sel.Toggle(cmd, f)
			}
			for k, v := range c.values {
				sel.Values[k] = v
			}
			for k, v := range c.args {
				sel.Args[k] = v
			}
			got, problems := Build(cmd, sel)
			if !reflect.DeepEqual(got, c.want) {
				t.Errorf("argv = %v, want %v", got, c.want)
			}
			if len(problems) != c.problems {
				t.Errorf("problems = %v, want %d", problems, c.problems)
			}
		})
	}
}

func TestToggleRadioGroup(t *testing.T) {
	cmd := specs(t).Get("reset")
	sel := NewSelection(cmd)
	sel.Toggle(cmd, "--soft")
	sel.Toggle(cmd, "--hard")
	if sel.Flags["--soft"] || !sel.Flags["--hard"] {
		t.Fatalf("flags = %v", sel.Flags)
	}
	if Danger(cmd, sel) != spec.DangerHigh {
		t.Errorf("danger = %q", Danger(cmd, sel))
	}
	sel.Toggle(cmd, "--hard")
	if len(sel.Flags) != 0 {
		t.Fatalf("flags = %v", sel.Flags)
	}
}

func TestQuoteParseRoundTrip(t *testing.T) {
	argv := []string{"git", "commit", "-m", "it's a \"test\" $HOME"}
	got, err := Parse(Quote(argv))
	if err != nil || !reflect.DeepEqual(got, argv) {
		t.Fatalf("round trip = %v, %v (quoted %s)", got, err, Quote(argv))
	}
	if _, err := Parse("rm -rf /"); err == nil {
		t.Error("non-git command should be rejected")
	}
}

func TestCloneIsDeep(t *testing.T) {
	cmd := specs(t).Get("add")
	a := NewSelection(cmd)
	a.Args["paths"] = []string{"x"}
	b := a.Clone()
	b.Args["paths"][0] = "y"
	b.Toggle(cmd, "--all")
	if a.Args["paths"][0] != "x" || a.Flags["--all"] {
		t.Fatal("clone shares state with original")
	}
}
