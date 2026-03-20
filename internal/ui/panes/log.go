package panes

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

// LogPane displays p4 filelog output.
type LogPane struct {
	viewport  viewport.Model
	focused   bool
	width     int
	height    int
	titleNum  string // section number shown in header, e.g. "2"
	titleName string // section name shown in header, e.g. "History"
}

var (
	styleLogCL   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleLogMeta = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleLogDesc = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

// NewLogPane creates an empty log pane with the given section number and name.
func NewLogPane(num, name string) *LogPane {
	vp := viewport.New(80, 20)
	return &LogPane{viewport: vp, titleNum: num, titleName: name}
}

// SetSize updates dimensions.
func (p *LogPane) SetSize(w, h int) {
	p.width = w
	p.height = h
	innerW := w - 2
	innerH := h - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}
	p.viewport.Width = innerW
	p.viewport.Height = innerH
}

// SetFocused toggles focus highlight.
func (p *LogPane) SetFocused(f bool) {
	p.focused = f
}

// SetEntries replaces the displayed log entries.
func (p *LogPane) SetEntries(entries []p4.FilelogEntry) {
	p.viewport.SetContent(renderEntries(entries))
	p.viewport.GotoTop()
}

// Init satisfies tea.Model.
func (p *LogPane) Init() tea.Cmd { return nil }

// Update handles scrolling.
func (p *LogPane) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return cmd
}

// View renders the pane.
func (p *LogPane) View() string {
	border := styleBlurBorder
	if p.focused {
		border = styleFocusBorder
	}
	innerW := p.width - 2
	innerH := p.height - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}
	rendered := border.Width(innerW).Height(innerH).Render(p.viewport.View())
	return injectTitle(rendered, p.titleNum, p.titleName, p.width, p.focused)
}

func renderEntries(entries []p4.FilelogEntry) string {
	if len(entries) == 0 {
		return styleLogMeta.Render("No history")
	}
	var sb strings.Builder
	for i, e := range entries {
		header := fmt.Sprintf("CL %s  rev #%d  %s  %s", e.Change, e.Rev, e.Author, e.Date)
		sb.WriteString(styleLogCL.Render(header))
		sb.WriteByte('\n')
		action := fmt.Sprintf("  action: %s", e.Action)
		sb.WriteString(styleLogMeta.Render(action))
		sb.WriteByte('\n')
		desc := "  " + strings.ReplaceAll(strings.TrimSpace(e.Description), "\n", "\n  ")
		sb.WriteString(styleLogDesc.Render(desc))
		if i < len(entries)-1 {
			sb.WriteString("\n\n")
		}
	}
	return sb.String()
}
