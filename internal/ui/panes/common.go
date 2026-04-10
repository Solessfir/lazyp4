package panes

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

var styleFilterBar = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))

func fuzzyMatch(pattern, s string) bool {
	if pattern == "" {
		return true
	}
	s = strings.ToLower(s)
	pattern = strings.ToLower(pattern)
	pi := 0
	for i := 0; i < len(s) && pi < len(pattern); i++ {
		if s[i] == pattern[pi] {
			pi++
		}
	}
	return pi == len(pattern)
}

// InjectTitle replaces the top border line of a rendered lipgloss box with
// a lazygit-style titled border: ╭───[N]─Name──────────╮
func InjectTitle(rendered, num, name string, paneWidth int, focused bool) string {
	return injectTitle(rendered, num, name, paneWidth, focused)
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

	color := lipgloss.Color("8")
	if focused {
		color = lipgloss.Color("12")
	}
	style := lipgloss.NewStyle().Foreground(color)

	label := " " + text + " "
	dashCount := measuredW - 2 - len([]rune(label))
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

	borderColor := lipgloss.Color("8")
	if focused {
		borderColor = lipgloss.Color("12")
	}
	borderStyle := lipgloss.NewStyle().Foreground(borderColor)
	activeStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	dimStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

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

	// Measure the visual width of the full label.
	plainLabel := prefix + firstName + separator + secondName
	innerW := paneWidth - 2
	dashCount := innerW - len([]rune(plainLabel))
	if dashCount < 0 {
		dashCount = 0
	}

	top := borderStyle.Render("╭"+prefix) +
		activeStyle.Render(activeLabel) +
		borderStyle.Render(separator) +
		dimStyle.Render(inactiveLabel) +
		borderStyle.Render(strings.Repeat("─", dashCount)+"╮")

	lines[0] = top
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

	color := lipgloss.Color("8")
	if focused {
		color = lipgloss.Color("12")
	}
	style := lipgloss.NewStyle().Foreground(color)

	var label string
	if num == "" {
		label = "───" + name
	} else {
		label = "───[" + num + "]─" + name
	}
	innerW := paneWidth - 2 // subtract ╭ and ╮
	dashCount := innerW - len([]rune(label))
	if dashCount < 0 {
		dashCount = 0
	}
	top := style.Render("╭" + label + strings.Repeat("─", dashCount) + "╮")

	lines[0] = top
	return strings.Join(lines, "\n")
}
