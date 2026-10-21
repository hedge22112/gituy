package ui

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hedge22112/gituy/internal/bg"
	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
	"github.com/hedge22112/gituy/internal/rules"
	"github.com/hedge22112/gituy/internal/spec"
)

func newModel(t *testing.T, st git.State) *Model {
	t.Helper()
	specs, err := spec.Builtin()
	if err != nil {
		t.Fatal(err)
	}
	eng, err := rules.Builtin(specs)
	if err != nil {
		t.Fatal(err)
	}
	m := New(Model{Specs: specs, Rules: eng, Cfg: config.Default()})
	m.Update(tea.WindowSizeMsg{Width: 100, Height: 30})
	m.Update(bg.StateMsg{State: st})
	return m
}

func keys(m *Model, ks ...string) {
	for _, k := range ks {
		var msg tea.KeyMsg
		switch k {
		case "enter":
			msg = tea.KeyMsg{Type: tea.KeyEnter}
		case "right":
			msg = tea.KeyMsg{Type: tea.KeyRight}
		case "down":
			msg = tea.KeyMsg{Type: tea.KeyDown}
		case "esc":
			msg = tea.KeyMsg{Type: tea.KeyEsc}
		case "space":
			msg = tea.KeyMsg{Type: tea.KeySpace, Runes: []rune{' '}}
		default:
			msg = tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(k)}
		}
		m.Update(msg)
	}
}

func preview(m *Model) string {
	argv, _, _ := m.top().preview(m)
	return strings.Join(argv, " ")
}

var mergeDone = git.State{Branch: "main", HasCommits: true, Op: git.OpMerge}

func TestBurstOfKeysIsNotReadAsShortcuts(t *testing.T) {
	m := newModel(t, mergeDone)
	// "/reset" arriving as one event must filter, not run ("r") anything.
	keys(m, "/reset")
	h := m.top().(*home)
	if !h.filtering || h.filter != "reset" {
		t.Fatalf("filtering=%v filter=%q", h.filtering, h.filter)
	}
	if m.Result.Action != ActionQuit || m.done {
		t.Fatal("burst triggered an action")
	}
}

func TestRunTopSuggestion(t *testing.T) {
	m := newModel(t, mergeDone)
	if got := preview(m); got != "git merge --continue" {
		t.Fatalf("preview = %q", got)
	}
	keys(m, "r")
	if m.Result.Action != ActionRun || !reflect.DeepEqual(m.Result.Argv, []string{"git", "merge", "--continue"}) {
		t.Fatalf("result = %+v", m.Result)
	}
}

func TestBuilderEditsDoNotLeakIntoSuggestion(t *testing.T) {
	m := newModel(t, git.State{Branch: "main", HasCommits: true, HeadPushed: true, Upstream: "origin/main", Behind: 2, Remotes: []string{"origin"}})
	if got := preview(m); got != "git pull --ff-only" {
		t.Fatalf("preview = %q", got)
	}
	keys(m, "right") // open builder
	// options: --rebase, --no-rebase, --ff-only, --autostash ...
	keys(m, "space", "down", "down", "down", "space")
	if got := preview(m); got != "git pull --rebase --autostash" {
		t.Fatalf("builder preview = %q", got)
	}
	keys(m, "esc")
	if got := preview(m); got != "git pull --ff-only" {
		t.Fatalf("suggestion changed to %q", got)
	}
}

func TestDangerousCommandNeedsConfirmation(t *testing.T) {
	m := newModel(t, git.State{Branch: "main", HasCommits: true, HeadPushed: true})
	keys(m, "/", "reset", "enter", "right")
	// reset options: --soft, --mixed, --hard
	keys(m, "down", "down", "space", "r")
	if _, ok := m.top().(*confirm); !ok {
		t.Fatalf("expected confirm screen, got %T", m.top())
	}
	keys(m, "n")
	if m.done {
		t.Fatal("cancel should not run")
	}
	keys(m, "r", "y")
	if !reflect.DeepEqual(m.Result.Argv, []string{"git", "reset", "--hard"}) {
		t.Fatalf("result = %+v", m.Result)
	}
}

func TestViewRenders(t *testing.T) {
	m := newModel(t, git.State{Branch: "main", HasCommits: true, Op: git.OpRebase, Conflicted: []string{"a.go"}})
	v := m.View()
	for _, want := range []string{"REBASE IN PROGRESS", "1 conflicted", "Mark a.go as resolved", "$ git add -- a.go", "fetch paused"} {
		if !strings.Contains(v, want) {
			t.Errorf("view missing %q:\n%s", want, v)
		}
	}
}
