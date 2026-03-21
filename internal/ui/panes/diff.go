package panes

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// DiffPane shows colorized p4 diff output for the selected file.
type DiffPane struct {
	viewport viewport.Model
	focused  bool
	width    int
	height   int
}

var (
	styleDiffAdd = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDiffDel = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDiffCtx = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleDiffHdr = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
)

// NewDiffPane creates an empty diff pane.
func NewDiffPane() *DiffPane {
	vp := viewport.New(80, 20)
	return &DiffPane{viewport: vp}
}

// SetSize updates dimensions.
func (p *DiffPane) SetSize(w, h int) {
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
func (p *DiffPane) SetFocused(f bool) {
	p.focused = f
}

// SetContent replaces the diff text.
func (p *DiffPane) SetContent(raw string) {
	p.viewport.SetContent(colorize(raw))
	p.viewport.GotoTop()
}

// Init satisfies tea.Model.
func (p *DiffPane) Init() tea.Cmd { return nil }

// Update handles scrolling.
func (p *DiffPane) Update(msg tea.Msg) tea.Cmd {
	var cmd tea.Cmd
	p.viewport, cmd = p.viewport.Update(msg)
	return cmd
}

// View renders the pane.
func (p *DiffPane) View() string {
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
	return injectTitle(rendered, "5", "Diff", p.width, p.focused)
}

// colorize applies ANSI colors to unified diff lines.
func colorize(raw string) string {
	lines := strings.Split(raw, "\n")
	var sb strings.Builder
	for i, line := range lines {
		var colored string
		switch {
		case strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			colored = styleDiffHdr.Render(line)
		case strings.HasPrefix(line, "@@"):
			colored = styleDiffCtx.Render(line)
		case strings.HasPrefix(line, "+"):
			colored = styleDiffAdd.Render(line)
		case strings.HasPrefix(line, "-"):
			colored = styleDiffDel.Render(line)
		default:
			colored = line
		}
		sb.WriteString(colored)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
