package builder

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kballard/go-shellquote"
)

func TestMatch(t *testing.T) {
	set := specs(t)
	cases := []struct {
		line     string
		prefer   string
		id       string
		flags    []string
		values   map[string]string
		args     map[string][]string
		extra    []string
		trailing []string
	}{
		{line: `git commit --amend -m "fix it"`, id: "commit",
			flags: []string{"--amend", "-m"}, values: map[string]string{"-m": "fix it"}},
		{line: `git branch -D old`, id: "branch-delete", flags: []string{"-D"},
			args: map[string][]string{"branch": {"old"}}},
		{line: `git merge --continue`, id: "merge-continue"},
		{line: `git push --force-with-lease --foo origin main`, id: "push",
			flags: []string{"--force-with-lease"}, extra: []string{"--foo"},
			args: map[string][]string{"remote": {"origin"}, "branch": {"main"}}},
		{line: `git log --oneline main..HEAD -- f.go`, id: "log", flags: []string{"--oneline"},
			args: map[string][]string{"range": {"main..HEAD"}}, trailing: []string{"--", "f.go"}},
		{line: `git add -- "a b.go" c.go`, id: "add",
			args: map[string][]string{"paths": {"a b.go", "c.go"}}},
		{line: `git log -n 5`, id: "log", flags: []string{"-n"}, values: map[string]string{"-n": "5"}},
		{line: `git restore --source=HEAD~2 f.go`, id: "restore", flags: []string{"--source"},
			values: map[string]string{"--source": "HEAD~2"}, args: map[string][]string{"paths": {"f.go"}}},
		{line: `git switch -`, id: "switch", args: map[string][]string{"branch": {"-"}}},
		{line: `git frobnicate --x y`, id: "custom", trailing: []string{"--x", "y"}},
	}
	for _, c := range cases {
		t.Run(c.line, func(t *testing.T) {
			argv, err := shellquote.Split(c.line)
			if err != nil {
				t.Fatal(err)
			}
			cmd, sel := Match(set, argv, set.Get(c.prefer))
			if cmd.ID != c.id {
				t.Fatalf("matched %s, want %s", cmd.ID, c.id)
			}
			flags := map[string]bool{}
			for _, f := range c.flags {
				flags[f] = true
			}
			if !reflect.DeepEqual(sel.Flags, flags) {
				t.Errorf("flags = %v, want %v", sel.Flags, flags)
			}
			if c.values == nil {
				c.values = map[string]string{}
			}
			if !reflect.DeepEqual(sel.Values, c.values) {
				t.Errorf("values = %v, want %v", sel.Values, c.values)
			}
			if c.args == nil {
				c.args = map[string][]string{}
			}
			if !reflect.DeepEqual(sel.Args, c.args) {
				t.Errorf("args = %v, want %v", sel.Args, c.args)
			}
			if !reflect.DeepEqual(sel.Extra, c.extra) || !reflect.DeepEqual(sel.Trailing, c.trailing) {
				t.Errorf("extra = %v trailing = %v, want %v %v", sel.Extra, sel.Trailing, c.extra, c.trailing)
			}
			// Unknown words are kept, so the command is never changed
			// beyond reordering known flags.
			if c.id == "custom" {
				if got, _ := Build(cmd, sel); !reflect.DeepEqual(got, argv) {
					t.Errorf("custom rebuilt as %v", got)
				}
			}
		})
	}
}

func TestMatchReportsGroupConflict(t *testing.T) {
	set := specs(t)
	cmd, sel := Match(set, []string{"git", "reset", "--soft", "--hard"}, nil)
	_, problems := Build(cmd, sel)
	if len(problems) != 1 || !strings.Contains(problems[0], "--hard") {
		t.Fatalf("problems = %v", problems)
	}
}

// TestRoundTripEverySpec builds a command from every spec with every
// option and argument set, parses it back, and checks it rebuilds to the
// same argv with nothing left over.
func TestRoundTripEverySpec(t *testing.T) {
	set := specs(t)
	for _, c := range set.All() {
		sel := NewSelection(c)
		groups := map[string]bool{}
		for _, o := range c.Options {
			if o.Group != "" {
				if groups[o.Group] {
					continue
				}
				groups[o.Group] = true
			}
			if !sel.Flags[o.Flag] {
				sel.Toggle(c, o.Flag)
			}
			if o.TakesValue() {
				sel.Values[o.Flag] = "v-" + strings.TrimLeft(o.Flag, "-")
			}
		}
		for _, a := range c.Args {
			sel.Args[a.Name] = []string{"a-" + a.Name}
			if a.Multi {
				sel.Args[a.Name] = append(sel.Args[a.Name], "b-"+a.Name)
			}
		}
		argv, _ := Build(c, sel)
		got, back := Match(set, argv, c)
		rebuilt, _ := Build(got, back)
		if !reflect.DeepEqual(rebuilt, argv) || len(back.Extra)+len(back.Trailing) > 0 {
			t.Errorf("%s: %v -> %s %v (extra %v trailing %v)", c.ID, argv, got.ID, rebuilt, back.Extra, back.Trailing)
		}
	}
}
