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
	diffActive   bool   // false = History is visible (this pane is shown)
	width        int
	height       int
	innerW       int    // width - 2, used for word-wrap
	titleNum     string // section number shown in header, e.g. "5"
	titleName    string // section name shown in header, e.g. "History"
	currentCL    string // CL the workspace is currently synced to
}

var (
	styleLogCL     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleLogCLNum  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleLogMeta   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
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
		content, offsets := buildLogContent(p.entries, p.cursor, innerW, p.focused, p.currentCL)
		p.entryOffsets = offsets
		p.viewport.SetContent(content)
	}
}

// SetFocused toggles focus highlight and re-renders the cursor.
func (p *LogPane) SetFocused(f bool) {
	p.focused = f
	p.rerender()
}

// SetDiffActive sets whether Diff (true) or History (false) is the visible pane.
func (p *LogPane) SetDiffActive(active bool) {
	p.diffActive = active
}

// ScrollOffset returns the viewport's top line offset.
func (p *LogPane) ScrollOffset() int { return p.viewport.YOffset }

// SetCursorByLine moves the cursor to the entry that contains the given viewport line.
func (p *LogPane) JumpTop() {
	p.cursor = 0
	p.rerender()
	p.scrollToCursor()
}
func (p *LogPane) JumpBottom() {
	if len(p.entries) > 0 {
		p.cursor = len(p.entries) - 1
		p.rerender()
		p.scrollToCursor()
	}
}

func (p *LogPane) SetCursorByLine(line int) {
	absLine := p.viewport.YOffset + line
	// Find the last entry whose offset is <= absLine.
	idx := 0
	for i, off := range p.entryOffsets {
		if off <= absLine {
			idx = i
		}
	}
	if idx != p.cursor {
		p.cursor = idx
		p.rerender()
		p.scrollToCursor()
	}
}

// SelectedChange returns the CL number of the currently selected entry, or "".
func (p *LogPane) SelectedChange() string {
	if len(p.entries) == 0 || p.cursor >= len(p.entries) {
		return ""
	}
	return p.entries[p.cursor].Change
}

// SetCurrentCL sets the CL the workspace is currently synced to, for `*` marking.
func (p *LogPane) SetCurrentCL(cl string) {
	p.currentCL = cl
	p.rerender()
}

// SetEntries replaces the displayed log entries and resets the cursor.
func (p *LogPane) SetEntries(entries []p4.FilelogEntry) {
	p.entries = entries
	p.cursor = 0
	w := p.innerW
	if w <= 0 {
		w = p.viewport.Width
	}
	content, offsets := buildLogContent(entries, p.cursor, w, p.focused, p.currentCL)
	p.entryOffsets = offsets
	p.viewport.SetContent(content)
	p.viewport.GotoTop()
}

// Init satisfies tea.Model.
func (p *LogPane) Init() tea.Cmd { return nil }

// Update handles j/k cursor movement and viewport scrolling.
func (p *LogPane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			if p.cursor > 0 {
				p.cursor--
				p.rerender()
				p.scrollToCursor()
			}
			return nil
		case tea.MouseButtonWheelDown:
			if p.cursor < len(p.entries)-1 {
				p.cursor++
				p.rerender()
				p.scrollToCursor()
			}
			return nil
		}
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
	return injectDualTitle(rendered, p.titleNum, "Diff", p.titleName, p.diffActive, p.width, p.focused)
}

func (p *LogPane) rerender() {
	w := p.innerW
	if w <= 0 {
		w = p.viewport.Width
	}
	content, offsets := buildLogContent(p.entries, p.cursor, w, p.focused, p.currentCL)
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
func buildLogContent(entries []p4.FilelogEntry, cursor, width int, focused bool, currentCL string) (string, []int) {
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
		var suffix string
		// Split "22 Mar 2026 15:04" into date and optional time parts.
		datePart, timePart := e.Date, ""
		if idx := strings.LastIndex(e.Date, " "); idx >= 0 && len(e.Date)-idx == 6 {
			datePart = e.Date[:idx]
			timePart = e.Date[idx+1:]
		}
		if e.Action != "" {
			suffix = fmt.Sprintf("  %s  %s  %s  %s", e.Client, timePart, datePart, e.Action)
		} else if timePart != "" {
			suffix = fmt.Sprintf("  %s  %s  %s", e.Client, timePart, datePart)
		} else {
			suffix = fmt.Sprintf("  %s  %s", e.Client, datePart)
		}
		marker := "  "
		if e.Change == currentCL {
			marker = "* "
		}
		if i == cursor && focused {
			sb.WriteString(styleCursor.Render(marker + "CL " + e.Change + suffix))
		} else {
			sb.WriteString(styleLogCLNum.Render(marker) + styleLogCLNum.Render("CL "+e.Change) + styleLogCL.Render(suffix))
		}
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
