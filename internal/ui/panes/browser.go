package panes

import (
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
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
	name           string
	path           string // tree identity in the current browser namespace
	depotPath      string
	localPath      string // absolute local path (workspace mode only)
	isDir          bool
	tracked        bool // synced via p4 have (workspace mode only; always true in depot mode)
	loaded         bool
	expanded       bool
	children       []*browserNode
	openedByMe     bool
	openedByOthers bool
	status         *workspaceStatus
}

type workspaceStatus struct {
	haveFiles    []string
	depotDirs    []string
	missingFiles []p4.WorkspaceEntry
	yoursOpen    map[string]bool
	othersOpen   map[string]bool
}

type browserRow struct {
	node  *browserNode
	depth int
}

var (
	styleBrowserDir       = lipgloss.NewStyle().Foreground(lipgloss.Color("6")).Bold(true) // cyan
	styleBrowserWsFile    = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))            // white (workspace)
	styleBrowserDepotFile = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))            // blue (depot)
	styleBrowserLoading   = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	styleOpenMe           = lipgloss.NewStyle().Foreground(lipgloss.Color("2")) // green — opened by you
	styleOpenOther        = lipgloss.NewStyle().Foreground(lipgloss.Color("4")) // blue — opened by others
	styleOpenBoth         = lipgloss.NewStyle().Foreground(lipgloss.Color("3")) // yellow — opened by both
	styleBrowserUntracked = lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // gray — local only, not in depot
)

// BrowserPane is a lazy-loading tree browser for workspace or depot files.
type BrowserPane struct {
	mode          BrowserMode
	root          *browserNode
	rows          []browserRow
	cursor        int
	focused       bool
	width         int
	height        int
	scrollOff     int
	filterMode    bool
	filter        string
	filterCursor  int
	searchIndex   []*browserNode // flat list of all files loaded for search
	depotRoot     string
	workspaceRoot string
}

func NewBrowserPane() *BrowserPane {
	return &BrowserPane{}
}

func (p *BrowserPane) SetRoots(depotRoot, workspaceRoot string) {
	p.depotRoot = depotRoot
	p.workspaceRoot = workspaceRoot
	if p.mode == BrowserModeWorkspace {
		p.SetRoot(workspaceRoot)
	} else {
		p.SetRoot(depotRoot)
	}
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

// RootPath returns the depot path of the root node (or the stored path if tree not yet loaded).
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
	// Reset tree — app will call cmdBrowserLoad + navigate to selected file.
	path := p.depotRoot
	if p.mode == BrowserModeWorkspace {
		path = p.workspaceRoot
	}
	if path == "" {
		path = p.RootPath()
	}
	p.SetRoot(path)
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
	if p.filter != "" {
		p.filterCursor = max(0, min(idx, len(p.filteredRows())-1))
		return
	}
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

func (p *BrowserPane) JumpTop()    { p.SetCursor(0) }
func (p *BrowserPane) JumpBottom() { p.SetCursor(len(p.rows) - 1) }

func (p *BrowserPane) CollapseAll() {
	p.collapseNode(p.root)
	p.rebuild()
}

func (p *BrowserPane) collapseNode(node *browserNode) {
	if node == nil {
		return
	}
	node.expanded = false
	for _, c := range node.children {
		p.collapseNode(c)
	}
}

// ExpandSelected1Level expands the selected directory one level deeper.
// If the selected dir is collapsed, it expands it.
// If it is already expanded, it expands each of its direct children one level.
// Returns paths of unloaded dirs that must be fetched before they can expand.
func (p *BrowserPane) ExpandSelected1Level() []string {
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
		return nil
	}

	var toLoad []string
	changed := false

	if !node.expanded {
		// Expand the selected dir itself.
		if node.loaded {
			node.expanded = true
			changed = true
		} else {
			toLoad = append(toLoad, node.path)
		}
	} else {
		// Already expanded — expand its direct children one level.
		for _, child := range node.children {
			if !child.isDir || child.expanded {
				continue
			}
			if child.loaded {
				child.expanded = true
				changed = true
			} else {
				toLoad = append(toLoad, child.path)
			}
		}
	}

	if changed {
		p.rebuild()
	}
	return toLoad
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
		p.searchIndex = append(p.searchIndex, &browserNode{name: name, path: f, tracked: true})
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
// For directories, appends "/..." to make the path recursive.
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
		return strings.TrimSuffix(n.path, "/") + "/..."
	}
	return n.path
}

// BrowserSelection holds information about the currently selected browser node.
type BrowserSelection struct {
	DepotPath  string
	ClientPath string
	LocalPath  string
	IsDir      bool
	Tracked    bool // always true in depot mode
}

// SelectedEntry returns info about the currently selected node, or nil if nothing selected.
func (p *BrowserPane) SelectedEntry() *BrowserSelection {
	rows := p.filteredRows()
	cur := p.cursor
	if p.filter != "" {
		cur = p.filterCursor
	}
	if len(rows) == 0 || cur >= len(rows) {
		return nil
	}
	n := rows[cur].node
	depotPath := n.path
	if n.depotPath != "" {
		depotPath = n.depotPath
	}
	if n.isDir {
		depotPath = strings.TrimSuffix(n.path, "/") + "/..."
	}
	return &BrowserSelection{
		DepotPath:  depotPath,
		ClientPath: n.path,
		LocalPath:  n.localPath,
		IsDir:      n.isDir,
		Tracked:    n.tracked,
	}
}

// LoadedDirPaths returns the depot paths of all currently loaded (expanded or collapsed) directories.
// Used to reload browser content on refresh.
func (p *BrowserPane) LoadedDirPaths() []string {
	var paths []string
	p.collectLoadedDirs(p.root, &paths)
	return paths
}

func (p *BrowserPane) collectLoadedDirs(node *browserNode, out *[]string) {
	if node == nil {
		return
	}
	if node.isDir && node.loaded {
		*out = append(*out, node.path)
		for _, c := range node.children {
			p.collectLoadedDirs(c, out)
		}
	}
}

// RefreshYoursOpen walks all loaded file nodes and updates openedByMe from the given set.
// Call this whenever the user's own open files change (e.g. after revert or p4 edit).
func (p *BrowserPane) RefreshYoursOpen(yours map[string]bool) {
	p.refreshYoursNode(p.root, yours)
}

func (p *BrowserPane) refreshYoursNode(node *browserNode, yours map[string]bool) {
	if node == nil {
		return
	}
	if node.status != nil {
		node.status.yoursOpen = yours
	}
	if !node.isDir {
		path := node.depotPath
		if path == "" {
			path = node.path
		}
		node.openedByMe = yours[path]
	}
	for _, c := range node.children {
		p.refreshYoursNode(c, yours)
	}
}

// LoadChildren populates a node's children after an async load.
// yoursOpen and othersOpen are sets of depot paths; nil means no data available.
// wsEntries is used for workspace mode (includes untracked files); pass nil for depot mode.
// files is used for depot mode when wsEntries is nil.
// Returns the path of the sole directory child if auto-expand should be triggered, otherwise "".
func (p *BrowserPane) LoadChildren(parentPath string, dirs []string, files []string, wsEntries []p4.WorkspaceEntry, yoursOpen, othersOpen map[string]bool) string {
	node := p.findNode(p.root, parentPath)
	if node == nil {
		return ""
	}
	node.children = nil
	for _, d := range dirs {
		name := d
		if idx := strings.LastIndex(d, "/"); idx >= 0 && idx < len(d)-1 {
			name = d[idx+1:]
		}
		node.children = append(node.children, &browserNode{name: name, path: d, isDir: true})
	}
	if wsEntries != nil {
		for _, e := range wsEntries {
			path := workspaceEntryPath(e)
			name := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
				name = path[idx+1:]
			}
			node.children = append(node.children, &browserNode{
				name:           name,
				path:           path,
				depotPath:      e.DepotPath,
				localPath:      e.LocalPath,
				isDir:          false,
				tracked:        e.Tracked,
				openedByMe:     yoursOpen[e.DepotPath],
				openedByOthers: othersOpen[e.DepotPath],
			})
		}
	} else {
		for _, f := range files {
			name := f
			if idx := strings.LastIndex(f, "/"); idx >= 0 && idx < len(f)-1 {
				name = f[idx+1:]
			}
			node.children = append(node.children, &browserNode{
				name:           name,
				path:           f,
				isDir:          false,
				tracked:        true,
				openedByMe:     yoursOpen[f],
				openedByOthers: othersOpen[f],
			})
		}
	}
	node.loaded = true
	node.expanded = true
	p.rebuild()
	// Auto-expand: if the only child is a directory, return its path for the caller to load.
	if len(node.children) == 1 && node.children[0].isDir {
		return node.children[0].path
	}
	return ""
}

// LoadChildrenFast populates a node's children from a local filesystem scan only.
// All files are marked untracked until ApplyStatus is called with p4 data.
// Returns the path of the sole directory child for auto-expand, or "".
func (p *BrowserPane) LoadChildrenFast(parentPath string, dirs []string, files []p4.WorkspaceEntry) string {
	node := p.findNode(p.root, parentPath)
	if node == nil {
		return ""
	}
	node.children = nil
	for _, d := range dirs {
		name := d
		if idx := strings.LastIndex(d, "/"); idx >= 0 && idx < len(d)-1 {
			name = d[idx+1:]
		}
		node.children = append(node.children, &browserNode{name: name, path: d, isDir: true})
	}
	for _, e := range files {
		path := workspaceEntryPath(e)
		name := path
		if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
			name = path[idx+1:]
		}
		node.children = append(node.children, &browserNode{
			name:      name,
			path:      path,
			depotPath: e.DepotPath,
			localPath: e.LocalPath,
			isDir:     false,
			tracked:   false,
		})
	}
	node.loaded = true
	node.expanded = true
	if status := node.status; status != nil {
		p.ApplyStatus(parentPath, status.haveFiles, status.depotDirs, status.missingFiles, status.yoursOpen, status.othersOpen)
	}
	p.rebuild()
	if len(node.children) == 1 && node.children[0].isDir {
		return node.children[0].path
	}
	return ""
}

// ApplyStatus overlays p4 data onto already-loaded children: sets tracked status,
// openedByMe/Others, adds depot-only dirs, and adds tracked files missing locally.
// If the node was not yet loaded (fast load failed), creates children from p4 data
// and returns the auto-expand path. Otherwise returns "".
func (p *BrowserPane) ApplyStatus(parentPath string, haveFiles, depotDirs []string, missingFiles []p4.WorkspaceEntry, yoursOpen, othersOpen map[string]bool) string {
	node := p.findNode(p.root, parentPath)
	if node == nil {
		return ""
	}
	node.status = &workspaceStatus{haveFiles, depotDirs, missingFiles, yoursOpen, othersOpen}
	haveSet := map[string]bool{}
	for _, f := range haveFiles {
		haveSet[f] = true
	}
	if !node.loaded {
		// Fallback: fast load didn't run — create nodes from p4 data.
		node.children = nil
		for _, d := range depotDirs {
			name := d
			if idx := strings.LastIndex(d, "/"); idx >= 0 && idx < len(d)-1 {
				name = d[idx+1:]
			}
			node.children = append(node.children, &browserNode{name: name, path: d, isDir: true})
		}
		for _, e := range missingFiles {
			path := workspaceEntryPath(e)
			name := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
				name = path[idx+1:]
			}
			node.children = append(node.children, &browserNode{
				name:           name,
				path:           path,
				depotPath:      e.DepotPath,
				localPath:      e.LocalPath,
				isDir:          false,
				tracked:        true,
				openedByMe:     yoursOpen[e.DepotPath],
				openedByOthers: othersOpen[e.DepotPath],
			})
		}
		node.loaded = true
		node.expanded = true
		p.rebuild()
		if len(node.children) == 1 && node.children[0].isDir {
			return node.children[0].path
		}
		return ""
	}
	// Update existing children with p4 data.
	childPaths := map[string]bool{}
	for _, c := range node.children {
		childPaths[c.path] = true
		if !c.isDir {
			path := c.depotPath
			if path == "" {
				path = c.path
			}
			c.tracked = haveSet[path]
			c.openedByMe = yoursOpen[path]
			c.openedByOthers = othersOpen[path]
		}
	}
	// Add depot-only dirs not in local filesystem.
	for _, d := range depotDirs {
		if !childPaths[d] {
			name := d
			if idx := strings.LastIndex(d, "/"); idx >= 0 && idx < len(d)-1 {
				name = d[idx+1:]
			}
			node.children = append(node.children, &browserNode{name: name, path: d, isDir: true})
		}
	}
	// Add tracked files missing from local filesystem.
	for _, e := range missingFiles {
		path := workspaceEntryPath(e)
		if !childPaths[path] {
			name := path
			if idx := strings.LastIndex(path, "/"); idx >= 0 && idx < len(path)-1 {
				name = path[idx+1:]
			}
			node.children = append(node.children, &browserNode{
				name:           name,
				path:           path,
				depotPath:      e.DepotPath,
				localPath:      e.LocalPath,
				isDir:          false,
				tracked:        true,
				openedByMe:     yoursOpen[e.DepotPath],
				openedByOthers: othersOpen[e.DepotPath],
			})
		}
	}
	p.rebuild()
	return ""
}

func workspaceEntryPath(entry p4.WorkspaceEntry) string {
	if entry.ClientPath != "" {
		return entry.ClientPath
	}
	return entry.DepotPath
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
		sort.Slice(node.children, func(i, j int) bool {
			ci, cj := node.children[i], node.children[j]
			if ci.isDir != cj.isDir {
				return ci.isDir
			}
			return strings.ToLower(ci.name) < strings.ToLower(cj.name)
		})
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

// ExpandCurrent expands the dir under the cursor (or loads it if not yet loaded).
// Has no effect if the cursor is on a file.
func (p *BrowserPane) ExpandCurrent() tea.Cmd {
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
		return nil
	}
	if node.expanded {
		return nil
	}
	return p.toggleOrLoad()
}

// CollapseCurrentOrParent collapses the dir under the cursor if it is expanded;
// otherwise moves the cursor to the parent directory and collapses it.
func (p *BrowserPane) CollapseCurrentOrParent() {
	rows := p.filteredRows()
	cur := p.cursor
	if p.filter != "" {
		cur = p.filterCursor
	}
	if len(rows) == 0 || cur >= len(rows) {
		return
	}
	node := rows[cur].node
	// If on an expanded dir, collapse it.
	if node.isDir && node.expanded {
		node.expanded = false
		p.rebuild()
		// Re-find the row since rebuild changed the list.
		for i, r := range p.filteredRows() {
			if r.node == node {
				p.cursor = i
				break
			}
		}
		return
	}
	// Otherwise move to the parent dir and collapse it.
	// Walk backwards to find the first row with a smaller depth.
	targetDepth := rows[cur].depth - 1
	if targetDepth < 0 {
		return
	}
	allRows := p.filteredRows()
	for i := cur - 1; i >= 0; i-- {
		if allRows[i].depth == targetDepth && allRows[i].node.isDir {
			allRows[i].node.expanded = false
			p.rebuild()
			p.cursor = i
			return
		}
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
		var line string
		if i == cur && p.focused {
			line = styleCursor.Width(innerW).Render(p.renderRowPlain(row, innerW))
		} else {
			line = p.renderRow(row, innerW)
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

func truncateName(name string, available int) string {
	if available <= 0 {
		return ""
	}
	runes := []rune(name)
	if len(runes) <= available {
		return name
	}
	if available <= 1 {
		return "…"
	}
	return string(runes[:available-1]) + "…"
}

func (p *BrowserPane) renderRow(row browserRow, maxW int) string {
	indent := strings.Repeat("  ", row.depth)
	prefixW := row.depth*2 + 2 // indent + icon/indicator (always 2 visible chars)
	name := truncateName(row.node.name, maxW-prefixW)
	if row.node.isDir {
		icon := "▶ "
		if row.node.expanded {
			icon = "▼ "
		}
		return styleBrowserDir.Render(indent + icon + name)
	}
	var indicator string
	switch {
	case row.node.openedByMe && row.node.openedByOthers:
		indicator = styleOpenBoth.Render("◐") + " "
	case row.node.openedByMe:
		indicator = styleOpenMe.Render("●") + " "
	case row.node.openedByOthers:
		indicator = styleOpenOther.Render("●") + " "
	default:
		indicator = "  "
	}
	var fileStyle lipgloss.Style
	switch {
	case p.mode == BrowserModeDepot:
		fileStyle = styleBrowserDepotFile
	case !row.node.tracked:
		fileStyle = styleBrowserUntracked
	default:
		fileStyle = styleBrowserWsFile
	}
	return indent + indicator + fileStyle.Render(name)
}

// renderRowPlain renders a row as plain text with no ANSI color codes, for use as
// cursor row content (cursor background style is applied by the caller).
func (p *BrowserPane) renderRowPlain(row browserRow, maxW int) string {
	indent := strings.Repeat("  ", row.depth)
	prefixW := row.depth*2 + 2
	name := truncateName(row.node.name, maxW-prefixW)
	if row.node.isDir {
		icon := "▶ "
		if row.node.expanded {
			icon = "▼ "
		}
		return indent + icon + name
	}
	var indicator string
	switch {
	case row.node.openedByMe && row.node.openedByOthers:
		indicator = "◐ "
	case row.node.openedByMe:
		indicator = "● "
	case row.node.openedByOthers:
		indicator = "● "
	default:
		indicator = "  "
	}
	return indent + indicator + name
}
