package ui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/rules"
	"github.com/hedge22112/gituy/internal/spec"
)

// home lists suggestions for the current state, then every command.
type home struct {
	cursorID  string // stable across refreshes
	filter    string
	filtering bool
}

type homeItem struct {
	id     string
	header string // non-empty for section headers (not selectable)
	sug    *rules.Suggestion
	cmd    *spec.Command
}

func (h *home) items(m *Model) []homeItem {
	f := strings.ToLower(h.filter)
	match := func(s ...string) bool {
		if f == "" {
			return true
		}
		for _, x := range s {
			if strings.Contains(strings.ToLower(x), f) {
				return true
			}
		}
		return false
	}

	var items []homeItem
	var sugs []homeItem
	for i := range m.suggestions {
		s := &m.suggestions[i]
		if match(s.Title, s.Command.Title()) {
			sugs = append(sugs, homeItem{id: "s:" + s.ID, sug: s})
		}
	}
	if limit := m.Cfg.MaxSuggestions; limit > 0 && len(sugs) > limit && f == "" {
		sugs = sugs[:limit]
	}
	if len(sugs) > 0 {
		items = append(items, homeItem{header: "Suggested"})
		items = append(items, sugs...)
	}
	var cmds []homeItem
	for _, c := range m.Specs.All() {
		if match(c.Title(), c.Summary, c.ID) {
			cmds = append(cmds, homeItem{id: "c:" + c.ID, cmd: c})
		}
	}
	if len(cmds) > 0 {
		items = append(items, homeItem{header: "All commands"})
		items = append(items, cmds...)
	}
	return items
}

// index returns the cursor position, falling back to the first
// selectable item if the remembered one disappeared.
func (h *home) index(items []homeItem) int {
	first := -1
	for i, it := range items {
		if it.header != "" {
			continue
		}
		if first < 0 {
			first = i
		}
		if it.id == h.cursorID {
			return i
		}
	}
	return first
}

func (h *home) move(items []homeItem, delta int) {
	i := h.index(items)
	for j := i + delta; j >= 0 && j < len(items); j += delta {
		if items[j].header == "" {
			h.cursorID = items[j].id
			return
		}
	}
}

func (h *home) selected(m *Model) *homeItem {
	items := h.items(m)
	i := h.index(items)
	if i < 0 {
		return nil
	}
	return &items[i]
}

func (it *homeItem) command() (*spec.Command, builder.Selection) {
	if it.sug != nil {
		return it.sug.Command, it.sug.Selection
	}
	return it.cmd, builder.NewSelection(it.cmd)
}

func (h *home) capturesText() bool { return h.filtering }

func (h *home) update(m *Model, msg tea.KeyMsg) tea.Cmd {
	items := h.items(m)
	key := msg.String()

	if h.filtering {
		switch key {
		case "esc":
			h.filtering, h.filter = false, ""
		case "enter":
			h.filtering = false
		case "backspace":
			if h.filter != "" {
				h.filter = h.filter[:len(h.filter)-1]
			}
		case "up", "down":
			// fall through to navigation below
		default:
			if msg.Type == tea.KeyRunes || msg.Type == tea.KeySpace {
				h.filter += string(msg.Runes)
			}
			return nil
		}
		if key != "up" && key != "down" {
			return nil
		}
	}

	switch key {
	case "up", "k":
		h.move(items, -1)
	case "down", "j":
		h.move(items, 1)
	case "/":
		h.filtering = true
	case "esc":
		h.filter = ""
	case "q":
		return m.quit()
	case "f":
		if m.Sup != nil {
			m.Sup.FetchNow()
			m.setNotice("fetching…")
		}
	case "l":
		m.push(&activityLog{})
	case "e", ":":
		m.push(editCommand("git ", nil))
	case "enter", "right":
		if it := h.selected(m); it != nil {
			c, sel := it.command()
			m.push(newBuild(c, sel, it.sug))
		}
	case "r":
		if it := h.selected(m); it != nil {
			c, sel := it.command()
			argv, problems := builder.Build(c, sel)
			if len(problems) > 0 {
				// open the builder so the missing pieces can be filled in
				m.push(newBuild(c, sel, it.sug))
				return nil
			}
			return m.run(argv, nil, builder.Danger(c, sel))
		}
	case "c":
		argv, _, _ := h.preview(m)
		m.copy(argv)
	}
	return nil
}

func (h *home) preview(m *Model) ([]string, []string, string) {
	it := h.selected(m)
	if it == nil {
		return nil, nil, ""
	}
	c, sel := it.command()
	argv, problems := builder.Build(c, sel)
	return argv, problems, builder.Danger(c, sel)
}

func (h *home) view(m *Model, height int) string {
	items := h.items(m)
	cur := h.index(items)
	var b strings.Builder
	lines := 0
	if h.filtering || h.filter != "" {
		b.WriteString(stPrompt.Render("/") + h.filter)
		if h.filtering {
			b.WriteString(stAccent.Render("▏"))
		}
		b.WriteString("\n")
		lines++
	}

	// explanation for the highlighted suggestion
	var explain string
	if cur >= 0 && items[cur].sug != nil && items[cur].sug.Explain != "" {
		explain = strings.TrimSpace(items[cur].sug.Explain)
	} else if cur >= 0 && items[cur].cmd != nil && items[cur].cmd.Help != "" {
		explain = strings.TrimSpace(items[cur].cmd.Help)
	}
	explainLines := 0
	if explain != "" {
		explainLines = strings.Count(explain, "\n") + 2
	}

	start, end := window(len(items), cur, height-lines-explainLines)
	for i := start; i < end; i++ {
		it := items[i]
		if it.header != "" {
			b.WriteString(stHeader.Render(" " + it.header))
		} else {
			b.WriteString(h.renderItem(it, i == cur))
		}
		if i < end-1 {
			b.WriteString("\n")
		}
	}
	if len(items) == 0 {
		b.WriteString(stMuted.Render("  nothing matches"))
	}
	if explain != "" {
		b.WriteString("\n\n")
		b.WriteString(stMuted.Render(indent(explain, "  ")))
	}
	return b.String()
}

func (h *home) renderItem(it homeItem, selected bool) string {
	if it.sug != nil {
		title := it.sug.Title
		if selected {
			title = stSelected.Render(title)
		}
		danger := builder.Danger(it.sug.Command, it.sug.Selection)
		if danger != "" {
			title += " " + dangerStyle(danger).Render("●")
		}
		return cursor(selected) + title
	}
	name := fmt.Sprintf("%-26s", it.cmd.Title())
	if selected {
		name = stSelected.Render(name)
	} else {
		name = dangerStyle(it.cmd.Danger).Render(name)
	}
	return cursor(selected) + name + " " + stMuted.Render(it.cmd.Summary)
}

func indent(s, prefix string) string {
	return prefix + strings.ReplaceAll(s, "\n", "\n"+prefix)
}
