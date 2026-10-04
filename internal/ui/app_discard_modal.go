package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/ui/panes"
)

func (a *App) handleDiscardKey(key tea.KeyMsg) (tea.Model, tea.Cmd) {
	c := a.confirm
	cancelRow := 1
	if len(c.localToDelete) > 0 {
		cancelRow = 2
	}
	choice := c.cursor
	switch key.String() {
	case "j", "down", "tab":
		c.cursor = min(cancelRow, c.cursor+1)
		return a, nil
	case "k", "up", "shift+tab":
		c.cursor = max(0, c.cursor-1)
		return a, nil
	case "esc", "ctrl+c", "n", "N", "h":
		choice = cancelRow
	case "x", "y", "Y", "l":
		choice = 0
	case "d":
		if len(c.localToDelete) == 0 {
			return a, nil
		}
		choice = 1
	case "enter":
	default:
		return a, nil
	}
	if choice == cancelRow {
		a.confirm = nil
		a.status = "Cancelled"
		return a, nil
	}
	if a.opRunning {
		a.status = a.opName + " in progress"
		return a, nil
	}
	a.confirm = nil
	if choice == 1 {
		return a, a.cmdRevertAndDeleteLocal(c.files, c.localToDelete, c.clID)
	}
	return a, a.cmdRevert(c.files, c.clID)
}

func (a *App) renderDiscardModal() string {
	c := a.confirm
	keys := []string{"x"}
	labels := []string{"Discard changes"}
	if len(c.localToDelete) > 0 {
		keys = append(keys, "d")
		labels = append(labels, "Discard and delete added files")
	}
	keys = append(keys, " ")
	labels = append(labels, "Cancel")
	cursor := max(0, min(c.cursor, len(labels)-1))
	width := max(3, min(a.width-2, max(80, min(4*a.width/7, 90))))
	innerW := width - 2
	rows := make([]string, len(labels))
	for i, label := range labels {
		style := lipgloss.NewStyle()
		if i == cursor {
			style = style.Background(lipgloss.Color("#292a2e")).Bold(true)
		}
		row := style.Foreground(lipgloss.Color("6")).Render(keys[i]) + style.Render(" "+label)
		row = ansi.Truncate(row, innerW, "…")
		rows[i] = row + style.Render(strings.Repeat(" ", max(0, innerW-lipgloss.Width(row))))
	}
	box := lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Width(innerW)
	menu := box.BorderForeground(lipgloss.Color("4")).Render(strings.Join(rows, "\n"))
	menu = panes.InjectTitle(menu, "", "Discard changes", width, true)
	menu = panes.InjectFooter(menu, fmt.Sprintf("%d of %d", cursor+1, len(labels)), true)

	scope := fmt.Sprintf("%d confirmed files", len(c.files))
	if len(c.files) == 1 {
		name := c.files[0]
		if a.client.Workspace != "" {
			name = strings.TrimPrefix(name, "//"+a.client.Workspace+"/")
		}
		if a.client.Root != "" && filepath.IsAbs(name) {
			if relative, err := filepath.Rel(a.client.Root, name); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) && !filepath.IsAbs(relative) {
				name = filepath.ToSlash(relative)
			}
		}
		scope = "'" + name + "'"
	} else if c.clID != "" {
		scope += " in CL " + c.clID
	}
	description := "Discard local changes in " + scope + "."
	if cursor == len(labels)-1 {
		description = "Keep local changes in " + scope + "."
	} else if len(c.localToDelete) > 0 {
		if cursor == 1 {
			description += " Delete local files opened for add after reverting them."
		} else {
			description += " Keep local files opened for add on disk."
		}
	}
	menuHeight := len(rows) + 2
	available := a.height - a.discardMenuY(menuHeight) - menuHeight - 4
	if available < 1 {
		return menu
	}
	bodyWidth := max(1, innerW-2)
	details := strings.Split(ansi.Hardwrap(ansi.Wrap(description, bodyWidth, ""), bodyWidth, true), "\n")
	if len(details) > available {
		details = details[:available]
		details[len(details)-1] = ansi.Truncate(details[len(details)-1], max(0, innerW-3), "") + "…"
	}
	tooltip := box.BorderForeground(lipgloss.Color("#44464f")).Padding(0, 1).Render(strings.Join(details, "\n"))
	return menu + "\n\n" + tooltip
}

func (a *App) discardMenuY(menuHeight int) int {
	// Reserve three rows for the description regardless of its wrapped height.
	return max(0, a.height/2-(menuHeight+4)/2)
}

func (a *App) placeDiscardModal(base string) string {
	popup := a.renderDiscardModal()
	menu := strings.SplitN(popup, "\n\n", 2)[0]
	x := max(0, (a.width-lipgloss.Width(menu))/2)
	return placeOverlay(x, a.discardMenuY(lipgloss.Height(menu)), popup, base)
}
