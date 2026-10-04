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
	cursor       int
	visiblePaths []string // depth-first render order, matches row indices
	scrollOffset int
	focused      bool
	width        int
	height       int
}

func NewStreamsPane() *StreamsPane {
	return &StreamsPane{}
}

func (p *StreamsPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

func (p *StreamsPane) SetFocused(f bool) { p.focused = f }

func (p *StreamsPane) ScrollOffset() int { return p.scrollOffset }

func (p *StreamsPane) SetStreams(streams []p4.StreamInfo, current string) {
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
	if s.Path == p.current {
		marker = "* "
		nameStyle = styleStreamCurrent
	}

	innerW := max(1, p.width-2)
	name = ansi.Truncate(name, max(0, innerW-lipgloss.Width(marker+prefix+connector)), "…")
	line := nameStyle.Render(marker) + styleStreamTree.Render(prefix+connector) + nameStyle.Render(name)
	if *idx == p.cursor {
		style := cursorStyle(p.focused)
		if s.Path == p.current {
			style = style.Foreground(lipgloss.Color("2"))
		}
		// Render the selection in one style so nested resets cannot clear its background.
		line = style.Width(innerW).Render(ansi.Truncate(marker+prefix+connector+name, innerW, "…"))
	}
	*rows = append(*rows, ansi.Truncate(line, innerW, "…"))
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
	return rendered
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
