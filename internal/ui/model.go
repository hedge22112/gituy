// Package ui is gituy's terminal interface: a stack of screens with a
// state bar on top and the live git command preview at the bottom.
package ui

import (
	"fmt"
	"os"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/muesli/termenv"

	"github.com/hedge22112/gituy/internal/bg"
	"github.com/hedge22112/gituy/internal/builder"
	"github.com/hedge22112/gituy/internal/config"
	"github.com/hedge22112/gituy/internal/git"
	"github.com/hedge22112/gituy/internal/rules"
	"github.com/hedge22112/gituy/internal/spec"
)

// Action is what the user chose when the UI exits.
type Action int

const (
	ActionQuit Action = iota
	ActionRun
)

// Result is returned to main when the UI exits.
type Result struct {
	Action Action
	Argv   []string
}

// Activity is one entry in the activity log.
type Activity struct {
	At   time.Time
	Argv []string
	Note string
	Err  error
}

// screen is one level of the navigation stack.
type screen interface {
	update(m *Model, msg tea.KeyMsg) tea.Cmd
	view(m *Model, height int) string
	// preview returns the command this screen would run, if any.
	preview(m *Model) (argv []string, problems []string, danger string)
	// capturesText reports whether letter keys are being typed into an input.
	capturesText() bool
}

// Model is the root Bubble Tea model.
type Model struct {
	Specs  *spec.Set
	Rules  *rules.Engine
	Cfg    config.Config
	Git    git.Runner // foreground runner (pickers)
	Sup    *bg.Supervisor
	Result Result
	// Activity is shared with main so it survives UI restarts.
	Activity *[]Activity

	state       git.State
	stateErr    error
	loaded      bool
	suggestions []rules.Suggestion
	fetching    bool
	lastFetch   time.Time
	fetchErr    error
	notice      string
	noticeAt    time.Time

	width, height int
	stack         []screen
	done          bool // quitting; ignore further keys
}

// New creates the root model.
func New(m Model) *Model {
	m.stack = []screen{&home{}}
	if m.Sup != nil {
		m.lastFetch = m.Sup.LastFetched()
	}
	return &m
}

func (m *Model) Init() tea.Cmd { return nil }

func (m *Model) top() screen { return m.stack[len(m.stack)-1] }

func (m *Model) push(s screen) { m.stack = append(m.stack, s) }

func (m *Model) pop() {
	if len(m.stack) > 1 {
		m.stack = m.stack[:len(m.stack)-1]
	}
}

func (m *Model) setNotice(format string, args ...any) {
	m.notice = fmt.Sprintf(format, args...)
	m.noticeAt = time.Now()
}

func (m *Model) log(a Activity) {
	if m.Activity != nil {
		*m.Activity = append(*m.Activity, a)
	}
}

// run finishes the UI with a command to execute, confirming dangerous ones.
func (m *Model) run(argv []string, problems []string, danger string) tea.Cmd {
	if len(problems) > 0 {
		m.setNotice("can't run yet: %s", strings.Join(problems, ", "))
		return nil
	}
	if danger == spec.DangerHigh {
		m.push(&confirm{argv: argv})
		return nil
	}
	return m.finish(argv)
}

func (m *Model) finish(argv []string) tea.Cmd {
	m.Result = Result{Action: ActionRun, Argv: argv}
	m.done = true
	return tea.Quit
}

func (m *Model) quit() tea.Cmd {
	m.Result = Result{Action: ActionQuit}
	m.done = true
	return tea.Quit
}

func (m *Model) copy(argv []string) {
	if len(argv) == 0 {
		return
	}
	termenv.NewOutput(os.Stderr).Copy(builder.Quote(argv))
	m.setNotice("copied: %s", builder.Quote(argv))
}

func (m *Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
	case bg.StateMsg:
		m.stateErr = msg.Err
		if msg.Err == nil {
			m.state = msg.State
			m.loaded = true
			m.suggestions = m.Rules.Suggest(msg.State)
		}
	case bg.WatchMsg:
		switch msg.Mode {
		case bg.WatchFull:
			m.log(Activity{At: time.Now(), Note: fmt.Sprintf("watching %d directories for changes", msg.Dirs)})
		case bg.WatchGitOnly:
			m.log(Activity{At: time.Now(), Note: "watching .git only, polling files: " + msg.Reason})
			m.setNotice("polling for file changes: %s", msg.Reason)
		default:
			m.log(Activity{At: time.Now(), Note: "file watching off, polling: " + msg.Reason})
			m.setNotice("file watching off: %s", msg.Reason)
		}
	case bg.FetchStartMsg:
		m.fetching = true
	case bg.FetchMsg:
		m.fetching = false
		m.lastFetch = msg.At
		m.fetchErr = msg.Err
		m.log(Activity{At: msg.At, Argv: msg.Args, Err: msg.Err, Note: describeChanges(msg.Changes)})
		if len(msg.Changes) > 0 {
			m.setNotice("fetched: %s", describeChanges(msg.Changes))
		}
	case tea.KeyMsg:
		if m.done {
			return m, nil
		}
		if msg.String() == "ctrl+c" {
			return m, m.quit()
		}
		// Fast typing can arrive as one multi-rune key ("/reset"). Feed it
		// one key at a time so "/" opens the filter instead of the letters
		// being read as shortcuts. Pastes stay whole.
		if msg.Type == tea.KeyRunes && len(msg.Runes) > 1 && !msg.Paste {
			var cmds []tea.Cmd
			for _, r := range msg.Runes {
				cmd := m.top().update(m, tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{r}, Alt: msg.Alt})
				cmds = append(cmds, cmd)
				if m.done {
					break
				}
			}
			return m, tea.Batch(cmds...)
		}
		return m, m.top().update(m, msg)
	}
	return m, nil
}

func describeChanges(changes []git.RefChange) string {
	var parts []string
	for _, c := range changes {
		switch {
		case c.Old == "":
			parts = append(parts, c.Ref+" (new)")
		case c.New == "":
			parts = append(parts, c.Ref+" (deleted)")
		default:
			parts = append(parts, c.Ref+" (updated)")
		}
	}
	if len(parts) > 4 {
		parts = append(parts[:4], fmt.Sprintf("+%d more", len(parts)-4))
	}
	return strings.Join(parts, ", ")
}

func (m *Model) View() string {
	var b strings.Builder
	b.WriteString(m.stateBar())
	b.WriteString("\n")
	b.WriteString(m.rule())
	b.WriteString("\n")

	footer := m.footer()
	bodyHeight := 20
	if m.height > 0 {
		bodyHeight = m.height - 3 - lipglossHeight(footer)
	}
	b.WriteString(m.top().view(m, max(bodyHeight, 3)))
	b.WriteString("\n")
	b.WriteString(footer)
	return b.String()
}

func lipglossHeight(s string) int { return strings.Count(s, "\n") + 1 }

func (m *Model) rule() string {
	w := m.width
	if w <= 0 {
		w = 60
	}
	return stRule.Render(strings.Repeat("─", w))
}

func (m *Model) stateBar() string {
	if m.stateErr != nil {
		return stBad.Render("⚠ " + m.stateErr.Error())
	}
	if !m.loaded {
		return stMuted.Render("reading repository…")
	}
	st := m.state
	var parts []string
	if st.Op != git.OpNone {
		parts = append(parts, stBanner.Render(strings.ToUpper(string(st.Op))+" IN PROGRESS"))
	}
	switch {
	case st.Detached:
		parts = append(parts, stWarn.Render("detached HEAD"))
	default:
		parts = append(parts, stBold.Render(st.Branch))
	}
	switch {
	case st.Upstream == "" && !st.Detached:
		parts = append(parts, stMuted.Render("no upstream"))
	case st.Upstream != "":
		ab := fmt.Sprintf("↑%d ↓%d", st.Ahead, st.Behind)
		if st.Ahead == 0 && st.Behind == 0 {
			parts = append(parts, stGood.Render("in sync"))
		} else {
			parts = append(parts, stAccent.Render(ab))
		}
	}
	if n := len(st.Conflicted); n > 0 {
		parts = append(parts, stBad.Render(fmt.Sprintf("%d conflicted", n)))
	}
	if n := len(st.Staged); n > 0 {
		parts = append(parts, stGood.Render(fmt.Sprintf("%d staged", n)))
	}
	if n := len(st.Unstaged); n > 0 {
		parts = append(parts, stWarn.Render(fmt.Sprintf("%d modified", n)))
	}
	if n := len(st.Untracked); n > 0 {
		parts = append(parts, stMuted.Render(fmt.Sprintf("%d untracked", n)))
	}
	if st.Clean() {
		parts = append(parts, stGood.Render("clean"))
	}
	if st.StashCount > 0 {
		parts = append(parts, stMuted.Render(fmt.Sprintf("%d stashed", st.StashCount)))
	}
	switch {
	case m.Cfg.Fetch.Enabled && st.Op != git.OpNone:
		parts = append(parts, stMuted.Render("fetch paused during "+string(st.Op)))
	case m.fetching:
		parts = append(parts, stAccent.Render("⟳ fetching…"))
	case m.fetchErr != nil:
		parts = append(parts, stBad.Render("⚠ fetch failed (f to retry, l for details)"))
	case !m.lastFetch.IsZero():
		parts = append(parts, stMuted.Render("fetched "+ago(m.lastFetch)))
	}
	return strings.Join(parts, stMuted.Render(" · "))
}

func ago(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d.Minutes()))
	default:
		return fmt.Sprintf("%dh ago", int(d.Hours()))
	}
}

// footer is the always-visible command preview plus key hints.
func (m *Model) footer() string {
	var b strings.Builder
	b.WriteString(m.rule())
	b.WriteString("\n")
	argv, problems, danger := m.top().preview(m)
	if len(argv) == 0 {
		b.WriteString(stMuted.Render("$ "))
	} else {
		b.WriteString(stPrompt.Render("$ "))
		b.WriteString(dangerStyle(danger).Bold(true).Render(builder.Quote(argv)))
		if danger == spec.DangerHigh {
			b.WriteString(stBad.Render("  ⚠ can lose work"))
		}
	}
	b.WriteString("\n")
	switch {
	case len(problems) > 0:
		b.WriteString(stWarn.Render("needs: " + strings.Join(problems, ", ")))
	case m.notice != "" && time.Since(m.noticeAt) < 8*time.Second:
		b.WriteString(stAccent.Render(m.notice))
	default:
		b.WriteString(stMuted.Render(m.hints()))
	}
	return b.String()
}

func (m *Model) hints() string {
	switch m.top().(type) {
	case *home:
		return "↑↓ move · → open · r run · c copy · / filter · f fetch · l log · q quit"
	case *build:
		return "↑↓ move · space toggle · → edit · x clear · r run · c copy · e edit command · ← back"
	case *picker:
		return "type to filter · ↑↓ move · space select · enter confirm · esc back"
	case *input:
		return "enter confirm · esc cancel"
	}
	return "esc back"
}
