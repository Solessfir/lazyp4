package panes

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

const ShelvedMaxHeight = 10

var (
	styleShelvedCL   = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleShelvedFile = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleShelvedAct  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
)

type shelvedRowKind int

const (
	shelvedRowCL shelvedRowKind = iota
	shelvedRowFile
)

type shelvedRow struct {
	kind  shelvedRowKind
	clIdx int
	file  int
}

// ShelvedPane displays shelved changelists for the current workspace.
type ShelvedPane struct {
	cls      []p4.ShelvedCL
	expanded map[string]bool
	rows     []shelvedRow
	cursor   int
	focused  bool
	width    int
	height   int
	scroll   int
}

func NewShelvedPane() *ShelvedPane {
	return &ShelvedPane{expanded: map[string]bool{}}
}

func (p *ShelvedPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *ShelvedPane) SetFocused(f bool) { p.focused = f }

func (p *ShelvedPane) SetCLs(cls []p4.ShelvedCL) {
	p.cls = cls
	// default all expanded
	for _, cl := range cls {
		if _, ok := p.expanded[cl.ID]; !ok {
			p.expanded[cl.ID] = true
		}
	}
	p.buildRows()
	if p.cursor >= len(p.rows) {
		p.cursor = max(0, len(p.rows)-1)
	}
}

func (p *ShelvedPane) buildRows() {
	p.rows = nil
	for ci, cl := range p.cls {
		p.rows = append(p.rows, shelvedRow{kind: shelvedRowCL, clIdx: ci})
		if p.expanded[cl.ID] {
			for fi := range cl.Files {
				p.rows = append(p.rows, shelvedRow{kind: shelvedRowFile, clIdx: ci, file: fi})
			}
		}
	}
}

// PreferredHeight returns the height this pane wants based on content.
func (p *ShelvedPane) PreferredHeight() int {
	rows := len(p.rows)
	if rows == 0 {
		rows = 1 // "No shelved changes"
	}
	h := rows + 2 // borders
	if h > ShelvedMaxHeight {
		h = ShelvedMaxHeight
	}
	if h < 3 {
		h = 3
	}
	return h
}

// ScrollOffset returns the index of the first visible row.
func (p *ShelvedPane) ScrollOffset() int { return p.scroll }

// SetCursor moves the cursor to idx, clamped to valid range.
func (p *ShelvedPane) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(p.rows) {
		idx = len(p.rows) - 1
	}
	if idx >= 0 {
		p.cursor = idx
		p.clampScroll()
	}
}

// SelectedCL returns the CL ID under the cursor, or "".
func (p *ShelvedPane) SelectedCL() string {
	if len(p.rows) == 0 || p.cursor >= len(p.rows) {
		return ""
	}
	r := p.rows[p.cursor]
	return p.cls[r.clIdx].ID
}

func (p *ShelvedPane) Update(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
		case "j", "down":
			if p.cursor < len(p.rows)-1 {
				p.cursor++
				p.clampScroll()
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
				p.clampScroll()
			}
		case "enter":
			if len(p.rows) > 0 {
				r := p.rows[p.cursor]
				cl := p.cls[r.clIdx]
				p.expanded[cl.ID] = !p.expanded[cl.ID]
				p.buildRows()
				p.clampScroll()
			}
		}
	}
	return nil
}

func (p *ShelvedPane) clampScroll() {
	innerH := p.height - 2
	if innerH < 1 {
		innerH = 1
	}
	if p.cursor < p.scroll {
		p.scroll = p.cursor
	}
	if p.cursor >= p.scroll+innerH {
		p.scroll = p.cursor - innerH + 1
	}
}

func (p *ShelvedPane) View() string {
	innerW := p.width - 2
	innerH := p.height - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}

	var lines []string
	if len(p.rows) == 0 {
		lines = append(lines, styleShelvedFile.Render("No shelved changes"))
	} else {
		end := p.scroll + innerH
		if end > len(p.rows) {
			end = len(p.rows)
		}
		for i := p.scroll; i < end; i++ {
			r := p.rows[i]
			cl := p.cls[r.clIdx]
			var line string
			switch r.kind {
			case shelvedRowCL:
				arrow := "▼"
				if !p.expanded[cl.ID] {
					arrow = "▶"
				}
				desc := cl.Description
				if idx := strings.Index(desc, "\n"); idx >= 0 {
					desc = desc[:idx]
				}
				line = styleShelvedCL.Render(fmt.Sprintf("%s CL %s  %s", arrow, cl.ID, desc))
			case shelvedRowFile:
				f := cl.Files[r.file]
				name := f.DepotFile
				if idx := strings.LastIndex(name, "/"); idx >= 0 {
					name = name[idx+1:]
				}
				line = styleShelvedFile.Render("    ") +
					styleShelvedAct.Render(fmt.Sprintf("%-7s", string(f.Action))) +
					styleShelvedFile.Render(name)
			}
			if i == p.cursor && p.focused {
				line = styleCursor.Width(innerW).Render(line)
			}
			lines = append(lines, line)
		}
	}

	for len(lines) < innerH {
		lines = append(lines, "")
	}

	border := styleBlurBorder
	if p.focused {
		border = styleFocusBorder
	}
	rendered := border.Width(innerW).Height(innerH).Render(strings.Join(lines, "\n"))
	return injectTitle(rendered, "4", "Shelved", p.width, p.focused)
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
