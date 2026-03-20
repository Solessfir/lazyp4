package ui

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/solessfir/lazyp4/internal/p4"
	"github.com/solessfir/lazyp4/internal/ui/panes"
)

// activePane tracks which pane currently has focus.
type activePane int

const (
	paneFileList activePane = iota
	paneDiff
	paneLog
	paneResolve
	paneMiniLog
	paneCmdLog
)

// submitModal is the centered overlay for writing a submit description.
type submitModal struct {
	input   textinput.Model
	clID    string          // non-empty = full CL submit
	files   []p4.OpenedFile // non-empty = marked files submit
}

type confirmModal struct {
	files []string // client files to revert
}

func newSubmitModal(clID string, files []p4.OpenedFile) *submitModal {
	ti := textinput.New()
	ti.Placeholder = "Changelist description..."
	ti.Width = 50
	ti.Focus()
	return &submitModal{input: ti, clID: clID, files: files}
}

// --- tea messages ---

type refreshDoneMsg struct {
	cls []p4.Changelist
	err error
}

type diffDoneMsg struct {
	content string
	err     error
}

type logDoneMsg struct {
	entries []p4.FilelogEntry
	err     error
}

type miniLogDoneMsg struct {
	entries []p4.FilelogEntry
	err     error
}

type conflictsDoneMsg struct {
	conflicts []p4.ConflictFile
	err       error
}

type statusMsg struct{ text string }

// opDoneMsg carries a status bar update and an optional command log entry.
type opDoneMsg struct {
	status string
	cmd    string // p4 command run, e.g. "p4 sync"
	result string // short result, e.g. "done" or error text
}

type infoFetchedMsg struct {
	info p4.WorkspaceInfo
	err  error
}

type fetchDoneMsg struct {
	count int
	err   error
}

type tickMsg time.Time

type syncDryDoneMsg struct {
	total int
	err   error
}
type opLineMsg struct{ line string }
type opEndMsg struct{ err error }

// App is the root bubbletea model.
type App struct {
	client        *p4.Client
	fetchInterval time.Duration
	active        activePane

	// operation in progress (sync or submit)
	opRunning bool
	opName    string // "Syncing" or "Submitting"
	opTotal   int
	opDone    int
	opCancel  context.CancelFunc
	opCh      chan string
	statusPane    *panes.StatusPane
	fileList      *panes.FileListPane
	diff          *panes.DiffPane
	log           *panes.LogPane
	miniLog       *panes.LogPane
	resolve       *panes.ResolvePane
	cmdLog        *panes.CmdLogPane

	width  int
	height int

	status       string
	modal        *submitModal
	confirm      *confirmModal
	showHelp     bool
	helpViewport viewport.Model
}

// New creates the root App model.
func New(client *p4.Client, fetchInterval time.Duration) *App {
	hv := viewport.New(54, 20)
	hv.SetContent(helpContent())
	a := &App{
		client:        client,
		fetchInterval: fetchInterval,
		active:        paneFileList,
		statusPane:    panes.NewStatusPane(),
		fileList:      panes.NewFileListPane(),
		diff:          panes.NewDiffPane(),
		log:           panes.NewLogPane("3", "Log"),
		miniLog:       panes.NewLogPane("2", "History"),
		resolve:       panes.NewResolvePane(),
		cmdLog:        panes.NewCmdLogPane(),
		helpViewport:  hv,
	}
	a.updateFocus()
	return a
}

// Init triggers the first data load and workspace info fetch.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.refresh(), a.cmdInfo(), a.cmdFetch()}
	if a.fetchInterval > 0 {
		cmds = append(cmds, tea.Tick(a.fetchInterval, func(t time.Time) tea.Msg { return tickMsg(t) }))
	}
	return tea.Batch(cmds...)
}

// Update is the main message handler.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Confirm modal captures all input when open.
	if a.confirm != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			switch m.String() {
			case "enter":
				files := a.confirm.files
				a.confirm = nil
				return a, a.cmdRevert(files)
			case "esc", "ctrl+c", "n", "N":
				a.confirm = nil
				a.status = "Cancelled"
			}
		}
		return a, nil
	}

	// Modal captures all input when open.
	if a.modal != nil {
		switch m := msg.(type) {
		case tea.KeyMsg:
			switch m.String() {
			case "enter":
				return a, a.execSubmit()
			case "esc", "ctrl+c":
				a.modal = nil
				a.status = "Cancelled"
				return a, nil
			default:
				var cmd tea.Cmd
				a.modal.input, cmd = a.modal.input.Update(msg)
				return a, cmd
			}
		default:
			var cmd tea.Cmd
			a.modal.input, cmd = a.modal.input.Update(msg)
			return a, cmd
		}
	}

	switch m := msg.(type) {

	case tea.WindowSizeMsg:
		a.width = m.Width
		a.height = m.Height
		a.relayout()
		return a, nil

	case tickMsg:
		var cmds []tea.Cmd
		if a.fetchInterval > 0 {
			cmds = append(cmds, tea.Tick(a.fetchInterval, func(t time.Time) tea.Msg { return tickMsg(t) }))
		}
		a.statusPane.SetFetching()
		cmds = append(cmds, a.cmdFetch())
		return a, tea.Batch(cmds...)

	case refreshDoneMsg:
		if m.err != nil {
			a.status = "refresh error: " + m.err.Error()
			a.cmdLog.Add("p4 opened", "error: "+m.err.Error())
		} else {
			a.fileList.SetChangelists(m.cls)
			a.status = fmt.Sprintf("Loaded %d changelists", len(m.cls))
			a.cmdLog.Add("p4 opened", fmt.Sprintf("%d changelists", len(m.cls)))
		}
		return a, nil

	case diffDoneMsg:
		if m.err != nil {
			a.status = "diff error: " + m.err.Error()
		} else {
			a.diff.SetContent(m.content)
		}
		return a, nil

	case logDoneMsg:
		if m.err != nil {
			a.status = "filelog error: " + m.err.Error()
		} else {
			a.log.SetEntries(m.entries)
			a.active = paneLog
			a.updateFocus()
		}
		return a, nil

	case miniLogDoneMsg:
		if m.err == nil {
			a.miniLog.SetEntries(m.entries)
		}
		return a, nil

	case conflictsDoneMsg:
		if m.err != nil {
			a.status = "resolve error: " + m.err.Error()
		} else {
			a.resolve.SetConflicts(m.conflicts)
			a.active = paneResolve
			a.updateFocus()
		}
		return a, nil

	case statusMsg:
		a.status = m.text
		return a, nil

	case opDoneMsg:
		a.status = m.status
		a.cmdLog.Add(m.cmd, m.result)
		return a, nil

	case syncDryDoneMsg:
		if m.err != nil {
			a.opRunning = false
			a.status = "sync dry-run failed: " + m.err.Error()
			return a, nil
		}
		a.opTotal = m.total
		a.opDone = 0
		return a, a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
			err := a.client.SyncStreaming(ctx, ch)
			ch <- opErrLine(err)
			close(ch)
		})

	case opLineMsg:
		a.opDone++
		return a, a.cmdReadOpLine()

	case opEndMsg:
		opName := a.opName
		opDone := a.opDone
		a.opRunning = false
		a.opCancel = nil
		a.opCh = nil
		p4cmd := "p4 " + strings.ToLower(opName)
		var cmds []tea.Cmd
		if m.err != nil && m.err != context.Canceled {
			a.status = opName + " failed: " + m.err.Error()
			a.cmdLog.Add(p4cmd, "error: "+m.err.Error())
		} else if m.err == context.Canceled {
			a.status = opName + " cancelled"
			a.cmdLog.Add(p4cmd, "cancelled")
		} else {
			a.status = fmt.Sprintf("%s complete (%d files)", opName, opDone)
			a.cmdLog.Add(p4cmd, fmt.Sprintf("%d files", opDone))
		}
		if opName == "Submitting" {
			a.fileList.ClearMarks()
		}
		cmds = append(cmds, a.refresh())
		return a, tea.Batch(cmds...)

	case revertDoneMsg:
		n := len(m.files)
		a.status = fmt.Sprintf("Reverted %d file(s)", n)
		basenames := make([]string, n)
		for i, f := range m.files {
			if idx := strings.LastIndexAny(f, "/\\"); idx >= 0 {
				basenames[i] = f[idx+1:]
			} else {
				basenames[i] = f
			}
		}
		a.cmdLog.Add("p4 revert", fmt.Sprintf("%d file(s)", n), basenames...)
		a.fileList.ClearMarks()
		return a, a.refresh()

	case infoFetchedMsg:
		if m.err == nil {
			a.statusPane.SetInfo(m.info)
		}
		return a, nil

	case fetchDoneMsg:
		if m.err != nil {
			a.status = "fetch error: " + m.err.Error()
			a.statusPane.SetPending(0)
			a.cmdLog.Add("p4 sync -n", "error: "+m.err.Error())
		} else {
			a.statusPane.SetPending(m.count)
			a.status = fmt.Sprintf("Fetch: %d file(s) pending", m.count)
			a.cmdLog.Add("p4 sync -n", fmt.Sprintf("↓%d pending", m.count))
		}
		return a, nil

	case tea.MouseMsg:
		if a.showHelp {
			switch m.Button {
			case tea.MouseButtonWheelUp:
				a.helpViewport.LineUp(3)
			case tea.MouseButtonWheelDown:
				a.helpViewport.LineDown(3)
			}
			return a, nil
		}
		if a.active == paneCmdLog {
			cmd := a.cmdLog.Update(m)
			return a, cmd
		}
		if m.Action == tea.MouseActionPress && m.Button == tea.MouseButtonLeft {
			return a.handleClick(m.X, m.Y)
		}
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(m)
	}
	return a, nil
}

func (a *App) handleKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.String() == "?" {
		a.showHelp = !a.showHelp
		if a.showHelp {
			a.helpViewport.GotoTop()
		}
		return a, nil
	}
	if a.showHelp {
		switch m.String() {
		case "j", "down":
			a.helpViewport.LineDown(1)
		case "k", "up":
			a.helpViewport.LineUp(1)
		default:
			a.showHelp = false
		}
		return a, nil
	}

	switch m.String() {
	case "q", "ctrl+c":
		return a, tea.Quit
	case "r":
		a.status = "Refreshing..."
		return a, a.refresh()
	case "t":
		a.fileList.ToggleMode()
		return a, nil
	case "f":
		a.statusPane.SetFetching()
		a.status = "Fetching..."
		return a, a.cmdFetch()
	case "d":
		if a.opRunning {
			a.status = a.opName + " in progress"
			return a, nil
		}
		marked := a.fileList.MarkedFiles()
		if len(marked) > 0 {
			files := make([]string, len(marked))
			for i, f := range marked {
				files[i] = f.ClientFile
			}
			a.confirm = &confirmModal{files: files}
		} else {
			f := a.fileList.SelectedFile()
			if f == nil {
				a.status = "No file selected"
				return a, nil
			}
			a.confirm = &confirmModal{files: []string{f.ClientFile}}
		}
		return a, nil
	case "S":
		if a.opRunning {
			a.status = a.opName + " already in progress"
			return a, nil
		}
		a.status = "Calculating sync..."
		return a, a.cmdSyncDryRun()
	case "s":
		if a.opRunning {
			a.status = a.opName + " in progress"
			return a, nil
		}
		marked := a.fileList.MarkedFiles()
		if len(marked) > 0 {
			a.modal = newSubmitModal("", marked)
		} else {
			clID := a.fileList.SelectedCL()
			if clID == "" {
				a.status = "No changelist selected"
				return a, nil
			}
			a.modal = newSubmitModal(clID, nil)
		}
		return a, textinput.Blink
	case "c":
		if a.opRunning && a.opCancel != nil {
			a.opCancel()
		}
		return a, nil
	case "h", "left":
		a.cycleFocusBackward()
		return a, nil
	case "l", "right":
		a.cycleFocusForward()
		return a, nil
	case "L":
		f := a.fileList.SelectedFile()
		if f == nil {
			a.status = "No file selected"
			return a, nil
		}
		a.status = "Loading filelog..."
		return a, a.cmdFilelog(f.DepotFile)
	case "R":
		a.status = "Checking conflicts..."
		return a, a.cmdResolveList()
	case "m":
		a.status = "Move: not yet implemented"
		return a, nil
	case "1":
		a.active = paneFileList
		a.updateFocus()
		return a, nil
	case "2":
		a.active = paneMiniLog
		a.updateFocus()
		return a, nil
	case "3":
		a.active = a.currentRightPane()
		a.updateFocus()
		return a, nil
	case "4":
		a.active = paneCmdLog
		a.updateFocus()
		return a, nil
	case "tab":
		a.cycleFocus()
		return a, nil
	case "esc":
		a.active = paneFileList
		a.updateFocus()
		return a, nil
	}

	var cmd tea.Cmd
	switch a.active {
	case paneFileList:
		cmd = a.fileList.Update(m)
		f := a.fileList.SelectedFile()
		if f != nil {
			return a, tea.Batch(cmd, a.cmdDiff(f.ClientFile), a.cmdMiniLog(f.DepotFile))
		}
	case paneDiff:
		cmd = a.diff.Update(m)
	case paneLog:
		cmd = a.log.Update(m)
	case paneResolve:
		cmd = a.resolve.Update(m)
	case paneMiniLog:
		cmd = a.miniLog.Update(m)
	case paneCmdLog:
		cmd = a.cmdLog.Update(m)
	}
	return a, cmd
}

func (a *App) execSubmit() tea.Cmd {
	m := a.modal
	a.modal = nil
	desc := strings.TrimSpace(m.input.Value())
	if desc == "" {
		desc = "lazyp4 submit"
	}
	if len(m.files) > 0 {
		return a.cmdSubmitMarkedStart(m.files, desc)
	}
	return a.cmdSubmitStart(m.clID, desc)
}

func (a *App) handleClick(x, y int) (tea.Model, tea.Cmd) {
	leftW := a.width / 3
	contentH := a.height - 1
	bodyH := contentH - panes.CmdLogHeight
	leftH := bodyH - panes.StatusHeight
	fileListH := leftH * 7 / 10
	if fileListH < 4 {
		fileListH = 4
	}

	if y >= bodyH {
		return a, nil
	}

	if x < leftW {
		fileListTop := panes.StatusHeight
		fileListBottom := fileListTop + fileListH
		if y >= fileListTop && y < fileListBottom {
			a.active = paneFileList
			a.updateFocus()
			contentY := y - fileListTop - 1 // -1 for top border
			if contentY >= 0 {
				rowIdx := a.fileList.ScrollOffset() + contentY
				a.fileList.SetCursor(rowIdx)
				if f := a.fileList.SelectedFile(); f != nil {
					return a, tea.Batch(a.cmdDiff(f.ClientFile), a.cmdMiniLog(f.DepotFile))
				}
			}
		}
	} else {
		a.active = a.currentRightPane()
		a.updateFocus()
	}

	return a, nil
}

func (a *App) currentRightPane() activePane {
	switch a.active {
	case paneLog, paneResolve:
		return a.active
	default:
		return paneDiff
	}
}

func (a *App) cycleFocus() {
	switch a.active {
	case paneFileList:
		a.active = a.currentRightPane()
	case paneDiff, paneLog, paneResolve:
		a.active = paneMiniLog
	case paneMiniLog:
		a.active = paneCmdLog
	default:
		a.active = paneFileList
	}
	a.updateFocus()
}

func (a *App) paneOrder() []activePane {
	return []activePane{paneFileList, paneMiniLog, a.currentRightPane(), paneCmdLog}
}

func (a *App) cycleFocusForward() {
	order := a.paneOrder()
	for i, p := range order {
		if p == a.active {
			a.active = order[(i+1)%len(order)]
			a.updateFocus()
			return
		}
	}
	a.active = order[0]
	a.updateFocus()
}

func (a *App) cycleFocusBackward() {
	order := a.paneOrder()
	for i, p := range order {
		if p == a.active {
			a.active = order[(i-1+len(order))%len(order)]
			a.updateFocus()
			return
		}
	}
	a.active = order[len(order)-1]
	a.updateFocus()
}

func (a *App) updateFocus() {
	a.fileList.SetFocused(a.active == paneFileList)
	a.diff.SetFocused(a.active == paneDiff)
	a.log.SetFocused(a.active == paneLog)
	a.resolve.SetFocused(a.active == paneResolve)
	a.miniLog.SetFocused(a.active == paneMiniLog)
	a.cmdLog.SetFocused(a.active == paneCmdLog)
}

func (a *App) relayout() {
	leftW := a.width / 3
	rightW := a.width - leftW
	contentH := a.height - 1

	bodyH := contentH - panes.CmdLogHeight
	if bodyH < 4 {
		bodyH = 4
	}

	a.statusPane.SetWidth(leftW)
	leftH := bodyH - panes.StatusHeight

	fileListH := leftH * 7 / 10
	if fileListH < 4 {
		fileListH = 4
	}
	miniLogH := leftH - fileListH

	a.fileList.SetSize(leftW, fileListH)
	a.miniLog.SetSize(leftW, miniLogH)
	a.diff.SetSize(rightW, bodyH)
	a.log.SetSize(rightW, bodyH)
	a.resolve.SetSize(rightW, bodyH)
	a.cmdLog.SetWidth(a.width)

	helpW := a.width*3/5 - 2 // inner width (subtract border chars)
	helpH := a.height*3/5 - 2
	if helpW < 20 {
		helpW = 20
	}
	if helpH < 5 {
		helpH = 5
	}
	a.helpViewport.Width = helpW
	a.helpViewport.Height = helpH
}

// View renders the full TUI.
func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	leftSide := lipgloss.JoinVertical(lipgloss.Left,
		a.statusPane.View(),
		a.fileList.View(),
		a.miniLog.View(),
	)

	var rightPane string
	switch a.active {
	case paneLog:
		rightPane = a.log.View()
	case paneResolve:
		rightPane = a.resolve.View()
	default:
		rightPane = a.diff.View()
	}

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftSide, rightPane)
	base := lipgloss.JoinVertical(lipgloss.Left, body, a.cmdLog.View(), a.renderHotkeys())

	if a.confirm != nil {
		return overlayCenter(a.renderConfirmModal(), base, a.width, a.height)
	}
	if a.modal != nil {
		return overlayCenter(a.renderModal(), base, a.width, a.height)
	}
	if a.showHelp {
		return overlayCenter(a.renderHelpModal(), base, a.width, a.height)
	}

	return base
}

// --- styles ---

var (
	styleStatus = lipgloss.NewStyle().Foreground(lipgloss.Color("7"))

	styleHotkeys = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	styleHotkeyKey = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	styleModalBox = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("12")).
			Padding(1, 2)

	styleModalTitle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("12")).
			Bold(true)

	styleModalHint = lipgloss.NewStyle().
			Foreground(lipgloss.Color("8"))
)

func (a *App) renderProgressBar() string {
	const barWidth = 20
	var filled int
	if a.opTotal > 0 {
		filled = a.opDone * barWidth / a.opTotal
		if filled > barWidth {
			filled = barWidth
		}
	}
	bar := styleHotkeyKey.Render(strings.Repeat("█", filled)) +
		styleHotkeys.Render(strings.Repeat("░", barWidth-filled))

	var count string
	if a.opTotal == 0 {
		count = styleHotkeys.Render("Calculating...")
	} else {
		count = styleHotkeys.Render(fmt.Sprintf("%d/%d files", a.opDone, a.opTotal))
	}

	cancelHint := styleHotkeys.Render("c - ") + styleHotkeyKey.Render("cancel")
	left := " " + styleHotkeys.Render(a.opName) + "  " + bar + "  " + count
	leftW := lipgloss.Width(left)
	cancelW := lipgloss.Width(cancelHint)
	gap := a.width - leftW - cancelW - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + cancelHint + " "
}

func (a *App) renderHotkeys() string {
	if a.opRunning {
		return a.renderProgressBar()
	}

	type binding struct{ desc, key string }

	var bindings []binding
	if a.fileList.HasFiles() {
		bindings = append(bindings, binding{"Add", "space"})
	}
	bindings = append(bindings,
		binding{"Submit", "s"},
		binding{"Discard", "d"},
		binding{"Fetch", "f"},
		binding{"Sync", "S"},
	)

	var parts []string
	for _, b := range bindings {
		parts = append(parts, styleHotkeys.Render(b.desc+": ")+styleHotkeyKey.Render(b.key))
	}
	parts = append(parts, styleHotkeys.Render("Keybindings: ")+styleHotkeyKey.Render("?"))
	hotkeys := " " + strings.Join(parts, styleHotkeys.Render(" | "))

	hotkeysW := lipgloss.Width(hotkeys)
	statusText := styleStatus.Render(a.status + " ")
	statusW := lipgloss.Width(statusText)
	gap := a.width - hotkeysW - statusW
	if gap < 1 {
		gap = 1
	}

	return hotkeys + strings.Repeat(" ", gap) + statusText
}

func (a *App) renderModal() string {
	m := a.modal
	var title string
	if len(m.files) > 0 {
		title = fmt.Sprintf("Submit %d marked file(s)", len(m.files))
	} else {
		title = fmt.Sprintf("Submit CL %s", m.clID)
	}

	content := styleModalTitle.Render(title) +
		"\n\n" +
		m.input.View() +
		"\n\n" +
		styleModalHint.Render("enter - confirm   esc - cancel")

	return styleModalBox.Render(content)
}

func (a *App) renderConfirmModal() string {
	var desc string
	if len(a.confirm.files) == 1 {
		name := a.confirm.files[0]
		if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
			name = name[idx+1:]
		}
		desc = styleStatus.Render(name)
	} else {
		desc = styleStatus.Render(fmt.Sprintf("%d files", len(a.confirm.files)))
	}
	content := styleModalTitle.Render("Discard changes?") +
		"\n\n" +
		desc +
		"\n\n" +
		styleModalHint.Render("enter - revert   esc - cancel")
	return styleModalBox.Render(content)
}

func (a *App) renderHelpModal() string {
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("12"))
	rendered := style.Width(a.helpViewport.Width).Height(a.helpViewport.Height).Render(a.helpViewport.View())
	totalW := lipgloss.Width(rendered)
	return panes.InjectTitle(rendered, "?", "Keybindings", totalW, true)
}

func helpContent() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	key := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)

	type row struct{ k, desc string }
	rows := []row{
		{"j / k", "Navigate files"},
		{"enter", "Expand / collapse directory"},
		{"space", "Mark / unmark file for submit"},
		{"s", "Submit (opens description form)"},
		{"d", "Discard (revert) selected file"},
		{"h / l", "Cycle panel focus left / right"},
		{"tab", "Cycle panel focus (forward)"},
		{"esc", "Back to file list"},
		{"t", "Toggle tree / flat view"},
		{"f", "Fetch (dry-run sync, shows pending count)"},
		{"S", "Sync"},
		{"c", "Cancel operation (sync / submit)"},
		{"r", "Refresh"},
		{"L", "File log for selected file"},
		{"R", "Show conflicts"},
		{"q", "Quit"},
		{"?", "Close this window"},
	}

	var sb strings.Builder
	for i, r := range rows {
		line := fmt.Sprintf("  %s  %s", key.Render(fmt.Sprintf("%-14s", r.k)), dim.Render(r.desc))
		sb.WriteString(line)
		if i < len(rows)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// --- async commands ---

func (a *App) cmdInfo() tea.Cmd {
	return func() tea.Msg {
		info, err := a.client.Info()
		return infoFetchedMsg{info: info, err: err}
	}
}

func (a *App) cmdFetch() tea.Cmd {
	return func() tea.Msg {
		count, err := a.client.SyncDryRun()
		return fetchDoneMsg{count: count, err: err}
	}
}

func (a *App) refresh() tea.Cmd {
	return func() tea.Msg {
		files, err := a.client.OpenedFiles()
		if err != nil {
			return refreshDoneMsg{err: err}
		}
		cls := p4.GroupByChangelist(files)
		return refreshDoneMsg{cls: cls}
	}
}

func (a *App) cmdDiff(clientFile string) tea.Cmd {
	return func() tea.Msg {
		out, err := a.client.Diff(clientFile)
		return diffDoneMsg{content: out, err: err}
	}
}

func (a *App) cmdFilelog(depotFile string) tea.Cmd {
	return func() tea.Msg {
		entries, err := a.client.Filelog(depotFile, 0)
		return logDoneMsg{entries: entries, err: err}
	}
}

func (a *App) cmdMiniLog(depotFile string) tea.Cmd {
	return func() tea.Msg {
		entries, err := a.client.Filelog(depotFile, 10)
		return miniLogDoneMsg{entries: entries, err: err}
	}
}

func (a *App) cmdResolveList() tea.Cmd {
	return func() tea.Msg {
		conflicts, err := a.client.ResolveList()
		return conflictsDoneMsg{conflicts: conflicts, err: err}
	}
}

func (a *App) cmdRevert(clientFiles []string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.RevertFiles(clientFiles)
		if err != nil {
			return opDoneMsg{"revert failed: " + err.Error(), "p4 revert", "error: " + err.Error()}
		}
		return revertDoneMsg{files: clientFiles}
	}
}

type revertDoneMsg struct {
	files []string
}

func opErrLine(err error) string {
	if err != nil {
		return "\x00" + err.Error()
	}
	return "\x00"
}

func (a *App) cmdSyncDryRun() tea.Cmd {
	a.opRunning = true
	a.opName = "Syncing"
	a.opTotal = 0
	a.opDone = 0
	return func() tea.Msg {
		count, err := a.client.SyncDryRun()
		return syncDryDoneMsg{total: count, err: err}
	}
}

// cmdOpStart launches a generic streaming operation goroutine and returns the first read cmd.
func (a *App) cmdOpStart(fn func(ctx context.Context, ch chan<- string)) tea.Cmd {
	ctx, cancel := context.WithCancel(context.Background())
	a.opCancel = cancel
	ch := make(chan string, 32)
	a.opCh = ch
	go fn(ctx, ch)
	return a.cmdReadOpLine()
}

func (a *App) cmdReadOpLine() tea.Cmd {
	return func() tea.Msg {
		line, ok := <-a.opCh
		if !ok {
			return opEndMsg{}
		}
		if len(line) > 0 && line[0] == '\x00' {
			errStr := line[1:]
			if errStr == context.Canceled.Error() {
				return opEndMsg{err: context.Canceled}
			}
			if errStr != "" {
				return opEndMsg{err: fmt.Errorf("%s", errStr)}
			}
			return opEndMsg{}
		}
		return opLineMsg{line: line}
	}
}

func (a *App) cmdShelve(clID string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.Shelve(clID)
		if err != nil {
			return opDoneMsg{"shelve failed: " + err.Error(), "p4 shelve -c " + clID, "error: " + err.Error()}
		}
		return opDoneMsg{fmt.Sprintf("CL %s shelved", clID), "p4 shelve -c " + clID, "done"}
	}
}

func (a *App) cmdSubmitStart(clID, description string) tea.Cmd {
	a.opRunning = true
	a.opName = "Submitting"
	a.opTotal = 0
	a.opDone = 0
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		err := a.client.SubmitStreaming(ctx, clID, description, ch)
		ch <- opErrLine(err)
		close(ch)
	})
}

func (a *App) cmdSubmitMarkedStart(files []p4.OpenedFile, description string) tea.Cmd {
	a.opRunning = true
	a.opName = "Submitting"
	a.opTotal = len(files)
	a.opDone = 0
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		err := a.client.SubmitMarkedStreaming(ctx, files, description, ch)
		ch <- opErrLine(err)
		close(ch)
	})
}
