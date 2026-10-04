package panes

import (
	"crypto/md5"
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/lucasb-eyer/go-colorful"

	"github.com/solessfir/lazyp4/internal/p4"
)

// LogPane displays p4 filelog output with cursor-based entry selection.
type LogPane struct {
	viewport     viewport.Model
	entries      []p4.FilelogEntry
	cursor       int
	entryOffsets []int // viewport line where each entry starts
	focused      bool
	diffActive   bool // false = History is visible (this pane is shown)
	width        int
	height       int
	innerW       int    // width - 2, used for word-wrap
	titleNum     string // section number shown in header, e.g. "5"
	titleName    string // section name shown in header, e.g. "History"
	currentCL    string // CL the workspace is currently synced to
}

var (
	styleLogMeta = lipgloss.NewStyle().Faint(true)
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
		p.rerender()
		p.scrollToCursor()
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

// SetCurrentCL sets the highest CL represented by the workspace have revisions.
func (p *LogPane) SetCurrentCL(cl string) {
	p.currentCL = cl
	p.rerender()
}

// SetEntries replaces the displayed log entries, preserving the selected CL when possible.
func (p *LogPane) SetEntries(entries []p4.FilelogEntry) {
	prevCL := p.SelectedChange()
	p.entries = entries
	p.cursor = 0
	if prevCL != "" {
		for i, e := range entries {
			if e.Change == prevCL {
				p.cursor = i
				break
			}
		}
	}
	w := p.innerW
	if w <= 0 {
		w = p.viewport.Width
	}
	content, offsets := buildLogContent(entries, p.cursor, w, p.focused, p.currentCL)
	p.entryOffsets = offsets
	p.viewport.SetContent(content)
	p.scrollToCursor()
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
	rendered = injectDualTitle(rendered, p.titleNum, "Diff", p.titleName, p.diffActive, p.width, p.focused)
	if len(p.entries) > 0 {
		e := p.entries[p.cursor]
		counter := fmt.Sprintf("%d of %d", p.cursor+1, len(p.entries))
		metadata := e.Author
		if e.Client != "" {
			metadata += "@" + e.Client
		}
		if e.Date != "" {
			metadata += " · " + e.Date
		}
		if e.Action != "" {
			metadata += " · " + string(e.Action)
		}
		label := counter
		if available := innerW - ansi.StringWidth(counter) - 5; available > 0 && metadata != "" {
			label = ansi.Truncate(metadata, available, "…") + " · " + counter
		}
		if ansi.StringWidth(label)+2 <= innerW {
			rendered = injectFooter(rendered, label, p.focused)
		}
	}
	return rendered
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
	width = max(1, width)
	var sb strings.Builder
	offsets := make([]int, len(entries))
	line := 0
	have, _ := strconv.Atoi(currentCL)
	for i, e := range entries {
		offsets[i] = line
		base := lipgloss.NewStyle()
		if i == cursor {
			base = cursorStyle(focused)
		}
		idColor := lipgloss.Color("2")
		if change, err := strconv.Atoi(e.Change); have > 0 && err == nil && change > have {
			idColor = lipgloss.Color("4")
		}
		author := shortAuthor(e.Author)
		marker := "○"
		if e.Change == currentCL {
			marker = "●"
		}
		prefix := base.Foreground(idColor).Render("CL "+e.Change) + base.Render(" ") +
			base.Foreground(authorColor(e.Author)).Render(author+" "+marker) + base.Render(" ")
		prefixWidth := ansi.StringWidth(prefix)
		desc := strings.TrimSpace(e.Description)
		if prefixWidth >= width {
			if line > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(ansi.Truncate(prefix, width, "…"))
			line++
			prefixWidth = min(2, width-1)
			prefix = base.Render(strings.Repeat(" ", prefixWidth))
		}
		rows := strings.Split(ansi.Wrap(desc, max(1, width-prefixWidth), ""), "\n")
		for j, text := range rows {
			row := prefix + base.Render(text)
			if j > 0 {
				row = base.Render(strings.Repeat(" ", min(prefixWidth, width-1)) + text)
			}
			row = ansi.Truncate(row, width, "…")
			row += base.Render(strings.Repeat(" ", max(0, width-ansi.StringWidth(row))))
			if line > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(row)
			line++
		}
	}
	return sb.String(), offsets
}

func shortAuthor(author string) string {
	parts := strings.Fields(author)
	if len(parts) > 1 {
		author = string([]rune(parts[0])[:1]) + string([]rune(parts[1])[:1])
	}
	author = ansi.Truncate(author, 2, "")
	return author + strings.Repeat(" ", max(0, 2-ansi.StringWidth(author)))
}

func authorColor(author string) lipgloss.Color {
	hash := md5.Sum([]byte(author))
	fraction := func(bytes []byte) float64 {
		sum := 0
		for _, b := range bytes {
			sum += int(b)
		}
		return float64(sum%100) / 100
	}
	c := colorful.Hsl(fraction(hash[0:4])*360, 0.6+0.4*fraction(hash[4:8]), 0.4+0.2*fraction(hash[8:12]))
	return lipgloss.Color(fmt.Sprintf("#%02x%02x%02x", uint8(c.R*255), uint8(c.G*255), uint8(c.B*255)))
}
