package panes

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// CmdLogHeight is the fixed outer height of the log pane.
const CmdLogHeight = 6 // top border + 4 content lines + bottom border

const cmdLogMaxEntries = 100

var (
	styleCmdLogCmd = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))
	styleCmdLogOut = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// logEntry is one command result with optional detail lines (e.g. file paths).
type logEntry struct {
	lines []string
}

// CmdLogPane shows a scrollable log of p4 commands and their results.
type CmdLogPane struct {
	entries  []logEntry
	viewport viewport.Model
	focused  bool
	width    int
}

func NewCmdLogPane() *CmdLogPane {
	vp := viewport.New(80, CmdLogHeight-2)
	return &CmdLogPane{viewport: vp}
}

func (p *CmdLogPane) SetWidth(w int) {
	p.width = w
	p.viewport.Width = w - 2
	p.rebuildContent()
}

func (p *CmdLogPane) SetFocused(f bool) { p.focused = f }

// Add appends a new entry. Optional detail strings are shown indented below the summary line.
func (p *CmdLogPane) Add(cmd, result string, details ...string) {
	summary := styleCmdLogCmd.Render(cmd)
	if result != "" {
		summary += styleCmdLogOut.Render(" → " + result)
	}
	entry := logEntry{lines: []string{summary}}
	for _, d := range details {
		entry.lines = append(entry.lines, styleCmdLogOut.Render("  "+d))
	}
	p.entries = append(p.entries, entry)
	if len(p.entries) > cmdLogMaxEntries {
		p.entries = p.entries[len(p.entries)-cmdLogMaxEntries:]
	}
	p.rebuildContent()
	if !p.focused {
		p.viewport.GotoBottom()
	}
}

func (p *CmdLogPane) rebuildContent() {
	if len(p.entries) == 0 {
		p.viewport.SetContent(styleCmdLogOut.Render("no commands yet"))
		return
	}
	var all []string
	for _, e := range p.entries {
		all = append(all, e.lines...)
	}
	p.viewport.SetContent(strings.Join(all, "\n"))
}

func (p *CmdLogPane) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return cmd
}

func (p *CmdLogPane) View() string {
	border := styleBlurBorder
	if p.focused {
		border = styleFocusBorder
	}
	innerW := p.width - 2
	if innerW < 1 {
		innerW = 1
	}
	rendered := border.Width(innerW).Height(CmdLogHeight - 2).Render(p.viewport.View())
	return injectTitle(rendered, "6", "Log", p.width, p.focused)
}
