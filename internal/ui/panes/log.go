package panes

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

// LogPane displays p4 filelog output with cursor-based entry selection.
type LogPane struct {
	viewport     viewport.Model
	entries      []p4.FilelogEntry
	cursor       int
	entryOffsets []int // viewport line where each entry starts
	focused      bool
	width        int
	height       int
	innerW       int    // width - 2, used for word-wrap
	titleNum     string // section number shown in header, e.g. "5"
	titleName    string // section name shown in header, e.g. "History"
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
	p.innerW = innerW
	p.viewport.Width = innerW
	p.viewport.Height = innerH
	if len(p.entries) > 0 {
		content, offsets := buildLogContent(p.entries, p.cursor, innerW, p.focused)
		p.entryOffsets = offsets
		p.viewport.SetContent(content)
	}
}

// SetFocused toggles focus highlight and re-renders the cursor.
func (p *LogPane) SetFocused(f bool) {
	p.focused = f
	p.rerender()
}

// SelectedChange returns the CL number of the currently selected entry, or "".
func (p *LogPane) SelectedChange() string {
	if len(p.entries) == 0 || p.cursor >= len(p.entries) {
		return ""
	}
	return p.entries[p.cursor].Change
}

// SetEntries replaces the displayed log entries and resets the cursor.
func (p *LogPane) SetEntries(entries []p4.FilelogEntry) {
	p.entries = entries
	p.cursor = 0
	w := p.innerW
	if w <= 0 {
		w = p.viewport.Width
	}
	content, offsets := buildLogContent(entries, p.cursor, w, p.focused)
	p.entryOffsets = offsets
	p.viewport.SetContent(content)
	p.viewport.GotoTop()
}

// Init satisfies tea.Model.
func (p *LogPane) Init() tea.Cmd { return nil }

// Update handles j/k cursor movement and viewport scrolling.
func (p *LogPane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if p.cursor < len(p.entries)-1 {
				p.cursor++
				p.rerender()
				p.scrollToCursor()
			}
			return nil
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
				p.rerender()
				p.scrollToCursor()
			}
			return nil
		}
	}
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

func (p *LogPane) rerender() {
	w := p.innerW
	if w <= 0 {
		w = p.viewport.Width
	}
	content, offsets := buildLogContent(p.entries, p.cursor, w, p.focused)
	p.entryOffsets = offsets
	p.viewport.SetContent(content)
}

func (p *LogPane) scrollToCursor() {
	if p.cursor >= len(p.entryOffsets) {
		return
	}
	line := p.entryOffsets[p.cursor]
	if line < p.viewport.YOffset {
		p.viewport.YOffset = line
	} else if line >= p.viewport.YOffset+p.viewport.Height {
		p.viewport.YOffset = line - p.viewport.Height + 1
	}
}

// buildLogContent renders all entries and returns the content string plus
// a slice of viewport line offsets (one per entry) for cursor scrolling.
func buildLogContent(entries []p4.FilelogEntry, cursor, width int, focused bool) (string, []int) {
	if len(entries) == 0 {
		return styleLogMeta.Render("No history"), nil
	}
	descWidth := width - 2
	if descWidth < 10 {
		descWidth = 10
	}
	var sb strings.Builder
	offsets := make([]int, len(entries))
	line := 0
	for i, e := range entries {
		offsets[i] = line
		header := fmt.Sprintf("CL %s  %s", e.Change, e.Date)
		if i == cursor && focused {
			sb.WriteString(styleCursor.Render(header))
		} else {
			sb.WriteString(styleLogCL.Render(header))
		}
		sb.WriteByte('\n')
		line++
		var meta string
		if e.Action != "" {
			meta = fmt.Sprintf("  %s  %s", e.Action, e.Client)
		} else {
			meta = fmt.Sprintf("  %s", e.Client)
		}
		sb.WriteString(styleLogMeta.Render(meta))
		sb.WriteByte('\n')
		line++
		desc := strings.TrimSpace(e.Description)
		if desc != "" {
			for _, l := range wrapText(desc, descWidth) {
				sb.WriteString(styleLogDesc.Render("  " + l))
				sb.WriteByte('\n')
				line++
			}
		}
		if i < len(entries)-1 {
			sb.WriteByte('\n')
			line++
		}
	}
	return sb.String(), offsets
}

// wrapText wraps text at word boundaries within maxWidth characters.
func wrapText(text string, maxWidth int) []string {
	var lines []string
	for _, paragraph := range strings.Split(text, "\n") {
		paragraph = strings.TrimSpace(paragraph)
		if paragraph == "" {
			continue
		}
		words := strings.Fields(paragraph)
		line := ""
		for _, w := range words {
			if line == "" {
				line = w
			} else if len(line)+1+len(w) <= maxWidth {
				line += " " + w
			} else {
				lines = append(lines, line)
				line = w
			}
		}
		if line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}
