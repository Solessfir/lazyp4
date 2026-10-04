package ui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/ui/panes"
)

func (a *App) popupWidth(maxWidth int) int {
	return max(3, min(a.width-2, max(80, min(4*a.width/7, maxWidth))))
}

func (a *App) renderModalBox(title, body string) string {
	width := a.popupWidth(80)
	bodyWidth := max(1, width-4)
	body = ansi.Hardwrap(ansi.Wrap(body, bodyWidth, ""), bodyWidth, true)
	lines := strings.Split(body, "\n")
	maxLines := max(1, a.height-4)
	if len(lines) > maxLines {
		lines = lines[:maxLines]
		lines[maxLines-1] = ansi.Truncate(lines[maxLines-1], bodyWidth-1, "") + "…"
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("4")).Padding(0, 1).Width(width - 2)
	rendered := box.Render(strings.Join(lines, "\n"))
	return panes.InjectTitle(rendered, "", title, width, true)
}

func (a *App) modalInputView(input textinput.Model) string {
	input.Prompt = ""
	input.Width = max(1, a.popupWidth(80)-5)
	input.TextStyle = lipgloss.NewStyle()
	input.PlaceholderStyle = lipgloss.NewStyle().Faint(true)
	// Force the resized copy's scrolling to recalculate even when the caret stays in its old window.
	position := input.Position()
	input.CursorEnd()
	input.SetCursor(position)
	return input.View()
}

func (a *App) modalHotkeys() string {
	switch {
	case a.authModal != nil:
		hint := "Login: <enter>"
		if a.client.StorePassword {
			hint += " | Login and remember: <ctrl+enter>"
		}
		return hint + " | Close/Cancel: <esc>"
	case a.integrateModal != nil:
		if !a.integrateModal.isClassic {
			return "Pull: m | Promote: c | Close/Cancel: <esc>"
		}
		if a.integrateModal.step == 0 {
			return "Next: <enter> | Close/Cancel: <esc>"
		}
		return "Integrate: <enter> | Close/Cancel: <esc>"
	case a.streamSwitch != nil:
		return "Switching workspace"
	case a.checkout != nil:
		if a.checkout.hasFiles {
			return "Shelve and sync: <enter> | Close/Cancel: <esc>"
		}
		return "Sync: <enter> | Close/Cancel: <esc>"
	case a.confirm != nil:
		return "Execute: <enter> | Close/Cancel: <esc>"
	case a.shelveModal != nil:
		if !a.shelveModal.noRevert {
			return "Shelve and revert: <enter> | Close/Cancel: <esc>"
		}
		return "Shelve: <enter> | Close/Cancel: <esc>"
	case a.moveModal != nil:
		return "Move: <enter> | Close/Cancel: <esc>"
	case a.modal != nil:
		return "Submit: <enter> | Close/Cancel: <esc>"
	case a.showHelp:
		if a.helpFilter.Value() != "" {
			return "Search: type (@keys) | Clear: <esc> | Close: <ctrl+c>"
		}
		return "Search: type (@keys) | Scroll: <up>/<down> | Close: <esc>"
	}
	return ""
}
