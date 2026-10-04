package ui

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

func (a *App) helpPosition() (position, total int) {
	line := 0
	for i, row := range a.helpRowsFor(a.helpFilter.Value()) {
		if row.section {
			if i > 0 {
				line++
			}
		} else {
			total++
			if position == 0 && line >= a.helpViewport.YOffset {
				position = total
			}
		}
		line++
	}
	return position, total
}

func (a *App) refreshHelp() {
	a.helpViewport.Width = max(1, a.popupWidth(90)-2)
	height := max(1, min(a.height-3, a.height*3/4-2))
	rows := len(strings.Split(a.helpContentFor(""), "\n"))
	height = min(height, rows)
	if a.helpFilter.Value() != "" {
		height = max(1, min(height-2, a.height-5))
	}
	a.helpViewport.Height = max(1, height)
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
		a.updateFocus()
		a.helpFilter.Blur()
		return a, nil
	case "ctrl+c", "enter":
		a.showHelp = false
		a.updateFocus()
		a.helpFilter.Blur()
		return a, nil
	case "?":
		if a.helpFilter.Value() == "" {
			a.showHelp = false
			a.updateFocus()
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
