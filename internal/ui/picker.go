package ui

import (
	"context"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hedge22112/gituy/internal/spec"
)

// choice is one pickable value.
type choice struct {
	Value string
	Label string // optional extra text, e.g. a commit subject
}

// choicesFor lists the values an argument can take, from repo state.
func choicesFor(m *Model, b *build, a *spec.Arg) []choice {
	st := m.state
	vals := func(list ...[]string) []choice {
		seen := map[string]bool{}
		var out []choice
		for _, l := range list {
			for _, v := range l {
				if !seen[v] {
					seen[v] = true
					out = append(out, choice{Value: v})
				}
			}
		}
		return out
	}
	switch a.Source {
	case "branches":
		return vals(st.Branches)
	case "all_branches":
		return vals(st.Branches, st.RemoteRefs)
	case "remote_branches":
		remote := ""
		if r := b.sel.Args["remote"]; len(r) > 0 {
			remote = r[0]
		}
		var names []string
		for _, ref := range st.RemoteRefs {
			rem, name, ok := strings.Cut(ref, "/")
			if ok && name != "HEAD" && (remote == "" || rem == remote) {
				names = append(names, name)
			}
		}
		return vals(names)
	case "remotes":
		return vals(st.Remotes)
	case "changed":
		if b.sel.Flags["--staged"] {
			return vals(st.Staged)
		}
		return vals(st.Unstaged, st.Untracked, st.Staged)
	case "unstaged":
		return vals(st.Unstaged)
	case "staged":
		return vals(st.Staged)
	case "untracked":
		return vals(st.Untracked)
	case "conflicted":
		return vals(st.Conflicted)
	case "commits":
		return gitChoices(m, []string{"log", "--format=%h%x09%s", "-n", "50"})
	case "stashes":
		return gitChoices(m, []string{"stash", "list", "--format=%gd%x09%s"})
	case "other_commits":
		// commits on other branches that aren't on this one yet
		return gitChoices(m, []string{"log", "--all", "--not", "HEAD", "--format=%h%x09%s (%D)", "-n", "50"})
	case "tags":
		return gitChoices(m, []string{"for-each-ref", "--sort=-creatordate", "--format=%(refname:short)%09%(contents:subject)", "refs/tags"})
	case "worktrees":
		return worktreeChoices(m)
	}
	return nil
}

func gitChoices(m *Model, args []string) []choice {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := m.Git.Run(ctx, args...)
	if err != nil {
		return nil
	}
	var cs []choice
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l == "" {
			continue
		}
		v, label, _ := strings.Cut(l, "\t")
		cs = append(cs, choice{Value: v, Label: label})
	}
	return cs
}

// worktreeChoices lists linked worktrees (not the main one, which can't
// be removed) from `git worktree list --porcelain`.
func worktreeChoices(m *Model) []choice {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := m.Git.Run(ctx, "worktree", "list", "--porcelain")
	if err != nil {
		return nil
	}
	var cs []choice
	for i, block := range strings.Split(strings.TrimSpace(out), "\n\n") {
		if i == 0 {
			continue // the main worktree
		}
		var c choice
		for _, l := range strings.Split(block, "\n") {
			if p, ok := strings.CutPrefix(l, "worktree "); ok {
				c.Value = p
			} else if b, ok := strings.CutPrefix(l, "branch refs/heads/"); ok {
				c.Label = b
			} else if l == "detached" {
				c.Label = "detached"
			}
		}
		if c.Value != "" {
			cs = append(cs, c)
		}
	}
	return cs
}

// picker selects one or more values, with type-to-filter. The typed text
// can also be used as a value directly (e.g. a new branch name).
type picker struct {
	arg      *spec.Arg
	choices  []choice
	picked   map[string]bool
	order    []string // picked values in selection order
	filter   string
	cur      int
	onChoose func([]string)
}

func newPicker(a *spec.Arg, choices []choice, current []string, onChoose func([]string)) *picker {
	p := &picker{arg: a, choices: choices, picked: map[string]bool{}, onChoose: onChoose}
	for _, v := range current {
		p.picked[v] = true
		p.order = append(p.order, v)
		found := false
		for _, c := range choices {
			found = found || c.Value == v
		}
		if !found {
			p.choices = append([]choice{{Value: v}}, p.choices...)
		}
	}
	return p
}

func (p *picker) capturesText() bool { return true }

func (p *picker) preview(m *Model) ([]string, []string, string) {
	// show the parent builder's command with the current picks applied
	parent, ok := m.stack[len(m.stack)-2].(*build)
	if !ok {
		return nil, nil, ""
	}
	sel := parent.sel.Clone()
	if vals := p.values(); len(vals) > 0 {
		sel.Args[p.arg.Name] = vals
	} else {
		delete(sel.Args, p.arg.Name)
	}
	pb := &build{cmd: parent.cmd, sel: sel}
	return pb.preview(m)
}

// visible returns the filtered choices. When the filter text isn't an
// existing choice, it's offered first as a custom value.
func (p *picker) visible() []choice {
	f := strings.ToLower(p.filter)
	var out []choice
	exact := false
	for _, c := range p.choices {
		if f == "" || strings.Contains(strings.ToLower(c.Value+" "+c.Label), f) {
			out = append(out, c)
		}
		exact = exact || c.Value == p.filter
	}
	if p.filter != "" && !exact {
		out = append([]choice{{Value: p.filter, Label: "use what you typed"}}, out...)
	}
	return out
}

func (p *picker) values() []string {
	var out []string
	for _, v := range p.order {
		if p.picked[v] {
			out = append(out, v)
		}
	}
	return out
}

func (p *picker) togglePick(v string) {
	if !p.arg.Multi {
		p.picked = map[string]bool{}
		p.order = nil
	}
	if p.picked[v] {
		delete(p.picked, v)
		return
	}
	p.picked[v] = true
	p.order = append(p.order, v)
}

func (p *picker) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	vis := p.visible()
	switch msg.String() {
	case "up":
		if p.cur > 0 {
			p.cur--
		}
	case "down":
		if p.cur < len(vis)-1 {
			p.cur++
		}
	case "esc", "left":
		m.pop()
	case "backspace":
		if p.filter != "" {
			p.filter = p.filter[:len(p.filter)-1]
			p.cur = 0
		} else {
			m.pop()
		}
	case " ", "tab":
		if p.cur < len(vis) {
			p.togglePick(vis[p.cur].Value)
		}
	case "ctrl+a":
		if p.arg.Multi {
			for _, c := range vis {
				if !p.picked[c.Value] {
					p.togglePick(c.Value)
				}
			}
		}
	case "ctrl+d":
		p.picked, p.order = map[string]bool{}, nil
	case "enter", "right":
		vals := p.values()
		if len(vals) == 0 && p.cur < len(vis) {
			vals = []string{vis[p.cur].Value}
		}
		if !p.arg.Multi && p.cur < len(vis) && msg.String() == "enter" {
			vals = []string{vis[p.cur].Value}
		}
		p.onChoose(vals)
		m.pop()
	default:
		if msg.Type == tea.KeyRunes {
			p.filter += string(msg.Runes)
			p.cur = 0
		}
	}
	return nil
}

func (p *picker) view(m *Model, height int) string {
	var s strings.Builder
	head := stBold.Render("<"+p.arg.Name+">") + "  " + stMuted.Render(p.arg.Desc)
	if p.arg.Multi {
		head += stMuted.Render("  (space to pick several, ctrl+a all, ctrl+d none)")
	}
	s.WriteString(head + "\n")
	s.WriteString(stPrompt.Render("> ") + p.filter + stAccent.Render("▏") + "\n")

	vis := p.visible()
	if len(vis) == 0 {
		s.WriteString(stMuted.Render("  nothing to choose from; type a value"))
		return s.String()
	}
	start, end := window(len(vis), p.cur, height-2)
	for i := start; i < end; i++ {
		c := vis[i]
		mark := "  "
		if p.picked[c.Value] {
			mark = stGood.Render("✓ ")
		}
		v := c.Value
		if i == p.cur {
			v = stSelected.Render(v)
		}
		line := cursor(i == p.cur) + mark + v
		if c.Label != "" {
			line += "  " + stMuted.Render(c.Label)
		}
		s.WriteString(line)
		if i < end-1 {
			s.WriteString("\n")
		}
	}
	return s.String()
}
