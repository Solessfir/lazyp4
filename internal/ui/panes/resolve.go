package panes

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

// ResolvePane lists files needing resolution.
type ResolvePane struct {
	conflicts []p4.ConflictFile
	cursor    int
	focused   bool
	width     int
	height    int
}

var (
	styleConflict = lipgloss.NewStyle().Foreground(lipgloss.Color("9")).Bold(true)
	styleResolved = lipgloss.NewStyle().Faint(true)
)

// NewResolvePane creates an empty resolve pane.
func NewResolvePane() *ResolvePane {
	return &ResolvePane{}
}

// SetSize updates dimensions.
func (p *ResolvePane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused toggles focus highlight.
func (p *ResolvePane) SetFocused(f bool) {
	p.focused = f
}

// SetConflicts replaces the displayed conflict list.
func (p *ResolvePane) SetConflicts(conflicts []p4.ConflictFile) {
	p.conflicts = conflicts
	if p.cursor >= len(p.conflicts) {
		p.cursor = 0
	}
}

// HasConflicts reports whether there are any unresolved conflicts.
func (p *ResolvePane) HasConflicts() bool { return len(p.conflicts) > 0 }

// Init satisfies tea.Model.
func (p *ResolvePane) Init() tea.Cmd { return nil }

// Update handles navigation and launching the merge tool.
func (p *ResolvePane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if p.cursor < len(p.conflicts)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		case "enter":
			return p.openMergeTool()
		case "a":
			return p.autoResolve("-am")
		case "t":
			return p.autoResolve("-at")
		case "y":
			return p.autoResolve("-ay")
		case "s":
			return p.autoResolve("-as")
		}
	}
	return nil
}

// autoResolve preserves the displayed conflict scope.
func (p *ResolvePane) autoResolve(flags ...string) tea.Cmd {
	if len(p.conflicts) == 0 {
		return nil
	}
	files := make([]string, 0, len(p.conflicts))
	seen := make(map[string]bool)
	for _, conflict := range p.conflicts {
		if !seen[conflict.ClientFile] {
			seen[conflict.ClientFile] = true
			files = append(files, conflict.ClientFile)
		}
	}
	return func() tea.Msg { return ResolveAutoMsg{Files: files, Flags: flags} }
}

// openMergeTool requests an interactive Perforce resolve for the selected file.
func (p *ResolvePane) openMergeTool() tea.Cmd {
	if len(p.conflicts) == 0 || p.cursor >= len(p.conflicts) {
		return nil
	}
	file := p.conflicts[p.cursor].ClientFile

	return func() tea.Msg { return ResolveInteractiveMsg{File: file} }
}

type ResolveInteractiveMsg struct{ File string }

// ResolveFinishedMsg is sent after the merge tool exits.
type ResolveFinishedMsg struct{ Err error }

// ResolveAutoMsg is sent when the user triggers a scoped auto-resolve action.
type ResolveAutoMsg struct {
	Files []string
	Flags []string
}

// View renders the pane.
func (p *ResolvePane) View() string {
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
	rendered := border.Width(innerW).Height(innerH).Render(p.renderLines(innerH))
	return injectTitle(rendered, "5", "Conflicts", p.width, p.focused)
}

func (p *ResolvePane) renderLines(maxH int) string {
	if len(p.conflicts) == 0 {
		return styleResolved.Render("No conflicts")
	}

	var sb strings.Builder
	start := max(0, p.cursor-maxH+1)
	end := min(start+maxH, len(p.conflicts))
	for i := start; i < end; i++ {
		c := p.conflicts[i]
		label := fmt.Sprintf("  %s", shortName(c.ClientFile, p.width-6))
		var line string
		if i == p.cursor {
			line = cursorStyle(p.focused).Width(p.width - 4).Render(styleConflict.Render(label))
		} else {
			line = styleConflict.Render(label)
		}
		sb.WriteString(line)
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}
