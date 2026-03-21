package panes

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

var (
	styleStreamCurrent = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	styleStreamNormal  = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))
	styleStreamTree    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleStreamType    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// StreamsPane displays the stream hierarchy for the current depot.
type StreamsPane struct {
	streams      []p4.StreamInfo
	current      string // current stream path e.g. //depot/main
	cursor       int
	visiblePaths []string // depth-first render order, matches row indices
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

// Update handles j/k navigation.
func (p *StreamsPane) Update(msg tea.Msg) tea.Cmd {
	if key, ok := msg.(tea.KeyMsg); ok {
		switch key.String() {
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

	typeLabel := styleStreamType.Render(" (" + s.Type + ")")
	var namePart string
	if s.Path == p.current {
		namePart = styleStreamCurrent.Render("* " + name)
	} else {
		namePart = styleStreamNormal.Render(name)
	}

	var line string
	if *idx == p.cursor && p.focused {
		// Build plain text for cursor row: inner ANSI resets from concatenated
		// styled strings would kill the cursor background mid-row, so render as
		// one clean string with no inner style conflicts.
		cursorName := name
		if s.Path == p.current {
			cursorName = "* " + name
		}
		line = styleCursor.Width(p.width - 2).Render(prefix + connector + cursorName + " (" + s.Type + ")")
	} else {
		line = styleStreamTree.Render(prefix+connector) + namePart + typeLabel
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

	for len(rows) < innerH {
		rows = append(rows, "")
	}

	content := strings.Join(rows[:min(len(rows), innerH)], "\n")

	border := styleBlurBorder
	if p.focused {
		border = styleFocusBorder
	}
	rendered := border.Width(innerW).Height(innerH).Render(content)
	return injectTitle(rendered, "3", "Streams", p.width, p.focused)
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
