package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/rules"
	"github.com/hedge22112/gituy/internal/spec"
)

// build is the command builder: toggle options, fill arguments, run.
type build struct {
	cmd *spec.Command
	sel builder.Selection
	sug *rules.Suggestion
	cur int
}

type rowKind int

const (
	rowOption rowKind = iota
	rowArg
	rowExtra
	rowRun
)

type row struct {
	kind rowKind
	opt  *spec.Option
	arg  *spec.Arg
}

func newBuild(c *spec.Command, sel builder.Selection, sug *rules.Suggestion) *build {
	return &build{cmd: c, sel: sel.Clone(), sug: sug}
}

func (b *build) rows() []row {
	var rows []row
	for i := range b.cmd.Options {
		rows = append(rows, row{kind: rowOption, opt: &b.cmd.Options[i]})
	}
	for i := range b.cmd.Args {
		rows = append(rows, row{kind: rowArg, arg: &b.cmd.Args[i]})
	}
	if len(b.sel.Extra)+len(b.sel.Trailing) > 0 {
		rows = append(rows, row{kind: rowExtra})
	}
	return append(rows, row{kind: rowRun})
}

func (b *build) capturesText() bool { return false }

func (b *build) preview(m *Model) ([]string, []string, string) {
	argv, problems := builder.Build(b.cmd, b.sel)
	return argv, problems, builder.Danger(b.cmd, b.sel)
}

func (b *build) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	rows := b.rows()
	r := rows[b.cur]
	switch msg.String() {
	case "up", "k":
		if b.cur > 0 {
			b.cur--
		}
	case "down", "j":
		if b.cur < len(rows)-1 {
			b.cur++
		}
	case "left", "h", "esc", "backspace":
		m.pop()
	case "q":
		return m.quit()
	case " ":
		if r.kind == rowOption {
			b.toggle(m, r.opt)
		} else if r.kind == rowArg {
			b.editArg(m, r.arg)
		}
	case "enter", "right", "l":
		switch r.kind {
		case rowOption:
			if r.opt.TakesValue() {
				b.editValue(m, r.opt)
			} else {
				b.toggle(m, r.opt)
			}
		case rowArg:
			b.editArg(m, r.arg)
		case rowExtra:
			argv, _, _ := b.preview(m)
			m.push(editCommand(builder.Quote(argv), b.cmd))
		case rowRun:
			return m.run(b.preview(m))
		}
	case "x", "delete":
		switch r.kind {
		case rowOption:
			delete(b.sel.Values, r.opt.Flag)
			delete(b.sel.Flags, r.opt.Flag)
		case rowArg:
			delete(b.sel.Args, r.arg.Name)
		case rowExtra:
			b.sel.Extra, b.sel.Trailing = nil, nil
		}
	case "r":
		return m.run(b.preview(m))
	case "c":
		argv, _, _ := b.preview(m)
		m.copy(argv)
	case "e":
		argv, _, _ := b.preview(m)
		m.push(editCommand(builder.Quote(argv), b.cmd))
	}
	return nil
}

// editCommand lets the user type or edit a whole command line. On enter
// the text is parsed back into a command and its toggles, replacing the
// current builder (or opening one), so the result can be reviewed before
// running. prefer breaks ties between commands that fit equally well.
func editCommand(line string, prefer *spec.Command) *input {
	in := newInput("Edit the command. Enter applies it to the builder", line, func(m *Model, v string) tea.Cmd {
		argv, err := builder.Parse(v)
		if err != nil {
			m.setNotice("%v", err)
			return nil
		}
		c, sel := builder.Match(m.Specs, argv, prefer)
		m.pop() // the input
		if _, ok := m.top().(*build); ok {
			m.pop() // the builder being edited
		}
		m.push(newBuild(c, sel, nil))
		if n := len(sel.Extra) + len(sel.Trailing); n > 0 && c.ID != "custom" {
			m.setNotice("%d word(s) gituy doesn't know are kept as typed", n)
		}
		return nil
	})
	in.live = true
	return in
}

func (b *build) toggle(m *Model, o *spec.Option) {
	if o.TakesValue() && !b.sel.Flags[o.Flag] && b.sel.Values[o.Flag] == "" {
		b.editValue(m, o)
		return
	}
	b.sel.Toggle(b.cmd, o.Flag)
}

func (b *build) editValue(m *Model, o *spec.Option) {
	m.push(newInput(fmt.Sprintf("%s <%s>: %s", o.Flag, o.Value, o.Desc), b.sel.Values[o.Flag], func(m *Model, v string) tea.Cmd {
		b.sel.Values[o.Flag] = v
		if v != "" && !b.sel.Flags[o.Flag] {
			b.sel.Toggle(b.cmd, o.Flag)
		}
		if v == "" && b.sel.Flags[o.Flag] {
			b.sel.Toggle(b.cmd, o.Flag)
		}
		m.pop()
		return nil
	}))
}

func (b *build) editArg(m *Model, a *spec.Arg) {
	set := func(vals []string) {
		if len(vals) == 0 {
			delete(b.sel.Args, a.Name)
		} else {
			b.sel.Args[a.Name] = vals
		}
	}
	if a.Source == "text" {
		m.push(newInput(a.Name+": "+a.Desc, strings.Join(b.sel.Args[a.Name], " "), func(m *Model, v string) tea.Cmd {
			set(strings.Fields(v))
			m.pop()
			return nil
		}))
		return
	}
	choices := choicesFor(m, b, a)
	m.push(newPicker(a, choices, b.sel.Args[a.Name], func(vals []string) { set(vals) }))
}

func (b *build) view(m *Model, height int) string {
	var s strings.Builder
	title := stBold.Render("git "+b.cmd.Title()) + "  " + stMuted.Render(b.cmd.Summary)
	s.WriteString(title)
	s.WriteString("\n")
	used := 1
	if b.sug != nil && b.sug.Explain != "" {
		s.WriteString(stMuted.Render(indent(strings.TrimSpace(b.sug.Explain), "  ")))
		s.WriteString("\n")
		used += strings.Count(strings.TrimSpace(b.sug.Explain), "\n") + 1
	} else if b.cmd.Help != "" {
		s.WriteString(stMuted.Render(indent(strings.TrimSpace(b.cmd.Help), "  ")))
		s.WriteString("\n")
		used += strings.Count(strings.TrimSpace(b.cmd.Help), "\n") + 1
	}

	rows := b.rows()
	start, end := window(len(rows), b.cur, height-used)
	for i := start; i < end; i++ {
		s.WriteString(b.renderRow(rows[i], i == b.cur))
		if i < end-1 {
			s.WriteString("\n")
		}
	}
	return s.String()
}

func (b *build) renderRow(r row, selected bool) string {
	switch r.kind {
	case rowOption:
		o := r.opt
		on := b.sel.Flags[o.Flag]
		var box string
		switch {
		case o.Group != "" && on:
			box = "(•)"
		case o.Group != "":
			box = "( )"
		case on:
			box = "[x]"
		default:
			box = "[ ]"
		}
		if on {
			box = stGood.Render(box)
		}
		flag := o.Flag
		if o.TakesValue() {
			v := b.sel.Values[o.Flag]
			if v == "" {
				v = "<" + o.Value + ">"
			}
			flag += " " + v
		}
		flag = fmt.Sprintf("%-26s", flag)
		if selected {
			flag = stSelected.Render(flag)
		} else {
			flag = dangerStyle(o.Danger).Render(flag)
		}
		return cursor(selected) + box + " " + flag + " " + stMuted.Render(o.Desc)
	case rowArg:
		a := r.arg
		vals := b.sel.Args[a.Name]
		val := stMuted.Render("(none)")
		if len(vals) > 0 {
			val = stGood.Render(strings.Join(vals, " "))
		} else if a.Required {
			val = stWarn.Render("(required)")
		}
		name := fmt.Sprintf("%-26s", "<"+a.Name+"> ▸")
		if selected {
			name = stSelected.Render(name)
		}
		return cursor(selected) + "    " + name + " " + val + "  " + stMuted.Render(a.Desc)
	case rowExtra:
		words := append(append([]string{}, b.sel.Extra...), b.sel.Trailing...)
		name := fmt.Sprintf("%-26s", "as typed ▸")
		if selected {
			name = stSelected.Render(name)
		}
		return cursor(selected) + "    " + name + " " + stWarn.Render(builder.Quote(words)) +
			"  " + stMuted.Render("not recognised; passed through unchanged (x to remove)")
	default:
		label := "▶ Run"
		if selected {
			label = stSelected.Render(label)
		} else {
			label = stGood.Render(label)
		}
		return cursor(selected) + label
	}
}
