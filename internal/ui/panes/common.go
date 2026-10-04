package panes

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

const (
	inactiveBorderColor = lipgloss.Color("#44464f")
	selectedLineBgColor = lipgloss.Color("#292a2e")
)

var styleFilterBar = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

func cursorStyle(focused bool) lipgloss.Style {
	if focused {
		return styleCursor
	}
	return styleCursor.UnsetBackground()
}

// FuzzyMatch reports whether pattern is a case-insensitive subsequence of s.
func FuzzyMatch(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	patternRunes := []rune(strings.ToLower(pattern))
	pi := 0
	for _, r := range strings.ToLower(s) {
		if r == patternRunes[pi] {
			pi++
			if pi == len(patternRunes) {
				return true
			}
		}
	}
	return false
}

// InjectTitle replaces the top border line of a rendered lipgloss box with
// a lazygit-style titled border: ╭───[N]─Name──────────╮
func InjectTitle(rendered, num, name string, paneWidth int, focused bool) string {
	return injectTitle(rendered, num, name, paneWidth, focused)
}

func InjectFooter(rendered, text string, focused bool) string {
	return injectFooter(rendered, text, focused)
}

func injectFooter(rendered, text string, focused bool) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}
	last := lines[len(lines)-1]
	measuredW := lipgloss.Width(last)
	if measuredW < 3 {
		return rendered
	}

	color := inactiveBorderColor
	if focused {
		color = lipgloss.Color("4")
	}
	style := lipgloss.NewStyle().Foreground(color)

	label := ansi.Truncate(" "+text+" ", measuredW-2, "…")
	dashCount := measuredW - 2 - lipgloss.Width(label)
	if dashCount < 0 {
		dashCount = 0
	}
	lines[len(lines)-1] = style.Render("╰" + strings.Repeat("─", dashCount) + label + "╯")
	return strings.Join(lines, "\n")
}

// injectDualTitle renders a title with two tab-like names: the active one bright,
// the inactive one dimmed. Format: ╭───[N]─ ActiveName / InactiveName ──────╮
func injectDualTitle(rendered, num, firstName, secondName string, firstActive bool, paneWidth int, focused bool) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}

	measuredW := lipgloss.Width(lines[0])
	if measuredW > 0 {
		paneWidth = measuredW
	}

	borderColor := inactiveBorderColor
	if focused {
		borderColor = lipgloss.Color("4")
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
	dimStyle := lipgloss.NewStyle().Foreground(inactiveBorderColor)
	if focused {
		dimStyle = lipgloss.NewStyle()
	}

	prefix := "───[" + num + "]─ "
	separator := " / "
	var activeLabel, inactiveLabel string
	if firstActive {
		activeLabel = firstName
		inactiveLabel = secondName
	} else {
		activeLabel = secondName
		inactiveLabel = firstName
	}

	innerW := max(0, paneWidth-2)
	label := borderStyle.Render(prefix) +
		activeStyle.Render(activeLabel) +
		borderStyle.Render(separator) +
		dimStyle.Render(inactiveLabel)
	label = ansi.Truncate(label, innerW, "…")
	lines[0] = borderStyle.Render("╭") + label + borderStyle.Render(strings.Repeat("─", max(0, innerW-lipgloss.Width(label)))+"╮")
	return strings.Join(lines, "\n")
}

func injectTitle(rendered, num, name string, paneWidth int, focused bool) string {
	lines := strings.Split(rendered, "\n")
	if len(lines) == 0 {
		return rendered
	}

	// Measure the actual rendered top border line so the replacement is
	// always exactly the right width, regardless of what the caller passes.
	measuredW := lipgloss.Width(lines[0])
	if measuredW > 0 {
		paneWidth = measuredW
	}

	color := inactiveBorderColor
	if focused {
		color = lipgloss.Color("4")
	}
	style := lipgloss.NewStyle().Foreground(color)

	var label string
	if num == "" {
		label = "───" + name
	} else {
		label = "───[" + num + "]─" + name
	}
	innerW := max(0, paneWidth-2)
	label = ansi.Truncate(label, innerW, "…")
	dashCount := max(0, innerW-lipgloss.Width(label))
	top := style.Render("╭" + label + strings.Repeat("─", dashCount) + "╮")

	lines[0] = top
	return strings.Join(lines, "\n")
}
