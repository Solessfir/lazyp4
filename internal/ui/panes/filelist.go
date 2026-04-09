package panes

import (
	"fmt"
	"strings"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
)

// ViewMode controls how files are displayed.
type ViewMode int

const (
	ViewTree ViewMode = iota
	ViewFlat
)

type rowKind int

const (
	rowKindHeader rowKind = iota
	rowKindDir
	rowKindFile
)

type row struct {
	kind      rowKind
	clIndex   int
	fileIndex int    // rowKindFile only
	dirKey    string // rowKindDir only - used as key in expanded map
	depth     int    // indent level under CL header
	label     string // display name (just the filename or dirname, not full path)
}

// FileListPane displays opened files grouped by changelist.
type FileListPane struct {
	changelists  []p4.Changelist
	cursor       int
	rows         []row
	focused      bool
	width        int
	height       int
	mode         ViewMode
	expanded     map[string]bool // dirKey -> expanded
	marked       map[string]bool // depotFile -> marked for partial submit
	scrollOffset int             // first visible row index, updated during render
	filterMode   bool
	filter       string
	filterCursor int
	filteredIdxs []int // indices into p.rows that match current filter
}

// Styles.
var (
	styleHeader = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("12"))

	styleFileEdit = lipgloss.NewStyle().
			Foreground(lipgloss.Color("3")) // yellow — edit with actual changes

	styleFileCheckedOut = lipgloss.NewStyle() // default — edit with no local changes

	styleFileAdd = lipgloss.NewStyle().
			Foreground(lipgloss.Color("2")) // green

	styleFileDelete = lipgloss.NewStyle().
			Foreground(lipgloss.Color("1")) // red

	styleFileOther = lipgloss.NewStyle().
			Foreground(lipgloss.Color("7"))

	styleDir = lipgloss.NewStyle().
			Foreground(lipgloss.Color("6")) // cyan

	styleMarked = lipgloss.NewStyle().
			Foreground(lipgloss.Color("10")).Bold(true) // bright green

	styleCursor = lipgloss.NewStyle().
			Background(lipgloss.Color("8")).
			Bold(true)

	styleFocusBorder = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("12"))

	styleBlurBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("8"))
)

// NewFileListPane creates an empty pane in tree view mode.
func NewFileListPane() *FileListPane {
	return &FileListPane{
		mode:     ViewTree,
		expanded: map[string]bool{},
		marked:   map[string]bool{},
	}
}

// SetSize updates dimensions.
func (p *FileListPane) SetSize(w, h int) {
	p.width = w
	p.height = h
}

// SetFocused toggles focus highlight.
func (p *FileListPane) SetFocused(f bool) {
	p.focused = f
}

func (p *FileListPane) InFilterMode() bool { return p.filterMode }

func (p *FileListPane) HasFilter() bool { return p.filter != "" }

func (p *FileListPane) ClearFilter() {
	if len(p.filteredIdxs) > 0 && p.filterCursor < len(p.filteredIdxs) {
		p.cursor = p.filteredIdxs[p.filterCursor]
	}
	p.filter = ""
	p.filterMode = false
	p.filterCursor = 0
	p.filteredIdxs = nil
}

// Mode returns the current view mode.
func (p *FileListPane) Mode() ViewMode { return p.mode }

// ToggleMode switches between tree and flat view.
func (p *FileListPane) ToggleMode() {
	if p.mode == ViewTree {
		p.mode = ViewFlat
	} else {
		p.mode = ViewTree
	}
	p.rebuildRows()
	if p.cursor >= len(p.rows) {
		p.cursor = 0
	}
}

// SetChangelists replaces the displayed data and rebuilds rows.
func (p *FileListPane) markedCount() int {
	n := 0
	for _, v := range p.marked {
		if v {
			n++
		}
	}
	return n
}

func (p *FileListPane) Changelists() []p4.Changelist { return p.changelists }

func (p *FileListPane) HasFiles() bool {
	for _, cl := range p.changelists {
		if len(cl.Files) > 0 {
			return true
		}
	}
	return false
}

func (p *FileListPane) SetChangelists(cls []p4.Changelist) {
	p.changelists = cls
	p.rebuildRows()
	if p.cursor >= len(p.rows) {
		p.cursor = 0
	}
}

func (p *FileListPane) rebuildRows() {
	p.rows = nil
	if p.mode == ViewFlat {
		p.rebuildFlat()
	} else {
		p.rebuildTree()
	}
}

func (p *FileListPane) rebuildFlat() {
	for ci, cl := range p.changelists {
		p.rows = append(p.rows, row{kind: rowKindHeader, clIndex: ci})
		for fi, f := range cl.Files {
			p.rows = append(p.rows, row{
				kind:      rowKindFile,
				clIndex:   ci,
				fileIndex: fi,
				label:     f.DepotFile,
			})
		}
	}
}

func (p *FileListPane) rebuildTree() {
	for ci, cl := range p.changelists {
		p.rows = append(p.rows, row{kind: rowKindHeader, clIndex: ci})
		root := buildFileTree(cl.Files)
		p.flattenTree(root, ci, cl.ID, "", 0)
	}
}

// treeNode is used transiently when building the tree.
type treeNode struct {
	name      string
	isDir     bool
	fileIndex int
	children  []*treeNode
}

func buildFileTree(files []p4.OpenedFile) *treeNode {
	root := &treeNode{isDir: true}
	for i, f := range files {
		rel := depotRelPath(f.DepotFile)
		parts := strings.Split(rel, "/")
		insertTreeNode(root, parts, i)
	}
	return root
}

func insertTreeNode(parent *treeNode, parts []string, fileIndex int) {
	if len(parts) == 0 {
		return
	}
	if len(parts) == 1 {
		parent.children = append(parent.children, &treeNode{
			name:      parts[0],
			fileIndex: fileIndex,
		})
		return
	}
	for _, child := range parent.children {
		if child.isDir && child.name == parts[0] {
			insertTreeNode(child, parts[1:], fileIndex)
			return
		}
	}
	dir := &treeNode{name: parts[0], isDir: true}
	parent.children = append(parent.children, dir)
	insertTreeNode(dir, parts[1:], fileIndex)
}

func (p *FileListPane) flattenTree(node *treeNode, clIndex int, clID, parentPath string, depth int) {
	for _, child := range node.children {
		if child.isDir {
			relPath := parentPath + child.name
			dirKey := clID + ":" + relPath
			if _, exists := p.expanded[dirKey]; !exists {
				p.expanded[dirKey] = true // default expanded
			}
			p.rows = append(p.rows, row{
				kind:    rowKindDir,
				clIndex: clIndex,
				dirKey:  dirKey,
				depth:   depth,
				label:   child.name,
			})
			if p.expanded[dirKey] {
				p.flattenTree(child, clIndex, clID, relPath+"/", depth+1)
			}
		} else {
			p.rows = append(p.rows, row{
				kind:      rowKindFile,
				clIndex:   clIndex,
				fileIndex: child.fileIndex,
				depth:     depth,
				label:     child.name,
			})
		}
	}
}

// SelectedFile returns the currently highlighted OpenedFile, or nil.
func (p *FileListPane) SelectedFile() *p4.OpenedFile {
	if len(p.rows) == 0 {
		return nil
	}
	cur := p.activeCursor()
	if cur >= len(p.rows) {
		return nil
	}
	r := p.rows[cur]
	if r.kind != rowKindFile {
		return nil
	}
	return &p.changelists[r.clIndex].Files[r.fileIndex]
}

// MarkedFiles returns all files the user has marked for partial submit.
func (p *FileListPane) MarkedFiles() []p4.OpenedFile {
	var out []p4.OpenedFile
	for ci, cl := range p.changelists {
		for fi := range cl.Files {
			f := p.changelists[ci].Files[fi]
			if p.marked[f.DepotFile] {
				out = append(out, f)
			}
		}
	}
	return out
}

// ClearMarks removes all marks.
func (p *FileListPane) ClearMarks() {
	p.marked = map[string]bool{}
}

// ScrollOffset returns the index of the first visible row (updated each render).
func (p *FileListPane) ScrollOffset() int { return p.scrollOffset }

func (p *FileListPane) JumpTop() { p.SetCursor(0) }
func (p *FileListPane) JumpBottom() { p.SetCursor(len(p.rows) - 1) }

// SetCursor moves the cursor to idx, clamped to valid range.
func (p *FileListPane) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(p.rows) {
		idx = len(p.rows) - 1
	}
	if idx >= 0 {
		p.cursor = idx
	}
}

// SelectedDepotPath returns the depot path for the current row:
// - file: exact depot path
// - dir or CL header: depot prefix with /... wildcard (for filelog)
// Returns "" if nothing is selected.
func (p *FileListPane) SelectedDepotPath() string {
	if len(p.rows) == 0 {
		return ""
	}
	cur := p.activeCursor()
	if cur >= len(p.rows) {
		return ""
	}
	r := p.rows[cur]
	switch r.kind {
	case rowKindFile:
		return p.changelists[r.clIndex].Files[r.fileIndex].DepotFile
	case rowKindDir:
		// dirKey is "clID:rel/path" - extract the depot prefix from any file in that CL
		cl := p.changelists[r.clIndex]
		if len(cl.Files) == 0 {
			return ""
		}
		// build depot base from first file: strip everything after second "/"
		depotFile := cl.Files[0].DepotFile // e.g. //depot/stream/rel/path
		trimmed := strings.TrimPrefix(depotFile, "//")
		idx := strings.Index(trimmed, "/")
		if idx == -1 {
			return ""
		}
		depotBase := "//" + trimmed[:idx+1] // e.g. //depot/
		// dirKey format: "clID:rel/path"
		colonIdx := strings.Index(r.dirKey, ":")
		if colonIdx == -1 {
			return ""
		}
		relDir := r.dirKey[colonIdx+1:]
		return depotBase + relDir + "/..."
	case rowKindHeader:
		cl := p.changelists[r.clIndex]
		if len(cl.Files) == 0 {
			return ""
		}
		depotFile := cl.Files[0].DepotFile
		trimmed := strings.TrimPrefix(depotFile, "//")
		idx := strings.Index(trimmed, "/")
		if idx == -1 {
			return ""
		}
		// go one more level for stream depot: //depot/stream/...
		rest := trimmed[idx+1:]
		idx2 := strings.Index(rest, "/")
		if idx2 == -1 {
			return "//" + trimmed + "/..."
		}
		return "//" + trimmed[:idx+1+idx2] + "/..."
	}
	return ""
}

// SelectedHistoryPath returns the best depot path for loading filelog history:
// - file row: exact depot file path (→ Filelog, shows action)
// - dir row: first file under that directory (→ Filelog, shows action)
// - header row: first file in the CL (→ Filelog, shows action)
// Returns "" if nothing is selected.
func (p *FileListPane) SelectedHistoryPath() string {
	if len(p.rows) == 0 {
		return ""
	}
	cur := p.activeCursor()
	if cur >= len(p.rows) {
		return ""
	}
	r := p.rows[cur]
	switch r.kind {
	case rowKindFile:
		return p.changelists[r.clIndex].Files[r.fileIndex].DepotFile
	case rowKindDir:
		cl := p.changelists[r.clIndex]
		colonIdx := strings.Index(r.dirKey, ":")
		if colonIdx == -1 {
			return ""
		}
		relDir := r.dirKey[colonIdx+1:] + "/"
		for _, f := range cl.Files {
			trimmed := strings.TrimPrefix(f.DepotFile, "//")
			idx := strings.Index(trimmed, "/")
			if idx == -1 {
				continue
			}
			depotBase := "//" + trimmed[:idx+1]
			rel := trimmed[idx+1:]
			if strings.HasPrefix(rel, relDir) {
				return depotBase + rel
			}
		}
		// fallback: first file in CL
		if len(cl.Files) > 0 {
			return cl.Files[0].DepotFile
		}
	case rowKindHeader:
		cl := p.changelists[r.clIndex]
		if len(cl.Files) > 0 {
			return cl.Files[0].DepotFile
		}
	}
	return ""
}

// FilesForCL returns all opened files belonging to the given CL ID.
func (p *FileListPane) FilesForCL(clID string) []p4.OpenedFile {
	for _, cl := range p.changelists {
		if cl.ID == clID {
			return cl.Files
		}
	}
	return nil
}

// IsOnCLHeader returns true when the cursor is on a changelist header row.
func (p *FileListPane) IsOnCLHeader() bool {
	if len(p.rows) == 0 {
		return false
	}
	cur := p.activeCursor()
	if cur >= len(p.rows) {
		return false
	}
	return p.rows[cur].kind == rowKindHeader
}

// SelectedCL returns the CL ID of the row under the cursor.
func (p *FileListPane) SelectedCL() string {
	if len(p.rows) == 0 {
		return ""
	}
	cur := p.activeCursor()
	if cur >= len(p.rows) {
		return ""
	}
	return p.changelists[p.rows[cur].clIndex].ID
}

// Init satisfies tea.Model.
func (p *FileListPane) Init() tea.Cmd { return nil }

// Update handles keyboard navigation within the pane.
func (p *FileListPane) Update(msg tea.Msg) tea.Cmd {
	switch m := msg.(type) {
	case tea.MouseMsg:
		switch m.Button {
		case tea.MouseButtonWheelUp:
			if p.filter != "" {
				if p.filterCursor > 0 {
					p.filterCursor--
				}
			} else if p.cursor > 0 {
				p.cursor--
			}
		case tea.MouseButtonWheelDown:
			if p.filter != "" {
				if p.filterCursor < len(p.filteredIdxs)-1 {
					p.filterCursor++
				}
			} else if p.cursor < len(p.rows)-1 {
				p.cursor++
			}
		}
		return nil
	case tea.KeyMsg:
		if p.filterMode {
			switch m.String() {
			case "esc":
				p.ClearFilter()
				return nil
			case "enter", "/":
				p.filterMode = false
				return nil
			case "j", "down":
				if p.filterCursor < len(p.filteredIdxs)-1 {
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
					_, size := utf8.DecodeLastRuneInString(p.filter)
					p.filter = p.filter[:len(p.filter)-size]
					p.buildFilteredIdxs()
					p.filterCursor = 0
				}
				return nil
			default:
				if len(m.Runes) == 1 {
					p.filter += string(m.Runes)
					p.buildFilteredIdxs()
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
				if p.filterCursor < len(p.filteredIdxs)-1 {
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
		case "tab":
			p.advanceToCLHeader()
		case "enter":
			p.toggleDir()
		case " ":
			p.toggleMark()
		}
	}
	return nil
}

// toggleMark marks or unmarks the file under the cursor for partial submit.
// On a directory row it marks/unmarks all files recursively within that directory.
func (p *FileListPane) toggleMark() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return
	}
	r := p.rows[p.cursor]
	if r.kind == rowKindDir {
		p.toggleMarkDir(r)
		return
	}
	if r.kind == rowKindFile {
		f := p.changelists[r.clIndex].Files[r.fileIndex]
		p.marked[f.DepotFile] = !p.marked[f.DepotFile]
	}
}

// toggleMarkDir marks or unmarks all files under the given directory row.
// If all files in the dir are already marked, it unmarks them; otherwise marks all.
func (p *FileListPane) toggleMarkDir(r row) {
	cl := p.changelists[r.clIndex]
	// dirKey = "clID:relPath" — extract relPath and append "/" to scope the prefix
	prefix := strings.TrimPrefix(r.dirKey, cl.ID+":") + "/"

	allMarked := true
	count := 0
	for _, f := range cl.Files {
		if strings.HasPrefix(depotRelPath(f.DepotFile), prefix) {
			count++
			if !p.marked[f.DepotFile] {
				allMarked = false
			}
		}
	}
	if count == 0 {
		return
	}
	newVal := !allMarked
	for _, f := range cl.Files {
		if strings.HasPrefix(depotRelPath(f.DepotFile), prefix) {
			p.marked[f.DepotFile] = newVal
		}
	}
}

// toggleDir expands or collapses the directory under the cursor.
func (p *FileListPane) toggleDir() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return
	}
	r := p.rows[p.cursor]
	if r.kind != rowKindDir {
		return
	}
	savedKey := r.dirKey
	p.expanded[savedKey] = !p.expanded[savedKey]
	p.rebuildRows()
	for i, row := range p.rows {
		if row.dirKey == savedKey {
			p.cursor = i
			break
		}
	}
}

// ExpandCurrent expands the dir under the cursor. No-op if already expanded or on a file/header.
func (p *FileListPane) ExpandCurrent() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return
	}
	r := p.rows[p.cursor]
	if r.kind != rowKindDir || p.expanded[r.dirKey] {
		return
	}
	savedKey := r.dirKey
	p.expanded[savedKey] = true
	p.rebuildRows()
	for i, row := range p.rows {
		if row.dirKey == savedKey {
			p.cursor = i
			break
		}
	}
}

// CollapseCurrentOrParent collapses the dir under the cursor if expanded;
// otherwise moves to the parent dir (or CL header) and collapses it.
func (p *FileListPane) CollapseCurrentOrParent() {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return
	}
	r := p.rows[p.cursor]
	// On an expanded dir: collapse it.
	if r.kind == rowKindDir && p.expanded[r.dirKey] {
		savedKey := r.dirKey
		p.expanded[savedKey] = false
		p.rebuildRows()
		for i, row := range p.rows {
			if row.dirKey == savedKey {
				p.cursor = i
				break
			}
		}
		return
	}
	// On a file or collapsed dir: move to the parent row (smaller depth or header).
	targetDepth := r.depth - 1
	for i := p.cursor - 1; i >= 0; i-- {
		pr := p.rows[i]
		if pr.kind == rowKindHeader || (pr.kind == rowKindDir && pr.depth == targetDepth) {
			if pr.kind == rowKindDir && p.expanded[pr.dirKey] {
				savedKey := pr.dirKey
				p.expanded[savedKey] = false
				p.rebuildRows()
				for j, row := range p.rows {
					if row.dirKey == savedKey {
						p.cursor = j
						break
					}
				}
			} else {
				p.cursor = i
			}
			return
		}
	}
}

// CollapseAll collapses all expanded dirs and moves cursor to the top.
func (p *FileListPane) CollapseAll() {
	for k := range p.expanded {
		p.expanded[k] = false
	}
	p.rebuildRows()
	p.cursor = 0
}

// ExpandAll expands all dirs.
func (p *FileListPane) ExpandAll() {
	for k := range p.expanded {
		p.expanded[k] = true
	}
	p.rebuildRows()
}

// buildFilteredIdxs populates p.filteredIdxs with indices of rows matching p.filter.
func (p *FileListPane) buildFilteredIdxs() {
	p.filteredIdxs = nil
	if p.filter == "" {
		return
	}
	// first pass: find matching file rows per CL
	matchingCLs := map[int]bool{}
	for _, r := range p.rows {
		if r.kind == rowKindFile && fuzzyMatch(p.filter, r.label) {
			matchingCLs[r.clIndex] = true
		}
	}
	// second pass: include header + matching files
	for i, r := range p.rows {
		switch r.kind {
		case rowKindHeader:
			if matchingCLs[r.clIndex] {
				p.filteredIdxs = append(p.filteredIdxs, i)
			}
		case rowKindFile:
			if fuzzyMatch(p.filter, r.label) {
				p.filteredIdxs = append(p.filteredIdxs, i)
			}
		}
	}
}

// activeCursor returns the effective cursor index into p.rows accounting for filter.
func (p *FileListPane) activeCursor() int {
	if p.filter != "" && len(p.filteredIdxs) > 0 {
		if p.filterCursor < len(p.filteredIdxs) {
			return p.filteredIdxs[p.filterCursor]
		}
	}
	return p.cursor
}

// advanceToCLHeader moves the cursor to the next CL header row.
func (p *FileListPane) advanceToCLHeader() {
	for i := p.cursor + 1; i < len(p.rows); i++ {
		if p.rows[i].kind == rowKindHeader {
			p.cursor = i
			return
		}
	}
	for i := 0; i < p.cursor; i++ {
		if p.rows[i].kind == rowKindHeader {
			p.cursor = i
			return
		}
	}
}

// View renders the pane content.
func (p *FileListPane) View() string {
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
	result := injectTitle(rendered, "2", "Pending", p.width, p.focused)
	if n := p.markedCount(); n > 0 {
		result = injectFooter(result, fmt.Sprintf("*%d", n), p.focused)
	}
	return result
}

func (p *FileListPane) renderLines(innerW, innerH int) string {
	if len(p.rows) == 0 {
		return styleFileOther.Render("No open files")
	}

	cursorW := innerW // full content width for cursor highlight
	innerW -= 2      // right margin so text doesn't touch the border
	if innerW < 1 {
		innerW = 40
	}
	if innerH < 1 {
		innerH = 1
	}

	showFilterBar := p.filterMode || p.filter != ""
	contentH := innerH
	if showFilterBar {
		contentH = innerH - 1
		if contentH < 0 {
			contentH = 0
		}
	}

	// Determine the effective row list and cursor.
	var displayIdxs []int
	var activeCur int
	if p.filter != "" {
		displayIdxs = p.filteredIdxs
		activeCur = p.filterCursor
	} else {
		displayIdxs = make([]int, len(p.rows))
		for i := range p.rows {
			displayIdxs[i] = i
		}
		activeCur = p.cursor
	}

	// Scroll so activeCur is visible.
	start := 0
	if activeCur >= contentH {
		start = activeCur - contentH + 1
	}
	p.scrollOffset = start
	end := start + contentH
	if end > len(displayIdxs) {
		end = len(displayIdxs)
	}

	var sb strings.Builder
	for pos := start; pos < end; pos++ {
		i := displayIdxs[pos]
		r := p.rows[i]
		var line string

		switch r.kind {
		case rowKindHeader:
			cl := p.changelists[r.clIndex]
			label := fmt.Sprintf("CL %s", cl.ID)
			if cl.Description != "" {
				label += "  " + cl.Description
			}
			label += fmt.Sprintf("  (%d files)", len(cl.Files))
			line = styleHeader.Render(label)

		case rowKindDir:
			indent := strings.Repeat("  ", r.depth)
			icon := "▶"
			if p.expanded[r.dirKey] {
				icon = "▼"
			}
			line = styleDir.Render(fmt.Sprintf("%s%s %s/", indent, icon, r.label))

		case rowKindFile:
			indent := strings.Repeat("  ", r.depth)
			f := p.changelists[r.clIndex].Files[r.fileIndex]
			// Color conveys action/change state — no action tags shown.
			// [?] is kept as it signals a required user action (resolve needed).
			actionTag := ""
			if f.NeedsResolve {
				actionTag = "[?] "
			}
			name := r.label
			if p.mode == ViewFlat {
				if idx := strings.LastIndex(f.DepotFile, "/"); idx >= 0 {
					name = f.DepotFile[idx+1:]
				} else {
					name = f.DepotFile
				}
			}
			marker := "  "
			if p.marked[f.DepotFile] {
				marker = "* "
			}
			raw := fmt.Sprintf("%s%s%s%s", indent, marker, actionTag, name)
			var fileStyle lipgloss.Style
			switch f.Action {
			case p4.ActionEdit:
				if f.HasChanges {
					fileStyle = styleFileEdit // yellow — has local changes
				} else {
					fileStyle = styleFileCheckedOut // default — checked out, unchanged
				}
			case p4.ActionAdd:
				fileStyle = styleFileAdd
			case p4.ActionDelete:
				fileStyle = styleFileDelete
			default:
				fileStyle = styleFileOther
			}
			if p.marked[f.DepotFile] {
				line = styleMarked.Render(raw)
			} else {
				line = fileStyle.Render(raw)
			}
		}

		if pos == activeCur && p.focused {
			line = styleCursor.Width(cursorW).Render(line)
		}
		sb.WriteString(line)
		if pos < end-1 {
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

// depotRelPath strips "//depot-name/" from a depot path.
func depotRelPath(depotFile string) string {
	trimmed := strings.TrimPrefix(depotFile, "//")
	idx := strings.Index(trimmed, "/")
	if idx == -1 {
		return trimmed
	}
	return trimmed[idx+1:]
}

// shortName truncates path to maxLen chars, prefixing with "...".
func shortName(path string, maxLen int) string {
	if maxLen <= 0 {
		return path
	}
	if len(path) <= maxLen {
		return path
	}
	return "..." + path[len(path)-maxLen+3:]
}
