package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/hedge22112/gituy/internal/builder"
)

// input asks for a line of text.
type input struct {
	prompt   string
	ti       textinput.Model
	onSubmit func(m *Model, v string) tea.Cmd
	// live, if set, previews the typed text itself as the command.
	live bool
}

func newInput(prompt, value string, onSubmit func(m *Model, v string) tea.Cmd) *input {
	ti := textinput.New()
	ti.SetValue(value)
	ti.CursorEnd()
	ti.Focus()
	ti.Prompt = "> "
	ti.PromptStyle = stPrompt
	return &input{prompt: prompt, ti: ti, onSubmit: onSubmit}
}

func (in *input) capturesText() bool { return true }

func (in *input) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc":
		m.pop()
		return nil
	case "enter":
		return in.onSubmit(m, strings.TrimSpace(in.ti.Value()))
	}
	var cmd tea.Cmd
	in.ti, cmd = in.ti.Update(msg)
	return cmd
}

func (in *input) preview(m *Model) ([]string, []string, string) {
	if in.live {
		argv, err := builder.Parse(in.ti.Value())
		if err != nil {
			return nil, []string{err.Error()}, ""
		}
		c, sel := builder.Match(m.Specs, argv, nil)
		return argv, nil, builder.Danger(c, sel)
	}
	if len(m.stack) >= 2 {
		if b, ok := m.stack[len(m.stack)-2].(*build); ok {
			return b.preview(m)
		}
	}
	return nil, nil, ""
}

func (in *input) view(m *Model, height int) string {
	return stBold.Render(in.prompt) + "\n" + in.ti.View()
}

// confirm guards commands that can lose work.
type confirm struct {
	argv []string
}

func (c *confirm) capturesText() bool { return false }

func (c *confirm) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "y", "Y":
		return m.finish(c.argv)
	default:
		m.pop()
		m.setNotice("cancelled")
	}
	return nil
}

func (c *confirm) preview(m *Model) ([]string, []string, string) {
	return c.argv, nil, "high"
}

func (c *confirm) view(m *Model, height int) string {
	return stBad.Render("This command can permanently lose work:") + "\n\n  " +
		stBold.Render(builder.Quote(c.argv)) + "\n\n" +
		"Run it? " + stBold.Render("y") + " to run, any other key to cancel"
}

// activityLog shows background commands and commands run this session.
type activityLog struct{}

func (a *activityLog) capturesText() bool { return false }

func (a *activityLog) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	switch msg.String() {
	case "esc", "left", "q", "l", "backspace":
		m.pop()
	}
	return nil
}

func (a *activityLog) preview(m *Model) ([]string, []string, string) { return nil, nil, "" }

func (a *activityLog) view(m *Model, height int) string {
	var s strings.Builder
	s.WriteString(stBold.Render("Activity") + "\n")
	if m.Activity == nil || len(*m.Activity) == 0 {
		s.WriteString(stMuted.Render("  nothing yet"))
		return s.String()
	}
	acts := *m.Activity
	if len(acts) > height-1 {
		acts = acts[len(acts)-(height-1):]
	}
	for i := len(acts) - 1; i >= 0; i-- {
		e := acts[i]
		line := stMuted.Render(e.At.Format("15:04:05")) + "  " + builder.Quote(e.Argv)
		if len(e.Argv) == 0 {
			line = stMuted.Render(e.At.Format("15:04:05")) + "  " + stMuted.Render(e.Note)
		}
		switch {
		case len(e.Argv) == 0:
		case e.Err != nil:
			line += "  " + stBad.Render(firstLine(e.Err.Error()))
		case e.Note != "":
			line += "  " + stAccent.Render(e.Note)
		default:
			line += "  " + stGood.Render("ok")
		}
		s.WriteString(line)
		if i > 0 {
			s.WriteString("\n")
		}
	}
	return s.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
