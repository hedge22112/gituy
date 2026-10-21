package ui

import (
	"github.com/charmbracelet/lipgloss"

	"github.com/hedge22112/gituy/internal/spec"
)

var (
	colAccent = lipgloss.AdaptiveColor{Light: "#5A3FD6", Dark: "#A893FF"}
	colMuted  = lipgloss.AdaptiveColor{Light: "#6B6B6B", Dark: "#8A8A8A"}
	colGood   = lipgloss.AdaptiveColor{Light: "#1A7F37", Dark: "#56D364"}
	colWarn   = lipgloss.AdaptiveColor{Light: "#9A6700", Dark: "#E3B341"}
	colBad    = lipgloss.AdaptiveColor{Light: "#CF222E", Dark: "#FF7B72"}

	stBold     = lipgloss.NewStyle().Bold(true)
	stMuted    = lipgloss.NewStyle().Foreground(colMuted)
	stAccent   = lipgloss.NewStyle().Foreground(colAccent)
	stGood     = lipgloss.NewStyle().Foreground(colGood)
	stWarn     = lipgloss.NewStyle().Foreground(colWarn)
	stBad      = lipgloss.NewStyle().Foreground(colBad)
	stSelected = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
	stHeader   = lipgloss.NewStyle().Foreground(colMuted).Bold(true)
	stBanner   = lipgloss.NewStyle().Bold(true).Foreground(lipgloss.Color("#FFFFFF")).Background(colBad).Padding(0, 1)
	stRule     = lipgloss.NewStyle().Foreground(colMuted)
	stPrompt   = lipgloss.NewStyle().Foreground(colAccent).Bold(true)
)

func dangerStyle(level string) lipgloss.Style {
	switch level {
	case spec.DangerHigh:
		return stBad
	case spec.DangerLow, spec.DangerConfirm:
		return stWarn
	}
	return lipgloss.NewStyle()
}

func cursor(selected bool) string {
	if selected {
		return stSelected.Render("❯ ")
	}
	return "  "
}

// window returns the [start, end) range of n rows to show so the cursor
// stays visible within height rows.
func window(n, cur, height int) (int, int) {
	if height <= 0 || n <= height {
		return 0, n
	}
	start := cur - height/2
	if start < 0 {
		start = 0
	}
	if start+height > n {
		start = n - height
	}
	return start, start + height
}
