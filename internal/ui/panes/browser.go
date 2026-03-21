package panes

import (
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// BrowserOpenFileMsg is emitted when the user presses Enter on a file node.
type BrowserOpenFileMsg struct {
	DepotPath string
}

// BrowserMode controls whether the browser shows workspace (synced) or full depot files.
type BrowserMode int

const (
	BrowserModeWorkspace BrowserMode = iota
	BrowserModeDepot
)

// BrowserNeedsLoadMsg is emitted when a directory node needs its children loaded.
type BrowserNeedsLoadMsg struct {
	Path string
	Mode BrowserMode
}

// BrowserNeedsSearchMsg is emitted when the search index needs to be populated.
type BrowserNeedsSearchMsg struct {
	Root string
	Mode BrowserMode
}

type browserNode struct {
	name     string
	path     string
	isDir    bool
	loaded   bool
	expanded bool
	children []*browserNode
}

type browserRow struct {
	node  *browserNode
	depth int
}

var (
	styleBrowserDir      = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan
	styleBrowserWsFile   = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))            // white (workspace)
	styleBrowserDepotFile = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))           // blue (depot)
	styleBrowserLoading  = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
)

// BrowserPane is a lazy-loading tree browser for workspace or depot files.
type BrowserPane struct {
	mode         BrowserMode
	root         *browserNode
	rows         []browserRow
	cursor       int
	focused      bool
	width        int
	height       int
	scrollOff    int
	filterMode   bool
	filter       string
	filterCursor int
	searchIndex  []*browserNode // flat list of all files loaded for search
}

func NewBrowserPane() *BrowserPane {
	return &BrowserPane{}
}

func (p *BrowserPane) SetRoot(path string) {
	name := path
	if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
		name = path[idx+1:]
	}
	p.root = &browserNode{name: name, path: path, isDir: true}
	p.cursor = 0
	p.scrollOff = 0
	p.searchIndex = nil
	p.rebuild()
}

// RootPath returns the depot path of the root node.
func (p *BrowserPane) RootPath() string {
	if p.root == nil {
		return ""
	}
	return p.root.path
}

func (p *BrowserPane) Mode() BrowserMode { return p.mode }

func (p *BrowserPane) ModeLabel() string {
	if p.mode == BrowserModeWorkspace {
		return "Workspace Browser"
	}
	return "Depot Browser"
}

func (p *BrowserPane) ToggleMode() {
	if p.mode == BrowserModeWorkspace {
		p.mode = BrowserModeDepot
	} else {
		p.mode = BrowserModeWorkspace
	}
	if p.root != nil {
		p.root.loaded = false
		p.root.expanded = false
		p.root.children = nil
	}
	p.cursor = 0
	p.scrollOff = 0
	p.searchIndex = nil
	p.rebuild()
}

func (p *BrowserPane) SetFocused(f bool) { p.focused = f }

func (p *BrowserPane) InFilterMode() bool { return p.filterMode }

func (p *BrowserPane) HasFilter() bool { return p.filter != "" }

// expandToNode expands all ancestor directories so that node becomes visible.
// Returns true if node was found in the subtree.
func (p *BrowserPane) expandToNode(current, target *browserNode) bool {
	if current == target {
		return true
	}
	for _, child := range current.children {
		if p.expandToNode(child, target) {
			current.expanded = true
			return true
		}
	}
	return false
}

// NavigateTo finds the node at path, expands ancestors, rebuilds, and sets the
// cursor. Returns true if the node was found in the loaded tree.
func (p *BrowserPane) NavigateTo(path string) bool {
	node := p.findNode(p.root, path)
	if node == nil {
		return false
	}
	p.expandToNode(p.root, node)
	p.rebuild()
	for i, r := range p.rows {
		if r.node == node {
			p.cursor = i
			return true
		}
	}
	return false
}

// FirstUnloadedAncestor returns the path of the shallowest directory under
// root that is on the way to targetPath but hasn't been loaded yet.
func (p *BrowserPane) FirstUnloadedAncestor(targetPath string) string {
	return p.firstUnloaded(p.root, targetPath)
}

func (p *BrowserPane) firstUnloaded(node *browserNode, targetPath string) string {
	if node == nil || !node.isDir {
		return ""
	}
	if node.path != targetPath && !strings.HasPrefix(targetPath, node.path+"/") {
		return ""
	}
	if !node.loaded {
		return node.path
	}
	for _, child := range node.children {
		if result := p.firstUnloaded(child, targetPath); result != "" {
			return result
		}
	}
	return ""
}

// ClearFilter clears the active filter and tries to restore the cursor to the
// previously selected item. Returns the target depot path when the item's
// parent directory hasn't been loaded yet (so the caller can trigger loads).
func (p *BrowserPane) ClearFilter() string {
	var navTarget string
	rows := p.filteredRows()
	if len(rows) > 0 && p.filterCursor < len(rows) {
		targetPath := rows[p.filterCursor].node.path
		if !p.NavigateTo(targetPath) {
			navTarget = targetPath
			// Temporarily land on deepest visible ancestor.
			bestLen := -1
			bestIdx := -1
			for i, r := range p.rows {
				if r.node.isDir && strings.HasPrefix(targetPath, r.node.path+"/") && len(r.node.path) > bestLen {
					bestLen = len(r.node.path)
					bestIdx = i
				}
			}
			if bestIdx >= 0 {
				p.cursor = bestIdx
			}
		}
	}
	p.filter = ""
	p.filterMode = false
	p.filterCursor = 0
	return navTarget
}

func (p *BrowserPane) ScrollOffset() int { return p.scrollOff }

func (p *BrowserPane) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(p.rows) && len(p.rows) > 0 {
		idx = len(p.rows) - 1
	}
	if idx >= 0 {
		p.cursor = idx
	}
}

func (p *BrowserPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// collectNodes recursively appends all nodes in the tree (ignoring expanded state).
func (p *BrowserPane) collectNodes(node *browserNode, depth int, out *[]browserRow) {
	if node == nil {
		return
	}
	*out = append(*out, browserRow{node: node, depth: depth})
	for _, c := range node.children {
		p.collectNodes(c, depth+1, out)
	}
}

// LoadSearchIndex stores a flat list of all files for use during filtering.
func (p *BrowserPane) LoadSearchIndex(files []string) {
	p.searchIndex = nil
	for _, f := range files {
		name := f
		if idx := strings.LastIndex(f, "/"); idx >= 0 && idx < len(f)-1 {
			name = f[idx+1:]
		}
		p.searchIndex = append(p.searchIndex, &browserNode{name: name, path: f})
	}
}

// filteredRows returns the rows to display given the current filter.
// When filter is empty it returns p.rows (normal tree view).
// When a search index is loaded, it is used instead of the in-memory tree.
func (p *BrowserPane) filteredRows() []browserRow {
	if p.filter == "" {
		return p.rows
	}
	if len(p.searchIndex) > 0 {
		var out []browserRow
		for _, n := range p.searchIndex {
			if fuzzyMatch(p.filter, n.name) {
				out = append(out, browserRow{node: n, depth: 0})
			}
		}
		return out
	}
	// Fall back to traversing the loaded in-memory tree.
	var all []browserRow
	p.collectNodes(p.root, 0, &all)
	var out []browserRow
	for _, r := range all {
		if fuzzyMatch(p.filter, r.node.name) {
			out = append(out, r)
		}
	}
	return out
}

// SelectedPath returns the depot path of the selected item.
// For directories, appends "/..." for use with p4 sync -f.
func (p *BrowserPane) SelectedPath() string {
	rows := p.filteredRows()
	cur := p.cursor
	if p.filter != "" {
		cur = p.filterCursor
	}
	if len(rows) == 0 || cur >= len(rows) {
		return ""
	}
	n := rows[cur].node
	if n.isDir {
		return n.path + "/..."
	}
	return n.path
}

// LoadChildren populates a node's children after an async load.
func (p *BrowserPane) LoadChildren(parentPath string, dirs, files []string) {
	node := p.findNode(p.root, parentPath)
	if node == nil {
		return
	}
	node.children = nil
	for _, d := range dirs {
		name := d
		if idx := strings.LastIndex(d, "/"); idx >= 0 && idx < len(d)-1 {
			name = d[idx+1:]
		}
		node.children = append(node.children, &browserNode{name: name, path: d, isDir: true})
	}
	for _, f := range files {
		name := f
		if idx := strings.LastIndex(f, "/"); idx >= 0 && idx < len(f)-1 {
			name = f[idx+1:]
		}
		node.children = append(node.children, &browserNode{name: name, path: f, isDir: false})
	}
	node.loaded = true
	node.expanded = true
	p.rebuild()
}

func (p *BrowserPane) findNode(node *browserNode, path string) *browserNode {
	if node == nil {
		return nil
	}
	if node.path == path {
		return node
	}
	for _, c := range node.children {
		if found := p.findNode(c, path); found != nil {
			return found
		}
	}
	return nil
}

func (p *BrowserPane) rebuild() {
	p.rows = nil
	if p.root != nil {
		p.flattenNode(p.root, 0)
	}
	if p.cursor >= len(p.rows) && len(p.rows) > 0 {
		p.cursor = len(p.rows) - 1
	}
}

func (p *BrowserPane) flattenNode(node *browserNode, depth int) {
	p.rows = append(p.rows, browserRow{node: node, depth: depth})
	if node.isDir && node.expanded {
		for _, c := range node.children {
			p.flattenNode(c, depth+1)
		}
	}
}

func (p *BrowserPane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.KeyMsg:
		if p.filterMode {
			rows := p.filteredRows()
			switch m.String() {
			case "esc":
				p.ClearFilter()
				return nil
			case "enter", "/":
				p.filterMode = false
				return nil
			case "j", "down":
				if p.filterCursor < len(rows)-1 {
					p.filterCursor++
				}
				return nil
			case "k", "up":
				if p.filterCursor > 0 {
					p.filterCursor--
				}
				return nil
			case "backspace", "ctrl+h":
				if len(p.filter) > 0 {
					runes := []rune(p.filter)
					p.filter = string(runes[:len(runes)-1])
					p.filterCursor = 0
				}
				return nil
			default:
				if len(m.Runes) == 1 {
					p.filter += string(m.Runes)
					p.filterCursor = 0
				}
				return nil
			}
		}
		if m.String() == "/" {
			p.filterMode = true
			return nil
		}
		switch m.String() {
		case "j", "down":
			if p.filter != "" {
				rows := p.filteredRows()
				if p.filterCursor < len(rows)-1 {
					p.filterCursor++
				}
			} else if p.cursor < len(p.rows)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.filter != "" {
				if p.filterCursor > 0 {
					p.filterCursor--
				}
			} else if p.cursor > 0 {
				p.cursor--
			}
		case "enter":
			return p.toggleOrLoad()
		}
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			if p.cursor > 0 {
				p.cursor--
			}
		case tea.MouseButtonWheelDown:
			if p.cursor < len(p.rows)-1 {
				p.cursor++
			}
		}
	}
	return nil
}

func (p *BrowserPane) toggleOrLoad() tea.Cmd {
	rows := p.filteredRows()
	cur := p.cursor
	if p.filter != "" {
		cur = p.filterCursor
	}
	if len(rows) == 0 || cur >= len(rows) {
		return nil
	}
	node := rows[cur].node
	if !node.isDir {
		path := node.path
		return func() tea.Msg { return BrowserOpenFileMsg{DepotPath: path} }
	}
	if node.loaded {
		node.expanded = !node.expanded
		p.rebuild()
		return nil
	}
	// needs async load
	path := node.path
	mode := p.mode
	return func() tea.Msg {
		return BrowserNeedsLoadMsg{Path: path, Mode: mode}
	}
}

func (p *BrowserPane) View() string {
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
	rendered := border.Width(innerW).Height(innerH).Render(p.renderLines(innerW, innerH))
	return injectTitle(rendered, "1", p.ModeLabel(), p.width, p.focused)
}

func (p *BrowserPane) renderLines(innerW, innerH int) string {
	if p.root == nil {
		return styleBrowserLoading.Render("Waiting for workspace info...")
	}

	rows := p.filteredRows()
	cur := p.cursor
	if p.filter != "" {
		cur = p.filterCursor
	}

	if len(rows) == 0 && p.filter == "" {
		return styleBrowserLoading.Render("Empty")
	}

	showFilterBar := p.filterMode || p.filter != ""
	contentH := innerH
	if showFilterBar {
		contentH = innerH - 1
		if contentH < 0 {
			contentH = 0
		}
	}

	// scroll
	if cur < p.scrollOff {
		p.scrollOff = cur
	}
	if cur >= p.scrollOff+contentH {
		p.scrollOff = cur - contentH + 1
	}

	var sb strings.Builder
	end := p.scrollOff + contentH
	if end > len(rows) {
		end = len(rows)
	}
	for i := p.scrollOff; i < end; i++ {
		row := rows[i]
		line := p.renderRow(row, innerW)
		if i == cur && p.focused {
			line = styleCursor.Width(innerW).Render(line)
		}
		sb.WriteString(line)
		if i < end-1 {
			sb.WriteByte('\n')
		}
	}

	if showFilterBar {
		filterBar := styleFilterBar.Render("/ " + p.filter)
		if sb.Len() > 0 {
			sb.WriteByte('\n')
		}
		sb.WriteString(filterBar)
	}

	return sb.String()
}

func (p *BrowserPane) renderRow(row browserRow, maxW int) string {
	indent := strings.Repeat("  ", row.depth)
	if row.node.isDir {
		icon := "▶ "
		if row.node.expanded {
			icon = "▼ "
		}
		return styleBrowserDir.Render(indent + icon + row.node.name)
	}
	if p.mode == BrowserModeDepot {
		return styleBrowserDepotFile.Render(indent + "  " + row.node.name)
	}
	return styleBrowserWsFile.Render(indent + "  " + row.node.name)
}
