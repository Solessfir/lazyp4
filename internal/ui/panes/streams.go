package panes

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/solessfir/lazyp4/internal/p4"
)

var (
	styleStreamCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleStreamNormal  = lipgloss.NewStyle()
	styleStreamTree    = lipgloss.NewStyle().Faint(true)
)

// StreamsPane displays the stream hierarchy for the current depot.
type StreamsPane struct {
	streams      []p4.StreamInfo
	current      string // current stream path e.g. //depot/main
	pending      int
	activity     string
	cursor       int
	visiblePaths []string // depth-first render order, matches row indices
	scrollOffset int
	focused      bool
	width        int
	height       int
}

func NewStreamsPane() *StreamsPane {
	return &StreamsPane{pending: -1}
}

func (p *StreamsPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *StreamsPane) SetFocused(f bool) { p.focused = f }

func (p *StreamsPane) ScrollOffset() int { return p.scrollOffset }

func (p *StreamsPane) SetPending(n int)        { p.pending = n }
func (p *StreamsPane) SetActivity(text string) { p.activity = text }

func (p *StreamsPane) SetStreams(streams []p4.StreamInfo, current string) {
	if p.current != "" && current != p.current {
		p.pending = -1
		p.activity = ""
	}
	p.streams = streams
	p.current = current
	p.buildVisibleList()
}

// buildVisibleList rebuilds the flat depth-first ordered list of stream paths
// and positions the cursor on the current stream if found.
func (p *StreamsPane) buildVisibleList() {
	p.visiblePaths = nil
	roots := buildTree(p.streams)
	for _, root := range roots {
		p.collectPaths(root)
	}
	// Try to keep cursor on the current stream.
	for i, path := range p.visiblePaths {
		if path == p.current {
			p.cursor = i
			return
		}
	}
	if p.cursor >= len(p.visiblePaths) {
		p.cursor = max(0, len(p.visiblePaths)-1)
	}
}

func (p *StreamsPane) collectPaths(node *streamNode) {
	p.visiblePaths = append(p.visiblePaths, node.stream.Path)
	for _, child := range node.children {
		p.collectPaths(child)
	}
}

// SelectedStream returns the stream path under the cursor, or "".
func (p *StreamsPane) SelectedStream() string {
	if p.cursor < len(p.visiblePaths) {
		return p.visiblePaths[p.cursor]
	}
	return ""
}

// SetCursor moves the cursor to idx, clamped to valid range.
func (p *StreamsPane) JumpTop()    { p.SetCursor(0) }
func (p *StreamsPane) JumpBottom() { p.SetCursor(len(p.visiblePaths) - 1) }

func (p *StreamsPane) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(p.visiblePaths) {
		idx = len(p.visiblePaths) - 1
	}
	if idx >= 0 {
		p.cursor = idx
	}
}

// Update handles j/k navigation.
func (p *StreamsPane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			if p.cursor > 0 {
				p.cursor--
			}
		case tea.MouseButtonWheelDown:
			if p.cursor < len(p.visiblePaths)-1 {
				p.cursor++
			}
		}
	case tea.KeyMsg:
		switch m.String() {
		case "j", "down":
			if p.cursor < len(p.visiblePaths)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		}
	}
	return nil
}

type streamNode struct {
	stream   p4.StreamInfo
	children []*streamNode
}

func buildTree(streams []p4.StreamInfo) []*streamNode {
	byPath := make(map[string]*streamNode, len(streams))
	for _, s := range streams {
		s := s
		byPath[s.Path] = &streamNode{stream: s}
	}
	var roots []*streamNode
	for _, s := range streams {
		node := byPath[s.Path]
		if parentNode, ok := byPath[s.Parent]; ok {
			parentNode.children = append(parentNode.children, node)
		} else {
			roots = append(roots, node)
		}
	}
	return roots
}

func (p *StreamsPane) renderNode(node *streamNode, prefix string, isLast bool, depth int, rows *[]string, idx *int) {
	s := node.stream

	name := s.Name
	if name == "" {
		if i := strings.LastIndex(s.Path, "/"); i >= 0 {
			name = s.Path[i+1:]
		} else {
			name = s.Path
		}
	}

	var connector string
	if depth > 0 {
		if isLast {
			connector = "└── "
		} else {
			connector = "├── "
		}
	}

	marker := "  "
	nameStyle := styleStreamNormal
	var badge string
	badgeColor := lipgloss.Color("6")
	if s.Path == p.current {
		marker = "* "
		nameStyle = styleStreamCurrent
		switch {
		case p.activity != "":
			badge = p.activity
		case p.pending > 0:
			badge = fmt.Sprintf("↓%d", p.pending)
			badgeColor = lipgloss.Color("3")
		}
	}

	innerW := max(1, p.width-2)
	nameW := max(0, innerW-lipgloss.Width(marker+prefix+connector))
	badge = ansi.Truncate(badge, max(0, nameW-1), "…")
	if badge != "" {
		nameW -= lipgloss.Width(badge) + 1
	}
	name = ansi.Truncate(name, nameW, "…")
	rowStyle := lipgloss.NewStyle()
	if *idx == p.cursor {
		rowStyle = cursorStyle(p.focused)
	}
	nameStyle = nameStyle.Inherit(rowStyle)
	line := nameStyle.Render(marker) + styleStreamTree.Inherit(rowStyle).Render(prefix+connector) + nameStyle.Render(name)
	if badge != "" {
		line += rowStyle.Foreground(badgeColor).Render(" " + badge)
	}
	line = ansi.Truncate(line, innerW, "…")
	if *idx == p.cursor {
		line += rowStyle.Render(strings.Repeat(" ", max(0, innerW-lipgloss.Width(line))))
	}
	*rows = append(*rows, line)
	*idx++

	var childPrefix string
	if depth == 0 {
		childPrefix = ""
	} else if isLast {
		childPrefix = prefix + "    "
	} else {
		childPrefix = prefix + "│   "
	}
	for i, child := range node.children {
		p.renderNode(child, childPrefix, i == len(node.children)-1, depth+1, rows, idx)
	}
}

func (p *StreamsPane) View() string {
	innerW := p.width - 2
	innerH := p.height - 2
	if innerW < 1 {
		innerW = 1
	}
	if innerH < 1 {
		innerH = 1
	}

	var rows []string
	roots := buildTree(p.streams)
	idx := 0
	for i, root := range roots {
		p.renderNode(root, "", i == len(roots)-1, 0, &rows, &idx)
	}

	if p.cursor < p.scrollOffset {
		p.scrollOffset = p.cursor
	} else if p.cursor >= p.scrollOffset+innerH {
		p.scrollOffset = p.cursor - innerH + 1
	}
	p.scrollOffset = max(0, min(p.scrollOffset, len(rows)-innerH))
	rows = rows[p.scrollOffset:min(len(rows), p.scrollOffset+innerH)]
	for len(rows) < innerH {
		rows = append(rows, "")
	}

	content := strings.Join(rows[:min(len(rows), innerH)], "\n")

	border := styleBlurBorder
	if p.focused {
		border = styleFocusBorder
	}
	rendered := border.Width(innerW).Height(innerH).Render(content)
	rendered = injectTitle(rendered, "3", "Streams", p.width, p.focused)
	if len(p.visiblePaths) > 0 {
		counter := fmt.Sprintf("%d of %d", p.cursor+1, len(p.visiblePaths))
		label := counter
		for _, stream := range p.streams {
			if stream.Path == p.SelectedStream() && stream.Type != "" {
				candidate := stream.Type + " · " + counter
				if lipgloss.Width(candidate)+4 <= p.width {
					label = candidate
				}
				break
			}
		}
		if lipgloss.Width(label)+4 <= p.width {
			rendered = injectFooter(rendered, label, p.focused)
		}
	}
	if p.width > 0 {
		lines := strings.Split(rendered, "\n")
		for i := range lines {
			lines[i] = ansi.Truncate(lines[i], p.width, "")
		}
		rendered = strings.Join(lines, "\n")
	}
	return rendered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
