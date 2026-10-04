package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) refreshHelp() {
	a.helpViewport.Width = max(1, a.popupWidth(90)-2)
	height := max(1, min(a.height-3, a.height*3/4-2))
	if a.helpFilter.Value() != "" {
		height = min(height, max(1, a.height-5))
	}
	rows := len(strings.Split(a.helpContentFor(""), "\n"))
	a.helpViewport.Height = max(1, min(height, rows))
	a.helpViewport.SetContent(a.helpContent())
	a.helpViewport.SetYOffset(a.helpViewport.YOffset)
}

func (a *App) handleHelpKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch key.String() {
	case "esc":
		if a.helpFilter.Value() != "" {
			a.helpFilter.SetValue("")
			a.refreshHelp()
			a.helpViewport.GotoTop()
			return a, nil
		}
		a.showHelp = false
		a.helpFilter.Blur()
		return a, nil
	case "ctrl+c", "enter":
		a.showHelp = false
		a.helpFilter.Blur()
		return a, nil
	case "?":
		if a.helpFilter.Value() == "" {
			a.showHelp = false
			a.helpFilter.Blur()
			return a, nil
		}
	case "up":
		a.helpViewport.LineUp(1)
		return a, nil
	case "down":
		a.helpViewport.LineDown(1)
		return a, nil
	case "pgup":
		a.helpViewport.ViewUp()
		return a, nil
	case "pgdown":
		a.helpViewport.ViewDown()
		return a, nil
	case "home":
		a.helpViewport.GotoTop()
		return a, nil
	case "end":
		a.helpViewport.GotoBottom()
		return a, nil
	}
	previous := a.helpFilter.Value()
	var cmd tea.Cmd
	a.helpFilter, cmd = a.helpFilter.Update(key)
	if a.helpFilter.Value() != previous {
		a.refreshHelp()
		a.helpViewport.GotoTop()
	}
	return a, cmd
}
