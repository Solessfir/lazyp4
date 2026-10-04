package panes

import (
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// DiffPane shows colorized p4 diff output for the selected file.
type DiffPane struct {
	viewport   viewport.Model
	raw        string // stored so we can rewrap on resize
	focused    bool
	diffActive bool // true = Diff is the visible bottom-right pane
	width      int
	height     int
}

var (
	styleDiffAdd  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleDiffDel  = lipgloss.NewStyle().Foreground(lipgloss.Color("1"))
	styleDiffCtx  = lipgloss.NewStyle()
	styleDiffHdr  = lipgloss.NewStyle().Bold(true)
	styleDiffHunk = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
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
	if p.raw != "" {
		p.rerender()
	}
}

// SetFocused toggles focus highlight.
func (p *DiffPane) SetFocused(f bool) {
	p.focused = f
}

// SetDiffActive sets whether Diff (true) or History (false) is the visible pane.
func (p *DiffPane) SetDiffActive(active bool) {
	p.diffActive = active
}

// SetContent replaces the diff text.
func (p *DiffPane) SetContent(raw string) {
	p.raw = raw
	p.rerender()
	p.viewport.GotoTop()
}

func (p *DiffPane) rerender() {
	p.viewport.SetContent(colorize(p.raw, p.viewport.Width))
	p.viewport.SetYOffset(p.viewport.YOffset)
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
	return injectDualTitle(rendered, "5", "Diff", "History", p.diffActive, p.width, p.focused)
}

// colorize wraps unified diff lines without losing their contents or colors.
func colorize(raw string, maxWidth int) string {
	lines := strings.Split(raw, "\n")
	var sb strings.Builder
	for i, line := range lines {
		line = strings.TrimSuffix(line, "\r")
		// Expand tabs before wrapping so indentation has a stable cell width.
		if strings.ContainsRune(line, '\t') {
			parts := strings.Split(line, "\t")
			line = parts[0]
			for _, part := range parts[1:] {
				line += strings.Repeat(" ", 8-ansi.StringWidth(line)%8) + part
			}
		}
		wrapped := ansi.Hardwrap(line, maxWidth, true)
		var colored string
		switch {
		case strings.HasPrefix(line, "==== ") || strings.HasPrefix(line, "+++ ") || strings.HasPrefix(line, "--- "):
			colored = styleDiffHdr.Render(wrapped)
		case strings.HasPrefix(line, "@@"):
			if end := strings.Index(line[2:], "@@"); end >= 0 {
				end += 4
				colored = ansi.Hardwrap(styleDiffHunk.Render(line[:end])+styleDiffCtx.Render(line[end:]), maxWidth, true)
			} else {
				colored = styleDiffHunk.Render(wrapped)
			}
		case strings.HasPrefix(line, "+"):
			colored = styleDiffAdd.Render(wrapped)
		case strings.HasPrefix(line, "-"):
			colored = styleDiffDel.Render(wrapped)
		default:
			colored = wrapped
		}
		sb.WriteString(colored)
		if i < len(lines)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
