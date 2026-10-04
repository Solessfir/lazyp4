package ui

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/solessfir/lazyp4/internal/p4"
	"github.com/solessfir/lazyp4/internal/ui/panes"
)

// activePane tracks which pane currently has focus.
type activePane int

const (
	paneBrowser activePane = iota
	paneFileList
	paneStreams
	paneShelved
	paneDiff
	paneLog
	paneResolve
	paneCmdLog
)

// submitModal is the centered overlay for writing a submit description.
type submitModal struct {
	input textinput.Model
	clID  string          // non-empty = full CL submit
	files []p4.OpenedFile // non-empty = marked files submit
}

type confirmKind int

const (
	confirmKindRevert confirmKind = iota
	confirmKindDeleteShelf
	confirmKindResolve
)

type confirmModal struct {
	kind          confirmKind
	cursor        int
	files         []string // client paths to revert
	localToDelete []string // local paths to delete after revert (ActionAdd files only)
	clID          string   // changelist scope for revert or shelf deletion
	resolveFlags  []string
}

// shelveDescModal is shown when shelving files from the default CL so the user can name the new CL.
type shelveDescModal struct {
	input    textinput.Model
	files    []p4.OpenedFile // files to move to new CL, then shelve+revert
	noRevert bool
}

// moveCLModal is shown when the user wants to move files to a different CL.
type moveCLModal struct {
	input textinput.Model
	files []p4.OpenedFile
}

// checkoutModal is shown when the user wants to sync the workspace to a specific CL.
type checkoutModal struct {
	cl       string // target CL number
	stream   string // stream root path
	hasFiles bool   // true = open files exist, show shelve prompt first
}

// streamSwitchModal tracks an in-progress p4 switch invocation.
type streamSwitchModal struct {
	stream    string // target stream path
	switching bool   // p4 switch in flight
}

// integrateModal is shown when the user presses `i` (stream or classic depot).
// For streams it shows pull/push options relative to the current stream's parent;
// for classic depots it collects source and target paths.
type integrateModal struct {
	sourceStream string
	parentStream string // stream depots: parent of current stream (for display)
	isClassic    bool   // true = classic depot, show path inputs
	step         int    // classic only: 0=source input, 1=target input
	sourceInput  textinput.Model
	targetInput  textinput.Model
}

func newIntegrateModalStream(source, parent string) *integrateModal {
	return &integrateModal{sourceStream: source, parentStream: parent}
}

func newIntegrateModalClassic() *integrateModal {
	src := textinput.New()
	src.Placeholder = "Source path, e.g. //depot/main/..."
	src.Width = 50
	src.Focus()
	dst := textinput.New()
	dst.Placeholder = "Target path, e.g. //depot/dev/..."
	dst.Width = 50
	return &integrateModal{isClassic: true, sourceInput: src, targetInput: dst}
}

// authModal is shown when the p4 ticket has expired and a password is needed.
type authModal struct {
	input textinput.Model
}

func newAuthModal() *authModal {
	ti := textinput.New()
	ti.Placeholder = "Password..."
	ti.EchoMode = textinput.EchoPassword
	ti.Width = 40
	ti.Focus()
	return &authModal{input: ti}
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
	request uint64
	cls     []p4.Changelist
	err     error
}

type diffDoneMsg struct {
	request uint64
	content string
	err     error
}

type logDoneMsg struct {
	request uint64
	entries []p4.FilelogEntry
	err     error
}

type browserLoadedMsg struct {
	epoch      uint64
	parentPath string
	dirs       []string
	files      []string // depot mode only
	othersOpen map[string]bool
	err        error
}

type browserFastMsg struct {
	epoch      uint64
	parentPath string
	dirs       []string
	files      []p4.WorkspaceEntry
	err        error
}

type browserStatusMsg struct {
	epoch        uint64
	parentPath   string
	haveFiles    []string
	depotDirs    []string
	missingFiles []p4.WorkspaceEntry
	othersOpen   map[string]bool
	err          error
}

type browserSearchDoneMsg struct {
	epoch uint64
	files []string
	err   error
}

type conflictsDoneMsg struct {
	request   uint64
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
	request uint64
	info    p4.WorkspaceInfo
	err     error
}

type currentCLFetchedMsg struct {
	request uint64
	cl      string
}

type streamsFetchedMsg struct {
	request uint64
	streams []p4.StreamInfo
	err     error
}

type fetchDoneMsg struct {
	request uint64
	count   int
	err     error
}

type tickMsg time.Time

type shelveDoneMsg struct {
	id    uint64
	count int
	clID  string // non-empty when a whole CL was shelved
	err   error
}

type shelvedDoneMsg struct {
	request uint64
	cls     []p4.ShelvedCL
	err     error
}

type unshelveDeleteDoneMsg struct {
	clID string
	err  error
}

type deleteShelfDoneMsg struct {
	clID string
	err  error
}

type forceSyncDoneMsg struct {
	path string
	err  error
}

type fileActionDoneMsg struct {
	op            string
	path          string
	err           error
	readOnlyCount int
	readOnlyErr   error
}

type openFileDoneMsg struct {
	err error
}

type moveDoneMsg struct {
	count   int
	clID    string
	err     error
	userErr bool // true when the error is a user input problem (no p4 command was run)
}

type syncToCLDoneMsg struct {
	id  uint64
	cl  string
	err error
}

type streamSwitchedMsg struct {
	stream string
	err    error
}

type integrateDoneMsg struct {
	op  string // "merge", "copy", or "integrate"
	src string
	err error
}

type opLineMsg struct {
	id   uint64
	line string
}

type browserNavigateMsg struct {
	epoch   uint64
	request uint64
	path    string
	err     error
}
type opEndMsg struct {
	id  uint64
	err error
}

type resolveDoneMsg struct {
	id  uint64
	err error
}

type authRequiredMsg struct{}
type authDoneMsg struct{ err error }

// App is the root bubbletea model.
type App struct {
	client           *p4.Client
	fetchInterval    time.Duration
	linuxFileManager string // empty = auto-detect
	active           activePane
	activity         activityState

	// operation in progress (sync or submit)
	opRunning   bool
	opName      string // "Syncing" or "Submitting"
	opTotal     int
	opDone      int
	opCancel    context.CancelFunc
	opCh        chan string
	opID        uint64
	statusPane  *panes.StatusPane
	browserPane *panes.BrowserPane
	fileList    *panes.FileListPane
	streamsPane *panes.StreamsPane
	shelvedPane *panes.ShelvedPane
	diff        *panes.DiffPane
	log         *panes.LogPane
	resolve     *panes.ResolvePane
	cmdLog      *panes.CmdLogPane

	streams           []p4.StreamInfo
	showStreams       bool   // true when stream depot with >1 stream
	isStreamDepot     bool   // false = classic depot (no streams)
	browserNavTarget  string // pending nav-to path after filter clear
	browserEpoch      uint64
	browserNavRequest uint64
	historyMode       bool   // true = show History pane at bottom-right, false = Diff
	logPath           string // last path loaded into the history pane
	logMax            int    // last max passed to cmdFilelogMax (0 = unlimited)
	diffRequest       uint64
	refreshRequest    uint64
	infoRequest       uint64
	currentCLRequest  uint64
	streamsRequest    uint64
	fetchRequest      uint64
	shelvedRequest    uint64
	logRequest        uint64
	resolveRequest    uint64
	resolvePath       string
	syncPath          string // depot path used for the current/last sync
	pinnedCL          string // non-empty when synced to a specific CL instead of HEAD
	offlineMode       bool   // true = p4 server unreachable

	width  int
	height int

	status         string
	modal          *submitModal
	shelveModal    *shelveDescModal
	moveModal      *moveCLModal
	confirm        *confirmModal
	checkout       *checkoutModal
	streamSwitch   *streamSwitchModal
	integrateModal *integrateModal
	authModal      *authModal
	showHelp       bool
	selectMode     bool // mouse disabled so terminal can select text
	helpViewport   viewport.Model
	helpFilter     textinput.Model
}

// New creates the root App model.
func New(client *p4.Client, fetchInterval time.Duration, linuxFileManager string, pendingTreeView bool) *App {
	hv := viewport.New(54, 20)
	filter := textinput.New()
	filter.Prompt = "/ "
	filter.Placeholder = "Filter descriptions (@keys)"
	fl := panes.NewFileListPane()
	if !pendingTreeView {
		fl.ToggleMode()
	}
	a := &App{
		client:           client,
		fetchInterval:    fetchInterval,
		linuxFileManager: linuxFileManager,
		active:           paneBrowser,
		activity:         newActivityState(),
		statusPane:       panes.NewStatusPane(),
		browserPane:      panes.NewBrowserPane(),
		fileList:         fl,
		streamsPane:      panes.NewStreamsPane(),
		shelvedPane:      panes.NewShelvedPane(),
		diff:             panes.NewDiffPane(),
		log:              panes.NewLogPane("5", "History"),
		resolve:          panes.NewResolvePane(),
		cmdLog:           panes.NewCmdLogPane(),
		helpViewport:     hv,
		helpFilter:       filter,
	}
	a.historyMode = true
	a.updateFocus()
	return a
}

// Init triggers the first data load and workspace info fetch.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.refresh(), a.cmdInfo()}
	cmds = append(cmds, a.cmdActivityTick())
	if a.fetchInterval > 0 {
		cmds = append(cmds, tea.Tick(a.fetchInterval, func(t time.Time) tea.Msg { return tickMsg(t) }))
	}
	return tea.Batch(cmds...)
}

// Update is the main message handler.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	msg, activityCmd, handled := a.updateActivity(msg)
	if handled {
		return a, activityCmd
	}
	model, cmd := a.update(msg)
	return model, tea.Batch(cmd, a.cmdActivityTick())
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	if key, ok := msg.(tea.KeyMsg); ok && key.Type == tea.KeyRunes && !key.Paste && len(key.Runes) > 1 {
		var cmds []tea.Cmd
		for _, r := range key.Runes {
			single := key
			single.Runes = []rune{r}
			_, cmd := a.Update(single)
			cmds = append(cmds, cmd)
		}
		return a, tea.Batch(cmds...)
	}
	if _, mouse := msg.(tea.MouseMsg); mouse && (a.authModal != nil || a.integrateModal != nil || a.streamSwitch != nil || a.checkout != nil || a.confirm != nil || a.moveModal != nil || a.shelveModal != nil || a.modal != nil) {
		return a, nil
	}
	// Auth modal captures all key input when open.
	if a.authModal != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			return a.handleAuthKey(m)
		}
	}
	// Integrate modal captures all key input when open.
	if a.integrateModal != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			return a.handleIntegrateKey(m)
		}
	}

	// Stream-switch modal captures key input when open; async result messages
	// must still fall through to the main switch so they are not dropped.
	if a.streamSwitch != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			return a.handleStreamSwitchKey(m)
		}
	}

	_, keyInput := msg.(tea.KeyMsg)
	// Modals consume keys; asynchronous results still reach the dispatcher.
	if a.checkout != nil && keyInput {
		if m, ok := msg.(tea.KeyMsg); ok {
			co := a.checkout
			switch m.String() {
			case "esc", "ctrl+c", "n", "N", "h":
				a.checkout = nil
				a.status = "Cancelled"
			case "enter", "y", "Y", "l":
				if a.opRunning {
					a.status = a.opName + " in progress"
					return a, nil
				}
				if co.hasFiles {
					return a, a.cmdShelveForCheckout(co)
				}
				a.checkout = nil
				return a, a.cmdSyncToCL(co.stream, co.cl)
			}
		}
		return a, nil
	}

	if a.confirm != nil && keyInput {
		if m, ok := msg.(tea.KeyMsg); ok {
			if a.confirm.kind == confirmKindRevert {
				return a.handleDiscardKey(m)
			}
			if a.opRunning {
				switch m.String() {
				case "enter", "y", "Y", "l", "d":
					a.status = a.opName + " in progress"
					return a, nil
				}
			}
			switch m.String() {
			case "enter", "y", "Y", "l":
				c := a.confirm
				a.confirm = nil
				switch c.kind {
				case confirmKindDeleteShelf:
					return a, a.cmdDeleteShelf(c.clID)
				case confirmKindResolve:
					return a, a.cmdAutoResolve(c.files, c.resolveFlags)
				}
			case "esc", "ctrl+c", "n", "N", "h":
				a.confirm = nil
				a.status = "Cancelled"
			}
		}
		return a, nil
	}

	if a.moveModal != nil && keyInput {
		switch m := msg.(type) {
		case tea.KeyMsg:
			switch m.String() {
			case "enter":
				return a, a.execMoveToCL()
			case "esc", "ctrl+c":
				a.moveModal = nil
				a.status = "Cancelled"
				return a, nil
			default:
				var cmd tea.Cmd
				a.moveModal.input, cmd = a.moveModal.input.Update(msg)
				return a, cmd
			}
		default:
			var cmd tea.Cmd
			a.moveModal.input, cmd = a.moveModal.input.Update(msg)
			return a, cmd
		}
	}

	if a.shelveModal != nil && keyInput {
		switch m := msg.(type) {
		case tea.KeyMsg:
			switch m.String() {
			case "enter":
				return a, a.execShelveWithDesc()
			case "esc", "ctrl+c":
				a.shelveModal = nil
				a.status = "Cancelled"
				return a, nil
			default:
				var cmd tea.Cmd
				a.shelveModal.input, cmd = a.shelveModal.input.Update(msg)
				return a, cmd
			}
		default:
			var cmd tea.Cmd
			a.shelveModal.input, cmd = a.shelveModal.input.Update(msg)
			return a, cmd
		}
	}

	if a.modal != nil && keyInput {
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
		if m.request != a.refreshRequest {
			return a, nil
		}
		if m.err != nil {
			if p4.IsAuthError(m.err) {
				return a.Update(authRequiredMsg{})
			}
			if p4.IsConnectionError(m.err) {
				a.setOffline(true)
			} else {
				a.status = "refresh error: " + m.err.Error()
			}
			a.cmdLog.Add("p4 opened", "error: "+m.err.Error())
		} else {
			a.setOffline(false)
			a.fileList.SetChangelists(m.cls)
			yoursOpen := map[string]bool{}
			for _, cl := range m.cls {
				for _, f := range cl.Files {
					yoursOpen[f.DepotFile] = true
				}
			}
			a.browserPane.RefreshYoursOpen(yoursOpen)
			a.status = fmt.Sprintf("Loaded %d changelists", len(m.cls))
			a.cmdLog.Add("p4 opened", fmt.Sprintf("%d changelists", len(m.cls)))
			a.relayout() // pending height changes
			if a.active == paneFileList {
				cmd := a.cmdSelectionDetails()
				if a.fileList.HasFiles() || cmd != nil {
					return a, cmd
				}
			}
			// No pending files and pending pane is focused (or history mode on):
			// show full stream history so the user has something useful to look at.
			if (a.active == paneFileList || a.historyMode) && !a.fileList.HasFiles() {
				a.historyMode = true
				if root := a.browserPane.RootPath(); root != "" {
					return a, a.cmdFilelogMax(depotWildcard(root), 100)
				}
			}
		}
		return a, nil

	case diffDoneMsg:
		if m.request != a.diffRequest {
			return a, nil
		}
		if m.err != nil {
			a.status = "diff error: " + m.err.Error()
		} else {
			a.diff.SetContent(m.content)
		}
		return a, nil

	case logDoneMsg:
		if m.request != a.logRequest {
			return a, nil
		}
		if m.err != nil {
			a.status = "filelog error: " + m.err.Error()
		} else {
			a.log.SetEntries(m.entries)
		}
		return a, nil

	case conflictsDoneMsg:
		if m.request != a.resolveRequest {
			return a, nil
		}
		if m.err != nil {
			a.status = "resolve error: " + m.err.Error()
		} else {
			a.resolve.SetConflicts(m.conflicts)
			if len(m.conflicts) == 0 {
				a.status = "all conflicts resolved"
				a.active = paneFileList
			} else {
				a.status = ""
				a.active = paneResolve
			}
			a.updateFocus()
		}
		return a, a.refresh()

	case panes.ResolveFinishedMsg:
		if m.Err != nil {
			a.status = "merge tool error: " + m.Err.Error()
		}
		return a, a.cmdResolveList(a.resolvePath)

	case panes.ResolveInteractiveMsg:
		if a.opRunning {
			return a, nil
		}
		cmd, err := a.client.ResolveCommand(m.File)
		if err != nil {
			a.status = "resolve failed: " + err.Error()
			return a, nil
		}
		a.opRunning = true
		a.opName = "Resolving"
		a.opID++
		id := a.opID
		return a, tea.ExecProcess(cmd, func(err error) tea.Msg { return resolveDoneMsg{id: id, err: err} })

	case panes.ResolveAutoMsg:
		if a.opRunning || len(m.Files) == 0 {
			return a, nil
		}
		for _, flag := range m.Flags {
			if flag == "-at" || flag == "-ay" {
				a.confirm = &confirmModal{kind: confirmKindResolve, files: m.Files, resolveFlags: m.Flags}
				return a, nil
			}
		}
		return a, a.cmdAutoResolve(m.Files, m.Flags)

	case resolveDoneMsg:
		if m.id != a.opID {
			return a, nil
		}
		a.opRunning = false
		if m.err != nil {
			a.status = "resolve failed: " + m.err.Error()
			a.cmdLog.Add("p4 resolve", "error: "+m.err.Error())
		} else {
			a.cmdLog.Add("p4 resolve", "done")
		}
		return a, a.cmdResolveList(a.resolvePath)

	case statusMsg:
		a.status = m.text
		return a, nil

	case opDoneMsg:
		a.status = m.status
		a.cmdLog.Add(m.cmd, m.result)
		return a, nil

	case submitReadyMsg:
		if m.id != a.opID {
			return a, nil
		}
		if m.ctx != nil {
			if err := m.ctx.Err(); err != nil {
				m.err = err
			}
		}
		if a.opCancel != nil {
			a.opCancel()
			a.opCancel = nil
		}
		if m.err != nil {
			a.opRunning = false
			if m.err == context.Canceled {
				a.status = "Submit preparation cancelled"
				a.cmdLog.Add("p4 submit", "cancelled")
			} else {
				a.status = "submit preparation failed: " + m.err.Error()
				a.cmdLog.Add("p4 submit", "error: "+m.err.Error())
			}
			return a, nil
		}
		if len(m.files) == 0 {
			a.opRunning = false
			a.status = "Nothing to submit — all files were unchanged"
			return a, nil
		}
		return a, a.cmdSubmitMarkedStart(m.files, m.description)

	case opLineMsg:
		if m.id != a.opID {
			return a, nil
		}
		fields := strings.Fields(m.line)
		if len(fields) > 0 && (strings.HasPrefix(fields[0], "//") || len(fields) > 1 && strings.HasPrefix(fields[1], "//")) {
			a.opDone++
		}
		return a, a.cmdReadOpLine()

	case opEndMsg:
		if m.id != a.opID {
			return a, nil
		}
		opName := a.opName
		opDone := a.opDone
		opTotal := a.opTotal
		a.opRunning = false
		if a.opCancel != nil {
			a.opCancel()
			a.opCancel = nil
		}
		a.opCh = nil
		p4cmd := "p4 submit"
		if opName == "Syncing" {
			p4cmd = "p4 sync"
			if a.syncPath != "" {
				p4cmd += " " + a.syncPath
			}
		}
		var cmds []tea.Cmd
		if m.err != nil && m.err != context.Canceled {
			a.status = opName + " failed: " + m.err.Error()
			a.cmdLog.Add(p4cmd, "error: "+m.err.Error())
		} else if m.err == context.Canceled {
			a.status = opName + " cancelled"
			a.cmdLog.Add(p4cmd, "cancelled")
		} else {
			fileCount := opDone
			if opName == "Submitting" && fileCount == 0 && opTotal > 0 {
				fileCount = opTotal
			}
			if opName == "Syncing" && a.syncPath != "" {
				a.status = fmt.Sprintf("Synced %s (%d files)", a.syncPath, fileCount)
			} else {
				a.status = fmt.Sprintf("%s complete (%d files)", opName, fileCount)
			}
			if opName == "Syncing" {
				a.pinnedCL = ""
			}
			a.cmdLog.Add(p4cmd, fmt.Sprintf("%d files", fileCount))
		}
		if opName == "Submitting" && m.err == nil {
			a.fileList.ClearMarks()
			cmds = append(cmds, a.cmdFetch())
		}
		cmds = append(cmds, a.refresh())
		if opName == "Syncing" || opName == "Submitting" {
			cmds = append(cmds, a.cmdFetchCurrentCL())
		}
		if opName == "Syncing" {
			a.statusPane.SetFetching()
			cmds = append(cmds, a.cmdFetch())
			if a.logPath != "" {
				cmds = append(cmds, a.cmdFilelogMax(a.logPath, a.logMax))
			}
		}
		return a, tea.Batch(cmds...)

	case revertCheckMsg:
		if m.id != a.opID || m.infoRequest != a.infoRequest || a.opRunning || a.streamSwitch != nil {
			return a, nil
		}
		if m.hasChanges {
			a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{m.clientFile}, clID: m.clID}
		} else {
			return a, a.cmdRevert([]string{m.clientFile}, m.clID)
		}
		return a, nil

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

	case shelveDoneMsg:
		if m.id != a.opID {
			return a, nil
		}
		a.opRunning = false
		if m.err != nil {
			a.status = "shelve failed: " + m.err.Error()
			a.cmdLog.Add("p4 shelve", "error: "+m.err.Error())
		} else if m.clID != "" {
			a.status = fmt.Sprintf("Shelved CL %s", m.clID)
			a.cmdLog.Add("p4 shelve", fmt.Sprintf("CL %s", m.clID))
		} else {
			a.status = fmt.Sprintf("Shelved %d file(s)", m.count)
			a.cmdLog.Add("p4 shelve", fmt.Sprintf("%d files", m.count))
		}
		a.fileList.ClearMarks()
		return a, tea.Batch(a.refresh(), a.cmdLoadShelved())

	case shelvedDoneMsg:
		if m.request != a.shelvedRequest {
			return a, nil
		}
		if m.err == nil {
			a.shelvedPane.SetCLs(m.cls)
			a.relayout() // update pending height too
		}
		return a, nil

	case unshelveDeleteDoneMsg:
		if m.err != nil {
			a.status = "unshelve failed: " + m.err.Error()
			a.cmdLog.Add("p4 unshelve", "error: "+m.err.Error())
		} else {
			a.status = fmt.Sprintf("CL %s unshelved", m.clID)
			a.cmdLog.Add("p4 unshelve", fmt.Sprintf("CL %s", m.clID))
		}
		return a, tea.Batch(a.refresh(), a.cmdLoadShelved())

	case deleteShelfDoneMsg:
		if m.err != nil {
			a.status = "delete shelf failed: " + m.err.Error()
			a.cmdLog.Add("p4 shelve -d", "error: "+m.err.Error())
		} else {
			a.status = fmt.Sprintf("Shelf %s deleted", m.clID)
			a.cmdLog.Add("p4 shelve -d", fmt.Sprintf("CL %s", m.clID))
		}
		return a, a.cmdLoadShelved()

	case currentCLFetchedMsg:
		if m.request != a.currentCLRequest {
			return a, nil
		}
		a.log.SetCurrentCL(m.cl)
		return a, nil

	case infoFetchedMsg:
		if m.request != a.infoRequest {
			return a, nil
		}
		if p4.IsAuthError(m.err) {
			return a.Update(authRequiredMsg{})
		}
		if m.err != nil && p4.IsConnectionError(m.err) {
			a.setOffline(true)
			return a, nil
		}
		if m.err == nil {
			workspace := a.client.Workspace
			if workspace == "" {
				workspace = m.info.Client
			}
			rootsChanged := a.browserPane.RootPath() == "" || a.client.Workspace != workspace || a.client.Root != m.info.Root || a.client.Stream != m.info.Stream
			a.client.Root = m.info.Root
			a.client.Stream = m.info.Stream
			// Fill in any connection fields that p4 resolved itself (e.g. via
			// P4CONFIG or environment) but were absent from our config.
			if a.client.Workspace == "" && m.info.Client != "" {
				a.client.Workspace = m.info.Client
			}
			if a.client.User == "" && m.info.User != "" {
				a.client.User = m.info.User
			}
			if a.client.Port == "" && m.info.ServerAddr != "" {
				a.client.Port = m.info.ServerAddr
			}
			a.statusPane.SetInfo(m.info)
			if rootsChanged {
				a.streamsPane.SetPending(-1)
			}
			cmds := []tea.Cmd{a.cmdFetchCurrentCL(), a.cmdFetch(), a.cmdLoadShelved()}
			a.isStreamDepot = m.info.Stream != ""
			if a.isStreamDepot {
				cmds = append(cmds, a.cmdStreams(m.info.Stream))
			}
			if !rootsChanged {
				if len(a.browserPane.LoadedDirPaths()) == 0 {
					root := a.browserPane.RootPath()
					cmds = append(cmds, a.cmdBrowserLoad(root, a.browserPane.Mode()))
					if a.isStreamDepot || a.browserPane.Mode() == panes.BrowserModeWorkspace {
						cmds = append(cmds, a.cmdBrowserSearch(root, a.browserPane.Mode()))
					}
				}
				return a, tea.Batch(cmds...)
			}
			a.browserEpoch++
			a.browserNavTarget = ""
			depotRoot := m.info.Stream
			if depotRoot == "" {
				depotRoot = "//"
			}
			a.browserPane.SetRoots(depotRoot, "//"+a.client.Workspace)
			root := a.browserPane.RootPath()
			cmds = append(cmds, a.cmdBrowserLoad(root, a.browserPane.Mode()))
			// Skip indexing entire classic depots, which could be enormous.
			if a.isStreamDepot || a.browserPane.Mode() == panes.BrowserModeWorkspace {
				cmds = append(cmds, a.cmdBrowserSearch(root, a.browserPane.Mode()))
			}
			if a.active != paneFileList || !a.fileList.HasFiles() {
				a.diffRequest++
				a.diff.SetContent("")
				if a.historyMode {
					cmds = append(cmds, a.cmdFilelogMax(depotWildcard(depotRoot), 100))
				} else {
					a.logRequest++
					a.logPath = ""
					a.log.SetEntries(nil)
				}
			}
			if len(cmds) > 0 {
				return a, tea.Batch(cmds...)
			}
		}
		return a, nil

	case streamsFetchedMsg:
		if m.request != a.streamsRequest {
			return a, nil
		}
		if m.err == nil && len(m.streams) > 1 {
			a.streams = m.streams
			a.showStreams = true
			a.streamsPane.SetStreams(m.streams, a.statusPane.CurrentStream())
			a.streamsPane.SetPending(a.statusPane.Pending())
			a.relayout()
		}
		return a, nil

	case fetchDoneMsg:
		if m.request != a.fetchRequest {
			return a, nil
		}
		if m.err != nil {
			if p4.IsConnectionError(m.err) {
				a.setOffline(true)
			} else {
				a.status = "fetch error: " + m.err.Error()
				a.statusPane.SetPending(-1)
			}
			a.streamsPane.SetPending(-1)
			a.cmdLog.Add("p4 changes", "error: "+m.err.Error())
		} else {
			a.setOffline(false)
			a.statusPane.SetPending(m.count)
			a.streamsPane.SetPending(m.count)
			if m.count == 0 {
				a.status = "No newer CLs"
				a.cmdLog.Add("p4 changes", "no newer CLs")
			} else {
				a.status = fmt.Sprintf("Fetch: %d CL(s) behind", m.count)
				a.cmdLog.Add("p4 changes", fmt.Sprintf("↓%d CLs behind", m.count))
			}
		}
		return a, nil

	case panes.BrowserNeedsLoadMsg:
		return a, a.cmdBrowserLoad(m.Path, m.Mode)

	case browserLoadedMsg:
		if m.epoch != a.browserEpoch {
			return a, nil
		}
		if m.err != nil {
			a.status = "browser load failed: " + m.err.Error()
			return a, nil
		}
		yoursOpen := map[string]bool{}
		for _, cl := range a.fileList.Changelists() {
			for _, f := range cl.Files {
				yoursOpen[f.DepotFile] = true
			}
		}
		previousPath := a.browserPane.SelectedPath()
		autoExpand := a.browserPane.LoadChildren(m.parentPath, m.dirs, m.files, nil, yoursOpen, m.othersOpen)
		a.relayout()
		return a, a.cmdBrowserLoadFollowup(previousPath, autoExpand, a.browserPane.Mode())

	case browserFastMsg:
		if m.epoch != a.browserEpoch {
			return a, nil
		}
		if m.err == nil {
			previousPath := a.browserPane.SelectedPath()
			autoExpand := a.browserPane.LoadChildrenFast(m.parentPath, m.dirs, m.files)
			a.relayout()
			return a, a.cmdBrowserLoadFollowup(previousPath, autoExpand, panes.BrowserModeWorkspace)
		}
		return a, nil

	case browserStatusMsg:
		if m.epoch != a.browserEpoch {
			return a, nil
		}
		if m.err != nil {
			a.status = "browser load failed: " + m.err.Error()
			return a, nil
		}
		yoursOpen := map[string]bool{}
		for _, cl := range a.fileList.Changelists() {
			for _, f := range cl.Files {
				yoursOpen[f.DepotFile] = true
			}
		}
		previousPath := a.browserPane.SelectedPath()
		autoExpand := a.browserPane.ApplyStatus(m.parentPath, m.haveFiles, m.depotDirs, m.missingFiles, yoursOpen, m.othersOpen)
		a.relayout()
		return a, a.cmdBrowserLoadFollowup(previousPath, autoExpand, panes.BrowserModeWorkspace)

	case panes.BrowserNeedsSearchMsg:
		return a, a.cmdBrowserSearch(m.Root, m.Mode)

	case browserSearchDoneMsg:
		if m.epoch != a.browserEpoch {
			return a, nil
		}
		if m.err == nil {
			previous := a.browserPane.SelectedPath()
			a.browserPane.LoadSearchIndex(m.files)
			if a.active == paneBrowser && a.browserPane.SelectedPath() != previous {
				return a, a.cmdSelectionDetails()
			}
		}
		return a, nil

	case browserNavigateMsg:
		if m.epoch != a.browserEpoch || m.request != a.browserNavRequest || m.err != nil {
			return a, nil
		}
		previous := a.browserPane.SelectedPath()
		a.browserNavTarget = m.path
		return a, a.cmdBrowserLoadFollowup(previous, "", a.browserPane.Mode())

	case forceSyncDoneMsg:
		if m.err != nil {
			a.status = "force sync failed: " + m.err.Error()
			a.cmdLog.Add("p4 sync -f "+m.path, "error: "+m.err.Error())
		} else {
			a.status = "Force sync complete: " + m.path
			a.cmdLog.Add("p4 sync -f "+m.path, "done")
		}
		return a, tea.Batch(a.refresh(), a.cmdFetch(), a.cmdFetchCurrentCL())

	case fileActionDoneMsg:
		logPath := filepath.ToSlash(m.path)
		if m.err != nil {
			a.status = m.op + " failed: " + m.err.Error()
			a.cmdLog.Add("p4 "+m.op+" "+logPath, "error: "+m.err.Error())
		} else {
			if m.op == "reconcile" {
				a.cmdLog.Add("p4 restore-readonly", fmt.Sprintf("path=%s count=%d err=%v", m.path, m.readOnlyCount, m.readOnlyErr))
			}
			a.status = m.op + " complete: " + m.path
			a.cmdLog.Add("p4 "+m.op+" "+logPath, "done")
		}
		return a, a.refresh()

	case browserDeleteDoneMsg:
		if m.err != nil {
			a.status = "delete failed - see log"
			a.cmdLog.Add("p4 delete "+m.path, "error: "+m.err.Error())
		} else {
			a.status = "marked for delete: " + m.path
			a.cmdLog.Add("p4 delete "+m.path, "done")
		}
		return a, a.refresh()

	case integrateDoneMsg:
		cmd := "p4 " + m.op + " " + m.src
		if m.err != nil {
			a.status = m.op + " failed - see log"
			a.cmdLog.Add(cmd, "error: "+m.err.Error())
		} else {
			a.status = m.op + " complete — resolve if needed, then submit"
			a.cmdLog.Add(cmd, "done")
		}
		return a, a.refresh()

	case panes.BrowserOpenFileMsg:
		return a, a.cmdOpenFile(m.DepotPath)

	case openFileDoneMsg:
		if m.err != nil {
			a.status = "open failed: " + m.err.Error()
		}
		return a, nil

	case syncToCLDoneMsg:
		if m.id != a.opID {
			return a, nil
		}
		a.opRunning = false
		if m.err != nil {
			a.status = "checkout failed: " + m.err.Error()
			a.cmdLog.Add("p4 sync @"+m.cl, "error: "+m.err.Error())
		} else {
			a.pinnedCL = m.cl
			a.status = fmt.Sprintf("Workspace synced to CL %s", m.cl)
			a.cmdLog.Add("p4 sync @"+m.cl, "done")
		}
		return a, tea.Batch(a.refresh(), a.cmdFetch(), a.cmdFetchCurrentCL(), a.cmdLoadShelved())

	case authRequiredMsg:
		a.authModal = newAuthModal()
		a.status = "Session expired — enter password"
		return a, textinput.Blink

	case authDoneMsg:
		if m.err != nil {
			a.status = "login failed: " + m.err.Error()
			a.cmdLog.Add("p4 login", "error: "+m.err.Error())
		} else {
			a.status = "Logged in"
			a.cmdLog.Add("p4 login", "done")
			return a, tea.Batch(a.refresh(), a.cmdInfo())
		}
		return a, nil

	case streamSwitchedMsg:
		a.streamSwitch = nil
		if m.err != nil {
			a.status = "stream switch failed: " + m.err.Error()
			a.cmdLog.Add("p4 switch", "error: "+m.err.Error())
		} else {
			a.status = "Stream switched to " + m.stream
			a.cmdLog.Add("p4 switch "+m.stream, "done")
			return a, tea.Batch(a.cmdInfo(), a.refresh())
		}
		return a, nil

	case moveDoneMsg:
		if m.err != nil {
			a.status = m.err.Error()
			if m.userErr {
				a.cmdLog.Add("move CL", "error: "+m.err.Error())
			} else {
				a.cmdLog.Add("p4 reopen", "error: "+m.err.Error())
			}
		} else {
			dest := m.clID
			if dest == "default" {
				dest = "default CL"
			}
			a.status = fmt.Sprintf("Moved %d file(s) to %s", m.count, dest)
			a.cmdLog.Add("p4 reopen -c "+m.clID, fmt.Sprintf("%d files", m.count))
			a.fileList.ClearMarks()
		}
		return a, a.refresh()

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
		if m.Button == tea.MouseButtonWheelUp || m.Button == tea.MouseButtonWheelDown {
			if hovered := a.paneAt(m.X, m.Y); hovered != a.active {
				a.active = hovered
				a.updateFocus()
			}
			var cmd tea.Cmd
			switch a.active {
			case paneBrowser:
				a.cancelBrowserNavigation()
				cmd = a.browserPane.Update(m)
			case paneFileList:
				cmd = a.fileList.Update(m)
			case paneStreams:
				cmd = a.streamsPane.Update(m)
			case paneShelved:
				cmd = a.shelvedPane.Update(m)
			case paneLog:
				cmd = a.log.Update(m)
			case paneDiff:
				cmd = a.diff.Update(m)
			case paneCmdLog:
				cmd = a.cmdLog.Update(m)
			}
			if a.active == paneBrowser || a.active == paneFileList {
				return a, tea.Batch(cmd, a.cmdSelectionDetails())
			}
			return a, cmd
		}
		if m.Action == tea.MouseActionPress && m.Button == tea.MouseButtonLeft {
			return a.handleClick(m.X, m.Y)
		}
		return a, nil

	case tea.KeyMsg:
		return a.handleKey(m)
	}
	var cmd tea.Cmd
	switch {
	case a.authModal != nil:
		a.authModal.input, cmd = a.authModal.input.Update(msg)
	case a.integrateModal != nil && a.integrateModal.isClassic:
		if a.integrateModal.step == 0 {
			a.integrateModal.sourceInput, cmd = a.integrateModal.sourceInput.Update(msg)
		} else {
			a.integrateModal.targetInput, cmd = a.integrateModal.targetInput.Update(msg)
		}
	case a.moveModal != nil:
		a.moveModal.input, cmd = a.moveModal.input.Update(msg)
	case a.shelveModal != nil:
		a.shelveModal.input, cmd = a.shelveModal.input.Update(msg)
	case a.modal != nil:
		a.modal.input, cmd = a.modal.input.Update(msg)
	case a.showHelp:
		a.helpFilter, cmd = a.helpFilter.Update(msg)
	}
	return a, cmd
}

func (a *App) handleKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	// Any key exits select mode and re-enables mouse.
	if a.selectMode {
		a.selectMode = false
		return a, tea.EnableMouseCellMotion
	}

	if a.showHelp {
		return a.handleHelpKey(m)
	}
	if m.String() == "?" {
		a.showHelp = true
		a.updateFocus()
		a.helpFilter.SetValue("")
		cmd := a.helpFilter.Focus()
		a.refreshHelp()
		a.helpViewport.GotoTop()
		return a, cmd
	}

	// When the active pane is in filter mode, route all keys to the pane so
	// global shortcuts (s, d, b, …) don't fire while typing a search term.
	// Esc is intercepted here so async nav loading can be triggered.
	if a.activePaneInFilterMode() {
		if a.active == paneBrowser {
			a.cancelBrowserNavigation()
		}
		if m.String() == "esc" {
			return a.handleFilterClear(m)
		}
		var cmd tea.Cmd
		switch a.active {
		case paneBrowser:
			cmd = a.browserPane.Update(m)
		case paneFileList:
			cmd = a.fileList.Update(m)
		}
		return a, tea.Batch(cmd, a.cmdSelectionDetails())
	}

	// Esc with an active (but non-typing) filter clears it and stays in the pane.
	if m.String() == "esc" {
		switch a.active {
		case paneBrowser:
			if a.browserPane.HasFilter() {
				return a.handleFilterClear(m)
			}
		case paneFileList:
			if a.fileList.HasFilter() {
				return a.handleFilterClear(m)
			}
		}
	}

	if a.opRunning {
		switch m.String() {
		case "p", "P", "s", "e", "E", "d", "u", "D", "F", "m", "R", "a", "i", "y":
			a.status = a.opName + " in progress"
			return a, nil
		}
		if (m.String() == "enter" || m.String() == "l") && (a.active == paneStreams || a.active == paneLog) ||
			m.String() == " " && (a.active == paneBrowser || a.active == paneLog) {
			a.status = a.opName + " in progress"
			return a, nil
		}
	}

	// Resolve pane captures its own keys before any global handler.
	if a.active == paneResolve {
		if m.String() == "esc" {
			a.active = paneFileList
			a.updateFocus()
			return a, nil
		}
		cmd := a.resolve.Update(m)
		return a, cmd
	}

	// Block server-dependent operations when offline.
	if a.offlineMode {
		switch m.String() {
		case "p", "s", "e", "E", "d", "u", "D", "F", "f", "m", "R", "a":
			a.status = "Offline — p4 server unreachable"
			return a, nil
		}
	}

	switch m.String() {
	case "v":
		a.selectMode = true
		return a, tea.DisableMouse
	case "q", "ctrl+c":
		if a.opCancel != nil {
			a.opCancel()
		}
		return a, tea.Quit
	case "i":
		if !a.isStreamDepot {
			a.integrateModal = newIntegrateModalClassic()
			return a, textinput.Blink
		}
		if a.active != paneStreams {
			return a, nil
		}
		source := a.streamsPane.SelectedStream()
		if source == "" {
			source = a.client.Stream
		}
		if parent := a.streamParent(source); parent != "" {
			a.integrateModal = newIntegrateModalStream(source, parent)
			return a, nil
		}
		a.status = "Selected stream has no parent to integrate with"
		return a, nil
	case "r":
		a.status = "Refreshing..."
		a.browserEpoch++
		a.cancelBrowserNavigation()
		cmds := []tea.Cmd{a.refresh()}
		for _, path := range a.browserPane.LoadedDirPaths() {
			cmds = append(cmds, a.cmdBrowserLoad(path, a.browserPane.Mode()))
		}
		if root := a.browserPane.RootPath(); root != "" && (a.browserPane.Mode() == panes.BrowserModeWorkspace || a.isStreamDepot) {
			cmds = append(cmds, a.cmdBrowserSearch(root, a.browserPane.Mode()))
		}
		return a, tea.Batch(cmds...)
	case "t":
		a.fileList.ToggleMode()
		return a, nil
	case "f":
		a.statusPane.SetFetching()
		a.status = "Fetching..."
		return a, a.cmdFetch()
	case "b":
		sel := a.browserPane.SelectedEntry()
		a.browserEpoch++
		a.cancelBrowserNavigation()
		a.browserPane.ToggleMode()
		if path := a.browserPane.RootPath(); path != "" {
			cmds := []tea.Cmd{
				a.cmdBrowserLoad(path, a.browserPane.Mode()),
				a.cmdBrowserSearch(path, a.browserPane.Mode()),
				a.cmdSelectionDetails(),
			}
			if sel != nil && !sel.IsDir {
				epoch := a.browserEpoch
				request := a.browserNavRequest
				client := *a.client
				if a.browserPane.Mode() == panes.BrowserModeWorkspace {
					cmds = append(cmds, func() tea.Msg {
						path, err := client.WhereClient(sel.DepotPath)
						return browserNavigateMsg{epoch: epoch, request: request, path: path, err: err}
					})
				} else if !strings.HasPrefix(sel.DepotPath, "//"+client.Workspace+"/") {
					a.browserNavTarget = sel.DepotPath
				}
			}
			return a, tea.Batch(cmds...)
		}
		return a, nil
	case "o":
		var localPath string
		switch a.active {
		case paneBrowser:
			if sel := a.browserPane.SelectedEntry(); sel != nil && sel.LocalPath != "" {
				localPath = sel.LocalPath
			}
		case paneFileList:
			if f := a.fileList.SelectedFile(); f != nil {
				localPath = clientToLocal(a.client.Root, a.client.Workspace, f.ClientFile)
			}
		}
		if localPath == "" {
			a.status = "No local file selected"
			return a, nil
		}
		if err := revealInExplorer(localPath, a.linuxFileManager); err != nil {
			a.status = "reveal error: " + err.Error()
		}
		return a, nil
	case "u":
		if a.active == paneShelved {
			clID := a.shelvedPane.SelectedCL()
			if clID == "" {
				a.status = "No shelved CL selected"
				return a, nil
			}
			return a, a.cmdUnshelveAndDelete(clID)
		}
		if a.active == paneBrowser {
			// handled in the pane-specific section below
			break
		}
		if a.opRunning {
			a.status = a.opName + " in progress"
			return a, nil
		}
		// Pending pane: revert only unchanged (edit action, no local changes).
		marked := a.fileList.MarkedFiles()
		if len(marked) > 0 {
			return a, a.cmdRevertUnchangedFiles(marked)
		}
		if f := a.fileList.SelectedFile(); f != nil {
			return a, a.cmdRevertUnchangedFiles([]p4.OpenedFile{*f})
		}
		if clID := a.fileList.SelectedCL(); clID != "" {
			return a, a.cmdRevertUnchangedCL(clID)
		}
		a.status = "No file selected"
		return a, nil
	case "d":
		if a.active == paneShelved {
			clID := a.shelvedPane.SelectedCL()
			if clID == "" {
				a.status = "No shelved CL selected"
				return a, nil
			}
			a.confirm = &confirmModal{kind: confirmKindDeleteShelf, clID: clID}
			return a, nil
		}
		if a.active == paneBrowser {
			// handled in the pane-specific section below
			break
		}
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
			a.confirm = &confirmModal{
				kind:          confirmKindRevert,
				files:         files,
				localToDelete: localPathsForAdds(a.client.Root, a.client.Workspace, marked),
			}
		} else {
			f := a.fileList.SelectedFile()
			if f != nil {
				if f.Action == p4.ActionAdd {
					local := clientToLocal(a.client.Root, a.client.Workspace, f.ClientFile)
					a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{f.ClientFile}, localToDelete: []string{local}}
					return a, nil
				}
				if f.Action != p4.ActionEdit {
					return a, a.cmdRevert([]string{f.ClientFile}, "")
				}
				return a, a.cmdRevertCheck(f.ClientFile, "")
			} else if clID := a.fileList.SelectedCL(); clID != "" {
				cls := a.fileList.FilesForCL(clID)
				if len(cls) == 0 {
					a.status = "No files in CL " + clID
					return a, nil
				}
				if len(cls) == 1 {
					f := cls[0]
					if f.Action == p4.ActionAdd {
						local := clientToLocal(a.client.Root, a.client.Workspace, f.ClientFile)
						a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{f.ClientFile}, localToDelete: []string{local}, clID: clID}
						return a, nil
					}
					if f.Action != p4.ActionEdit {
						return a, a.cmdRevert([]string{f.ClientFile}, clID)
					}
					return a, a.cmdRevertCheck(f.ClientFile, clID)
				}
				files := make([]string, len(cls))
				for i, ff := range cls {
					files[i] = ff.ClientFile
				}
				a.confirm = &confirmModal{
					kind:          confirmKindRevert,
					files:         files,
					localToDelete: localPathsForAdds(a.client.Root, a.client.Workspace, cls),
					clID:          clID,
				}
			} else {
				a.status = "No file selected"
				return a, nil
			}
		}
		return a, nil
	case "p":
		if a.opRunning {
			a.status = a.opName + " already in progress"
			return a, nil
		}
		if a.active == paneBrowser {
			path := a.browserPane.SelectedPath()
			if path == "" {
				a.status = "No file or folder selected"
				return a, nil
			}
			a.status = "Syncing " + path + "..."
			return a, a.cmdSyncPath(path)
		}
		if a.pinnedCL != "" {
			return a, a.cmdSyncToCL(a.client.Stream, a.pinnedCL)
		}
		syncTarget := "//..."
		if a.client.Stream != "" {
			syncTarget = a.client.Stream + "/..."
		}
		a.status = "Syncing " + syncTarget + "..."
		return a, a.cmdSyncStart()
	case "P":
		if a.active != paneBrowser {
			return a, nil
		}
		path := a.browserPane.SelectedPath()
		if path == "" {
			a.status = "No file or folder selected for force sync"
			return a, nil
		}
		a.status = "Force syncing " + path + "..."
		return a, a.cmdForceSync(path)
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
	case "left":
		a.cycleFocusBackward()
		return a, a.cmdSelectionDetails()
	case "g":
		a.historyMode = !a.historyMode
		a.updateFocus()
		if a.historyMode {
			// load filelog for currently selected file/dir, or stream root
			if a.fileList.SelectedIsHeader() {
				if root := a.browserPane.RootPath(); root != "" {
					return a, a.cmdFilelogMax(depotWildcard(root), 100)
				}
			} else if f := a.fileList.SelectedFile(); f != nil {
				return a, a.cmdFilelog(f.DepotFile)
			} else if dp := a.fileList.SelectedDepotPath(); dp != "" {
				return a, a.cmdFilelogMax(dp, 100)
			}
			if path := a.browserPane.SelectedPath(); path != "" {
				max := 0
				if strings.HasSuffix(path, "/...") {
					max = 100
				}
				return a, a.cmdFilelogMax(path, max)
			}
			if root := a.browserPane.RootPath(); root != "" {
				return a, a.cmdFilelogMax(depotWildcard(root), 100)
			}
		} else if a.active == paneLog {
			a.active = paneDiff
			a.updateFocus()
		}
		return a, a.cmdSelectionDetails()
	case "J":
		switch a.active {
		case paneBrowser:
			a.browserPane.JumpBottom()
		case paneFileList:
			a.fileList.JumpBottom()
		case paneLog:
			a.log.JumpBottom()
		case paneStreams:
			a.streamsPane.JumpBottom()
		}
		return a, nil
	case "K":
		switch a.active {
		case paneBrowser:
			a.browserPane.JumpTop()
		case paneFileList:
			a.fileList.JumpTop()
		case paneLog:
			a.log.JumpTop()
		case paneStreams:
			a.streamsPane.JumpTop()
		}
		return a, nil
	case "H":
		switch a.active {
		case paneBrowser:
			a.cancelBrowserNavigation()
			a.browserPane.CollapseAll()
		case paneFileList:
			a.fileList.CollapseAll()
		}
		return a, nil
	case "L":
		switch a.active {
		case paneBrowser:
			a.cancelBrowserNavigation()
			toLoad := a.browserPane.ExpandSelected1Level()
			if len(toLoad) > 0 {
				cmds := make([]tea.Cmd, len(toLoad))
				for i, path := range toLoad {
					cmds[i] = a.cmdBrowserLoad(path, a.browserPane.Mode())
				}
				return a, tea.Batch(cmds...)
			}
		case paneFileList:
			a.fileList.ExpandAll()
		}
		return a, nil
	case "right":
		a.cycleFocusForward()
		return a, a.cmdSelectionDetails()
	case "R":
		a.status = "Checking conflicts..."
		var resolvePath string
		if f := a.fileList.SelectedFile(); f != nil {
			resolvePath = f.ClientFile
		} else if dp := a.fileList.SelectedDepotPath(); dp != "" {
			resolvePath = dp
		}
		return a, a.cmdResolveList(resolvePath)
	case "e", "E":
		if a.active == paneFileList {
			noRevert := m.String() == "E"
			if files := a.filesToShelve(); len(files) > 0 {
				ti := textinput.New()
				ti.Placeholder = "Changelist description..."
				ti.Width = 50
				ti.Focus()
				a.shelveModal = &shelveDescModal{input: ti, files: files, noRevert: noRevert}
				return a, textinput.Blink
			}
			return a, nil
		}
		return a, nil
	case "D":
		if a.active == paneBrowser {
			path := a.browserPane.SelectedPath()
			if path == "" {
				if root := a.browserPane.RootPath(); root != "" {
					path = root + "/..."
				}
			}
			if path != "" {
				return a, a.cmdBrowserDelete(path)
			}
		}
		return a, nil
	case "m":
		if a.active == paneFileList {
			files := a.filesToMove()
			if len(files) == 0 {
				a.status = "No file selected"
				return a, nil
			}
			ti := textinput.New()
			ti.Placeholder = "Existing CL number, new CL name, or empty for default"
			ti.Width = 40
			ti.Focus()
			a.moveModal = &moveCLModal{input: ti, files: files}
			return a, textinput.Blink
		}
		return a, nil
	case "1", "2", "3", "4", "5", "6":
		order := []activePane{paneBrowser, paneFileList, paneStreams, paneShelved, a.currentRightPane()}
		n := int(m.String()[0]-'0') - 1
		if n >= 0 && n < len(order) && (order[n] != paneStreams || a.showStreams) {
			a.active = order[n]
			a.updateFocus()
		}
		return a, a.cmdSelectionDetails()
	case "tab":
		a.cycleFocus()
		return a, a.cmdSelectionDetails()
	case "esc":
		a.active = paneBrowser
		a.updateFocus()
		return a, a.cmdSelectionDetails()
	}

	var cmd tea.Cmd
	switch a.active {
	case paneBrowser:
		a.cancelBrowserNavigation()
		switch m.String() {
		case "l":
			return a, tea.Batch(a.browserPane.ExpandCurrent(), a.cmdSelectionDetails())
		case "h":
			a.browserPane.CollapseCurrentOrParent()
			return a, a.cmdSelectionDetails()
		case "d":
			if sel := a.browserPane.SelectedEntry(); sel != nil && !sel.IsDir {
				// Find the opened file matching this depot path.
				var found *p4.OpenedFile
				for _, cl := range a.fileList.Changelists() {
					for i := range cl.Files {
						if cl.Files[i].DepotFile == sel.DepotPath {
							found = &cl.Files[i]
							break
						}
					}
					if found != nil {
						break
					}
				}
				if found != nil {
					if found.Action == p4.ActionAdd {
						local := clientToLocal(a.client.Root, a.client.Workspace, found.ClientFile)
						a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{found.ClientFile}, localToDelete: []string{local}}
						return a, nil
					}
					if found.Action != p4.ActionEdit {
						return a, a.cmdRevert([]string{found.ClientFile}, "")
					}
					return a, a.cmdRevertCheck(found.ClientFile, "")
				}
			}
			return a, nil
		case "u":
			if lp := a.browserLocalPath(); lp != "" {
				return a, a.cmdRevertUnchangedPath(lp)
			}
			return a, nil
		case " ":
			if lp := a.browserLocalPath(); lp != "" {
				a.status = "Reconciling..."
				return a, a.cmdReconcile(lp)
			}
			return a, nil
		case "a":
			if lp := a.browserLocalPath(); lp != "" {
				a.status = "Checking out..."
				return a, a.cmdEdit(lp)
			}
			return a, nil
		}
		cmd = a.browserPane.Update(m)
		return a, tea.Batch(cmd, a.cmdSelectionDetails())
	case paneFileList:
		switch m.String() {
		case "enter":
			if f := a.fileList.SelectedFile(); f != nil {
				return a, a.cmdOpenFile(f.DepotFile)
			}
		case "l":
			a.fileList.ExpandCurrent()
			return a, a.cmdSelectionDetails()
		case "h":
			a.fileList.CollapseCurrentOrParent()
			return a, a.cmdSelectionDetails()
		}
		cmd = a.fileList.Update(m)
		return a, tea.Batch(cmd, a.cmdSelectionDetails())
	case paneStreams:
		cmd = a.streamsPane.Update(m)
		switch m.String() {
		case "enter", "l":
			stream := a.streamsPane.SelectedStream()
			if stream != "" {
				sw := &streamSwitchModal{stream: stream, switching: true}
				a.streamSwitch = sw
				return a, tea.Batch(cmd, a.cmdSwitchToStream(stream))
			}
		}
	case paneShelved:
		cmd = a.shelvedPane.Update(m)
	case paneDiff:
		cmd = a.diff.Update(m)
	case paneLog:
		if a.historyMode && (m.String() == " " || m.String() == "enter") {
			cl := a.log.SelectedChange()
			if cl != "" {
				stream := ""
				if a.isStreamDepot {
					stream = a.browserPane.RootPath()
				}
				openFiles := a.fileList.HasFiles()
				a.checkout = &checkoutModal{cl: cl, stream: stream, hasFiles: openFiles}
				return a, nil
			}
		}
		cmd = a.log.Update(m)
	case paneResolve:
		cmd = a.resolve.Update(m)
	case paneCmdLog:
		cmd = a.cmdLog.Update(m)
	}
	return a, cmd
}

func (a *App) handleFilterClear(_ tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch a.active {
	case paneBrowser:
		a.cancelBrowserNavigation()
		if navTarget := a.browserPane.ClearFilter(); navTarget != "" {
			a.browserNavTarget = navTarget
			if next := a.browserPane.FirstUnloadedAncestor(navTarget); next != "" {
				return a, tea.Batch(a.cmdBrowserLoad(next, a.browserPane.Mode()), a.cmdSelectionDetails())
			}
		}
	case paneFileList:
		a.fileList.ClearFilter()
	}
	return a, a.cmdSelectionDetails()
}

func (a *App) handleStreamSwitchKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	// While p4 switch is running, ignore all keys.
	return a, nil
}

func (a *App) handleIntegrateKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	im := a.integrateModal
	switch m.String() {
	case "esc":
		a.integrateModal = nil
		return a, nil
	}
	if !im.isClassic {
		// Stream depot: m = pull from parent, c = push to parent.
		if a.opRunning && (m.String() == "m" || m.String() == "c") {
			a.status = a.opName + " in progress"
			return a, nil
		}
		switch m.String() {
		case "m":
			a.integrateModal = nil
			return a, a.cmdPullStream(im.sourceStream)
		case "c":
			a.integrateModal = nil
			return a, a.cmdPromoteStream(im.sourceStream)
		}
		return a, nil
	}
	// Classic depot: two-step path input
	switch m.String() {
	case "enter":
		if im.step == 0 {
			if im.sourceInput.Value() == "" {
				return a, nil
			}
			im.step = 1
			im.sourceInput.Blur()
			im.targetInput.Focus()
			return a, textinput.Blink
		}
		// step 1 — submit
		if a.opRunning {
			a.status = a.opName + " in progress"
			return a, nil
		}
		src := im.sourceInput.Value()
		dst := im.targetInput.Value()
		if dst == "" {
			return a, nil
		}
		a.integrateModal = nil
		return a, a.cmdIntegrateClassic(src, dst)
	default:
		var cmd tea.Cmd
		if im.step == 0 {
			im.sourceInput, cmd = im.sourceInput.Update(m)
		} else {
			im.targetInput, cmd = im.targetInput.Update(m)
		}
		return a, cmd
	}
}

func (a *App) handleAuthKey(m tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.String() {
	case "esc":
		a.authModal = nil
		a.status = "Login cancelled"
		return a, nil
	case "enter", "ctrl+enter":
		save := m.String() == "ctrl+enter" && a.client.StorePassword
		password := a.authModal.input.Value()
		client := *a.client
		a.authModal = nil
		a.status = "Logging in..."
		return a, a.withActivity("Logging in", func() tea.Msg {
			if err := client.Login(password); err != nil {
				return authDoneMsg{err: err}
			}
			if save {
				if err := client.StoreCredential(password); err != nil {
					return authDoneMsg{err: fmt.Errorf("login ok but keychain save failed: %w", err)}
				}
			}
			return authDoneMsg{}
		})
	default:
		var cmd tea.Cmd
		a.authModal.input, cmd = a.authModal.input.Update(m)
		return a, cmd
	}
}

func (a *App) setOffline(offline bool) {
	a.offlineMode = offline
	a.statusPane.SetOffline(offline)
	if offline {
		a.streamsPane.SetPending(-1)
	}
	if offline {
		a.status = "Offline — p4 server unreachable"
	}
}

func (a *App) activePaneInFilterMode() bool {
	switch a.active {
	case paneBrowser:
		return a.browserPane.InFilterMode()
	case paneFileList:
		return a.fileList.InFilterMode()
	}
	return false
}

// filesToShelve returns the current selection as a flat file list for the shelve modal.
func (a *App) filesToShelve() []p4.OpenedFile {
	if marked := a.fileList.MarkedFiles(); len(marked) > 0 {
		return marked
	}
	if f := a.fileList.SelectedFile(); f != nil {
		return []p4.OpenedFile{*f}
	}
	if clID := a.fileList.SelectedCL(); clID != "" {
		return a.fileList.FilesForCL(clID)
	}
	return nil
}

func (a *App) execShelveWithDesc() tea.Cmd {
	if a.opRunning {
		a.status = a.opName + " in progress"
		return nil
	}
	client := *a.client
	m := a.shelveModal
	a.shelveModal = nil
	desc := strings.TrimSpace(m.input.Value())
	if desc == "" {
		desc = "Shelved changes"
	}
	files := m.files
	noRevert := m.noRevert
	a.opRunning = true
	a.opName = "Shelving"
	a.opTotal = len(files)
	a.opDone = 0
	a.opID++
	id := a.opID
	clientFiles := make([]string, len(files))
	for i, f := range files {
		clientFiles[i] = f.ClientFile
	}
	return func() tea.Msg {
		clID, err := client.CreateChange(desc)
		if err != nil {
			return shelveDoneMsg{id: id, err: err}
		}
		for _, f := range files {
			if _, err := client.Reopen(clID, f.ClientFile); err != nil {
				return shelveDoneMsg{id: id, err: err}
			}
		}
		if _, err := client.ShelveFiles(clID, clientFiles); err != nil {
			return shelveDoneMsg{id: id, err: err}
		}
		if !noRevert {
			if _, err := client.RevertCL(clID, clientFiles); err != nil {
				return shelveDoneMsg{id: id, err: err}
			}
		}
		return shelveDoneMsg{id: id, count: len(files)}
	}
}

func (a *App) renderShelveModal() string {
	return a.renderModalBox(fmt.Sprintf("Shelve %d file(s)", len(a.shelveModal.files)), a.modalInputView(a.shelveModal.input))
}

func (a *App) execSubmit() tea.Cmd {
	if a.opRunning {
		a.status = a.opName + " in progress"
		return nil
	}
	m := a.modal
	a.modal = nil
	desc := strings.TrimSpace(m.input.Value())
	if desc == "" {
		desc = "lazyp4 submit"
	}
	if len(m.files) > 0 {
		return a.cmdSubmitMarkedFilter(m.files, desc)
	}
	return a.cmdSubmitStart(m.clID, desc)
}

func (a *App) handleClick(x, y int) (tea.Model, tea.Cmd) {
	leftW := a.width / 3
	contentH := a.height - 1
	bodyH := contentH - panes.CmdLogHeight

	if y >= bodyH {
		return a, nil
	}

	if x < leftW {
		streamsH := 0
		if a.showStreams {
			streamsH = len(a.streams) + 2
			if streamsH > 10 {
				streamsH = 10
			}
		}
		shelvedH := a.shelvedPane.PreferredHeight()
		browserH := (bodyH - panes.StatusHeight) - streamsH - shelvedH
		if browserH < 3 {
			browserH = 3
		}

		browserTop := panes.StatusHeight
		browserBottom := browserTop + browserH
		streamsTop := browserBottom
		streamsBottom := streamsTop + streamsH
		shelvedTop := streamsBottom

		switch {
		case y >= browserTop && y < browserBottom:
			a.active = paneBrowser
			a.updateFocus()
			a.cancelBrowserNavigation()
			if contentY := y - browserTop - 1; contentY >= 0 {
				a.browserPane.SetCursor(a.browserPane.ScrollOffset() + contentY)
			}
			return a, a.cmdSelectionDetails()
		case a.showStreams && y >= streamsTop && y < streamsBottom:
			a.active = paneStreams
			a.updateFocus()
			if contentY := y - streamsTop - 1; contentY >= 0 && contentY < streamsH-2 {
				a.streamsPane.SetCursor(a.streamsPane.ScrollOffset() + contentY)
			}
		case y >= shelvedTop:
			a.active = paneShelved
			a.updateFocus()
			if contentY := y - shelvedTop - 1; contentY >= 0 {
				a.shelvedPane.SetCursor(a.shelvedPane.ScrollOffset() + contentY)
			}
		}
	} else {
		// right side: top = pending, bottom = diff/log/resolve
		pendingH := bodyH * 3 / 5
		if pendingH < 4 {
			pendingH = 4
		}

		if y < pendingH {
			a.active = paneFileList
			a.updateFocus()
			contentY := y - 1
			if contentY >= 0 {
				rowIdx := a.fileList.ScrollOffset() + contentY
				a.fileList.SetCursor(rowIdx)
			}
			return a, a.cmdSelectionDetails()
		} else {
			a.active = a.currentRightPane()
			a.updateFocus()
			if a.historyMode {
				if contentY := y - pendingH - 1; contentY >= 0 {
					a.log.SetCursorByLine(contentY)
				}
			}
		}
	}

	return a, nil
}

// paneAt returns which pane contains the given screen coordinate.
func (a *App) paneAt(x, y int) activePane {
	leftW := a.width / 3
	contentH := a.height - 1
	bodyH := contentH - panes.CmdLogHeight

	if y >= bodyH {
		return paneCmdLog
	}

	if x < leftW {
		streamsH := 0
		if a.showStreams {
			streamsH = len(a.streams) + 2
			if streamsH > 10 {
				streamsH = 10
			}
		}
		shelvedH := a.shelvedPane.PreferredHeight()
		browserH := (bodyH - panes.StatusHeight) - streamsH - shelvedH
		if browserH < 3 {
			browserH = 3
		}
		browserTop := panes.StatusHeight
		browserBottom := browserTop + browserH
		streamsTop := browserBottom
		streamsBottom := streamsTop + streamsH
		switch {
		case y >= browserTop && y < browserBottom:
			return paneBrowser
		case a.showStreams && y >= streamsTop && y < streamsBottom:
			return paneStreams
		default:
			return paneShelved
		}
	}

	pendingH := bodyH * 3 / 5
	if pendingH < 4 {
		pendingH = 4
	}
	if y < pendingH {
		return paneFileList
	}
	return a.currentRightPane()
}

func (a *App) currentRightPane() activePane {
	if a.active == paneResolve {
		return paneResolve
	}
	if a.historyMode {
		return paneLog
	}
	if a.active == paneLog {
		return paneLog
	}
	return paneDiff
}

func (a *App) cycleFocus() {
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

func (a *App) paneOrder() []activePane {
	order := []activePane{paneBrowser, paneFileList}
	if a.showStreams {
		order = append(order, paneStreams)
	}
	order = append(order, paneShelved, a.currentRightPane())
	return order
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
	visible := !a.showHelp
	a.browserPane.SetFocused(visible && a.active == paneBrowser)
	a.fileList.SetFocused(visible && a.active == paneFileList)
	a.streamsPane.SetFocused(visible && a.active == paneStreams)
	a.shelvedPane.SetFocused(visible && a.active == paneShelved)
	a.diff.SetFocused(visible && a.active == paneDiff)
	a.log.SetFocused(visible && a.active == paneLog)
	a.resolve.SetFocused(visible && a.active == paneResolve)
	a.cmdLog.SetFocused(visible && a.active == paneCmdLog)
	diffActive := !a.historyMode
	a.diff.SetDiffActive(diffActive)
	a.log.SetDiffActive(diffActive)
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

	streamsH := 0
	if a.showStreams {
		streamsH = len(a.streams) + 2
		if streamsH > 10 {
			streamsH = 10
		}
		a.streamsPane.SetSize(leftW, streamsH)
	}

	shelvedH := a.shelvedPane.PreferredHeight()
	a.shelvedPane.SetSize(leftW, shelvedH)

	browserH := leftH - streamsH - shelvedH
	if browserH < 3 {
		browserH = 3
	}
	a.browserPane.SetSize(leftW, browserH)

	// Right column: pending (top) + diff (bottom)
	pendingH := bodyH * 3 / 5
	if pendingH < 4 {
		pendingH = 4
	}
	diffH := bodyH - pendingH

	a.fileList.SetSize(rightW, pendingH)
	a.diff.SetSize(rightW, diffH)
	a.log.SetSize(rightW, diffH)
	a.resolve.SetSize(rightW, diffH)
	a.cmdLog.SetWidth(a.width)

	a.refreshHelp()
}

// View renders the full TUI.
func (a *App) View() string {
	if a.width == 0 {
		return "Loading..."
	}

	leftParts := []string{a.statusPane.View(), a.browserPane.View()}
	if a.showStreams {
		a.streamsPane.SetActivity(a.streamActivityView())
		leftParts = append(leftParts, a.streamsPane.View())
	}
	leftParts = append(leftParts, a.shelvedPane.View())
	leftSide := lipgloss.JoinVertical(lipgloss.Left, leftParts...)

	var rightBottom string
	switch a.currentRightPane() {
	case paneLog:
		rightBottom = a.log.View()
	case paneResolve:
		rightBottom = a.resolve.View()
	default:
		rightBottom = a.diff.View()
	}
	rightSide := lipgloss.JoinVertical(lipgloss.Left, a.fileList.View(), rightBottom)

	body := lipgloss.JoinHorizontal(lipgloss.Top, leftSide, rightSide)
	content := lipgloss.JoinVertical(lipgloss.Left, body, a.cmdLog.View())
	lines := strings.Split(content, "\n")
	// Pane minimum heights may exceed a short terminal; keep the footer and overlays visible.
	if len(lines) > max(0, a.height-1) {
		content = strings.Join(lines[:max(0, a.height-1)], "\n")
	}
	base := lipgloss.JoinVertical(lipgloss.Left, content, a.renderHotkeys())

	if a.authModal != nil {
		return overlayCenter(a.renderAuthModal(), base, a.width, a.height)
	}
	if a.integrateModal != nil {
		return overlayCenter(a.renderIntegrateModal(), base, a.width, a.height)
	}
	if a.streamSwitch != nil {
		return overlayCenter(a.renderStreamSwitchModal(), base, a.width, a.height)
	}
	if a.checkout != nil {
		return overlayCenter(a.renderCheckoutModal(), base, a.width, a.height)
	}
	if a.confirm != nil {
		if a.confirm.kind == confirmKindRevert {
			return a.placeDiscardModal(base)
		}
		return overlayCenter(a.renderConfirmModal(), base, a.width, a.height)
	}
	if a.shelveModal != nil {
		return overlayCenter(a.renderShelveModal(), base, a.width, a.height)
	}
	if a.moveModal != nil {
		return overlayCenter(a.renderMoveModal(), base, a.width, a.height)
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
	styleStatus   = lipgloss.NewStyle()
	styleActivity = lipgloss.NewStyle().Foreground(lipgloss.Color("6"))

	styleHotkeys = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))

	styleHotkeyKey = lipgloss.NewStyle().Foreground(lipgloss.Color("4"))
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
		styleStatus.Render(strings.Repeat("░", barWidth-filled))

	var count string
	if a.opTotal == 0 {
		if a.opDone > 0 {
			count = styleStatus.Render(fmt.Sprintf("%d files", a.opDone))
		} else {
			count = styleStatus.Render("Calculating...")
		}
	} else {
		count = styleStatus.Render(fmt.Sprintf("%d/%d files", a.opDone, a.opTotal))
	}

	var cancelHint string
	if a.opCancel != nil {
		cancelHint = styleHotkeys.Render("c - ") + styleHotkeyKey.Render("cancel")
	}
	left := " " + a.activityView() + "  " + bar + "  " + count
	left = ansiTruncate(left, max(0, a.width-lipgloss.Width(cancelHint)-2))
	leftW := lipgloss.Width(left)
	cancelW := lipgloss.Width(cancelHint)
	gap := a.width - leftW - cancelW - 1
	if gap < 1 {
		gap = 1
	}
	return left + strings.Repeat(" ", gap) + cancelHint + " "
}

func (a *App) renderHotkeys() string {
	if a.selectMode {
		hint := styleHotkeyKey.Render("Select mode") +
			styleHotkeys.Render(" - select text in terminal, then press any key to restore mouse")
		return " " + hint
	}
	if modalHint := a.modalHotkeys(); modalHint != "" {
		hint := styleHotkeys.Render(modalHint)
		if activity := a.activityView(); activity != "" {
			if a.opRunning && a.opTotal > 0 {
				activity += styleStatus.Render(fmt.Sprintf(" %d/%d files", a.opDone, a.opTotal))
			}
			hint = activity + " " + hint
		}
		return " " + ansi.Truncate(hint, max(0, a.width-1), "…")
	}
	if a.opRunning {
		return a.renderProgressBar()
	}

	type binding struct{ desc, key string }

	// Local bindings — most common actions for the active pane.
	var local []binding
	switch a.active {
	case paneBrowser:
		local = []binding{
			{"Expand", "enter"},
			{"Reconcile", "space"},
			{"Checkout", "a"},
			{"Browser mode", "b"},
			{"Revert unchanged", "u"},
		}
	case paneFileList:
		if a.fileList.HasFiles() {
			local = append(local, binding{"Mark", "space"})
		}
		local = append(local,
			binding{"Submit", "s"},
			binding{"Shelve", "e"},
			binding{"Move CL", "m"},
			binding{"Discard", "d"},
			binding{"Revert unchanged", "u"},
			binding{"Conflicts", "R"},
		)
	case paneShelved:
		local = []binding{
			{"Unshelve", "u"},
			{"Delete shelf", "d"},
		}
	case paneStreams:
		local = []binding{{"Switch stream", "enter"}, {"Integrate", "i"}}
	case paneResolve:
		local = []binding{
			{"Merge", "enter"},
			{"Accept theirs", "a"},
			{"Accept yours", "y"},
			{"Safe auto", "s"},
			{"Close", "esc"},
		}
	case paneLog:
		if a.historyMode {
			local = []binding{{"Checkout", "enter"}}
		}
	}

	// Global bindings — always shown.
	global := []binding{
		{"History/Diff", "g"},
		{"Fetch", "f"},
		{"Sync", "p"},
		{"Refresh", "r"},
		{"?", "help"},
	}
	if !a.isStreamDepot {
		global = append([]binding{{"Integrate", "i"}}, global...)
	}

	shown := local
	if len(local) < 5 {
		shown = append(shown, global...)
	}

	var parts []string
	for _, b := range shown {
		parts = append(parts, styleHotkeys.Render(b.desc+": ")+styleHotkeyKey.Render(b.key))
	}
	hotkeys := " " + strings.Join(parts, styleHotkeys.Render(" | "))

	hotkeysW := lipgloss.Width(hotkeys)
	statusText := styleStatus.Render(a.status + " ")
	statusW := lipgloss.Width(statusText)
	if activity := a.activityView(); activity != "" {
		hotkeys = " " + activity
		for _, part := range parts {
			candidate := hotkeys + styleHotkeys.Render(" | ") + part
			if lipgloss.Width(candidate)+statusW+1 > a.width {
				break
			}
			hotkeys = candidate
		}
		hotkeysW = lipgloss.Width(hotkeys)
		statusText = ansiTruncate(statusText, max(0, a.width-hotkeysW-1))
		statusW = lipgloss.Width(statusText)
	}
	gap := a.width - hotkeysW - statusW
	if gap < 1 {
		gap = 1
	}

	footer := hotkeys + strings.Repeat(" ", gap) + statusText
	if a.activityLabel() != "" {
		return ansiTruncate(footer, a.width)
	}
	return footer
}

func (a *App) renderModal() string {
	m := a.modal
	var title string
	if len(m.files) > 0 {
		title = fmt.Sprintf("Submit %d marked file(s)", len(m.files))
	} else {
		title = fmt.Sprintf("Submit CL %s", m.clID)
	}

	return a.renderModalBox(title, a.modalInputView(m.input))
}

func (a *App) renderCheckoutModal() string {
	co := a.checkout
	title := fmt.Sprintf("Sync workspace to CL %s", co.cl)
	body := "Sync the workspace to the selected changelist."
	if co.hasFiles {
		body = "Shelve and revert open files, then sync the workspace to the selected changelist."
	}
	return a.renderModalBox(title, body)
}

func (a *App) renderAuthModal() string {
	return a.renderModalBox("Session expired", a.modalInputView(a.authModal.input))
}

// streamParent returns the parent of a known stream, or empty for a mainline.
func (a *App) streamParent(stream string) string {
	for _, s := range a.streams {
		if s.Path == stream && s.Parent != "" && s.Parent != "none" {
			return s.Parent
		}
	}
	return ""
}

func (a *App) renderIntegrateModal() string {
	im := a.integrateModal
	if !im.isClassic {
		shortName := func(p string) string {
			if idx := strings.LastIndex(p, "/"); idx >= 0 {
				return p[idx+1:]
			}
			return p
		}
		parent := shortName(im.parentStream)
		current := shortName(im.sourceStream)
		key := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
		body := key.Render("m") + fmt.Sprintf(" Pull     %s → %s\n", parent, current) +
			key.Render("c") + fmt.Sprintf(" Promote  %s → %s", current, parent)
		return a.renderModalBox("Integrate", body)
	}
	// Classic depot
	var body string
	if im.step == 0 {
		body = "Source path:\n" + a.modalInputView(im.sourceInput)
	} else {
		source := ansi.Truncate("Source: "+im.sourceInput.Value(), max(1, a.popupWidth(80)-4), "…")
		body = source + "\nTarget path:\n" + a.modalInputView(im.targetInput)
	}
	rows := strings.Split(body, "\n")
	// Keep the active input when only part of its context fits above it.
	rows = rows[max(0, len(rows)-max(1, a.height-4)):]
	body = strings.Join(rows, "\n")
	return a.renderModalBox("Integrate", body)
}

func (a *App) renderStreamSwitchModal() string {
	sw := a.streamSwitch
	streamName := sw.stream
	if idx := strings.LastIndex(streamName, "/"); idx >= 0 {
		streamName = streamName[idx+1:]
	}
	return a.renderModalBox("Switch to "+streamName, "Switching workspace…")
}

func (a *App) renderConfirmModal() string {
	if a.confirm.kind == confirmKindRevert {
		return a.renderDiscardModal()
	}
	var title, desc string
	switch a.confirm.kind {
	case confirmKindDeleteShelf:
		title = "Delete shelf"
		desc = styleStatus.Render(fmt.Sprintf("CL %s", a.confirm.clID))
	case confirmKindResolve:
		title = "Accept yours"
		desc = "Incoming changes will be discarded."
		for _, flag := range a.confirm.resolveFlags {
			if flag == "-at" {
				title = "Accept theirs"
				desc = "Local changes will be discarded."
			}
		}
		desc = styleStatus.Render(fmt.Sprintf("%s\n%d selected conflict(s)", desc, len(a.confirm.files)))
	}
	return a.renderModalBox(title, desc)
}

func (a *App) renderHelpModal() string {
	view := a.helpViewport
	view.SetContent(a.helpContent())
	view.Height = max(1, min(view.Height, len(strings.Split(a.helpContentFor(""), "\n"))))
	filtering := a.helpFilter.Value() != ""
	if filtering {
		view.Height = min(view.Height, max(1, a.height-5))
	}
	body := view.View()
	if filtering {
		filter := a.helpFilter
		filter.Prompt = "Filter ('@' for keybindings): "
		if lipgloss.Width(filter.Prompt)+8 > view.Width {
			filter.Prompt = "Filter: "
		}
		filter.Prompt = ansi.Truncate(filter.Prompt, max(0, view.Width-8), "…")
		filter.Width = max(1, view.Width-lipgloss.Width(filter.Prompt)-1)
		filter.PromptStyle = lipgloss.NewStyle()
		filter.TextStyle = lipgloss.NewStyle()
		position := filter.Position()
		filter.CursorEnd()
		filter.SetCursor(position)
		body += "\n" + styleHotkeys.Render(strings.Repeat("─", view.Width)) + "\n" + ansi.Truncate(filter.View(), view.Width, "")
	}
	style := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("4"))
	rendered := style.Width(view.Width).Height(view.Height).Render(body)
	if filtering {
		lines := strings.Split(rendered, "\n")
		lines[len(lines)-3] = styleHotkeys.Render("├" + strings.Repeat("─", view.Width) + "┤")
		rendered = strings.Join(lines, "\n")
	}
	totalW := lipgloss.Width(rendered)
	title := "Keybindings"
	if !filtering {
		hint := "(Type to filter) "
		if gap := view.Width - 3 - lipgloss.Width(title) - lipgloss.Width(hint); gap > 0 {
			title += strings.Repeat("─", gap) + hint
		}
	}
	rendered = panes.InjectTitle(rendered, "", title, totalW, true)
	position, total := a.helpPosition()
	return panes.InjectFooter(rendered, fmt.Sprintf("%d of %d", position, total), true)
}

func (a *App) fileListHasFiles() bool {
	for _, cl := range a.fileList.Changelists() {
		if len(cl.Files) > 0 {
			return true
		}
	}
	return false
}

func (a *App) helpContent() string {
	return a.helpContentFor(a.helpFilter.Value())
}

type helpRow struct {
	k, desc string
	section bool
}

func (a *App) helpRowsFor(query string) []helpRow {

	var local []helpRow
	switch a.active {
	case paneBrowser:
		local = []helpRow{
			{k: "enter / l", desc: "Expand directory"},
			{k: "h", desc: "Collapse directory"},
			{k: "space", desc: "Reconcile file or folder (edit / add / delete)"},
			{k: "a", desc: "Checkout for edit (p4 edit, supports folders)"},
			{k: "b", desc: "Toggle Workspace / Depot Browser"},
			{k: "t", desc: "Toggle tree / flat view"},
			{k: "/", desc: "Filter / search"},
			{k: "P", desc: "Force sync selected file or folder"},
		}
		if sel := a.browserPane.SelectedEntry(); sel != nil {
			if sel.LocalPath != "" {
				local = append(local, helpRow{k: "o", desc: "Reveal in file manager"})
			}
			local = append(local, helpRow{k: "u", desc: "Revert unchanged files only"})
			if !sel.IsDir {
				local = append(local, helpRow{k: "D", desc: "Mark for delete"})
			}
		}
	case paneFileList:
		local = []helpRow{
			{k: "t", desc: "Toggle tree / flat view"},
			{k: "/", desc: "Filter / search"},
		}
		if a.fileListHasFiles() {
			local = append([]helpRow{
				{k: "space", desc: "Mark / unmark file for submit"},
				{k: "enter", desc: "Open file"},
				{k: "o", desc: "Reveal in file manager"},
				{k: "l / h", desc: "Expand / collapse folder"},
				{k: "s", desc: "Submit (opens description form)"},
				{k: "e", desc: "Shelve (reverts after)"},
				{k: "E", desc: "Shelve without reverting"},
				{k: "m", desc: "Move file(s) to a different CL"},
				{k: "d", desc: "Discard (revert)"},
				{k: "u", desc: "Revert unchanged files only"},
			}, local...)
		}
		if a.resolve.HasConflicts() {
			local = append(local, helpRow{k: "R", desc: "Show conflicts"})
		}
	case paneStreams:
		local = []helpRow{
			{k: "enter / l", desc: "Switch workspace to selected stream"},
			{k: "i", desc: "Integrate (merge/copy)"},
		}
	case paneShelved:
		local = []helpRow{
			{k: "u", desc: "Unshelve + delete shelf"},
			{k: "d", desc: "Delete shelf"},
		}
	case paneResolve:
		local = []helpRow{
			{k: "enter", desc: "Open merge tool for selected file"},
			{k: "a", desc: "Automatic merge"},
			{k: "t", desc: "Accept theirs"},
			{k: "y", desc: "Accept yours"},
			{k: "s", desc: "Safe auto-resolve"},
			{k: "esc", desc: "Close conflicts pane"},
		}
	case paneLog:
		local = []helpRow{
			{k: "space / enter", desc: "Checkout workspace to selected CL"},
		}
	}

	global := []helpRow{
		{k: "j / k", desc: "Navigate"},
		{k: "J / K", desc: "Jump to bottom / top"},
		{k: "H / L", desc: "Collapse all / Expand (browser + pending)"},
		{k: "g", desc: "Toggle History / Diff pane"},
		{k: "tab", desc: "Cycle panel focus"},
		{k: "1–5", desc: "Jump to pane by number"},
		{k: "f", desc: "Fetch newer mapped changelists"},
		{k: "p", desc: "Sync selected path (browser) or workspace"},
		{k: "r", desc: "Refresh"},
		{k: "v", desc: "Select terminal text (disable mouse)"},
		{k: "q", desc: "Quit"},
		{k: "esc / ?", desc: "Close this window"},
	}
	if a.active != paneBrowser && a.active != paneResolve {
		global = append([]helpRow{{k: "esc", desc: "Back to browser"}}, global...)
	}
	if a.opRunning && a.opCancel != nil {
		global = append(global, helpRow{k: "c", desc: "Cancel operation (sync / submit)"})
	}
	if !a.isStreamDepot {
		global = append([]helpRow{{k: "i", desc: "Integrate (classic depot)"}}, global...)
	}

	var rows []helpRow
	if len(local) > 0 {
		rows = append(rows, helpRow{k: "Local", section: true})
		rows = append(rows, local...)
	}
	rows = append(rows, helpRow{k: "Global", section: true})
	rows = append(rows, global...)

	if query != "" {
		pattern := strings.TrimPrefix(query, "@")
		var matches []helpRow
		var section helpRow
		sectionAdded := false
		for _, r := range rows {
			if r.section {
				section, sectionAdded = r, false
				continue
			}
			text := r.desc
			if strings.HasPrefix(query, "@") {
				text = formatHelpKey(r.k)
			}
			if panes.FuzzyMatch(pattern, text) {
				if !sectionAdded {
					matches = append(matches, section)
					sectionAdded = true
				}
				matches = append(matches, r)
			}
		}
		rows = matches
	}
	return rows
}

func (a *App) helpContentFor(query string) string {
	rows := a.helpRowsFor(query)
	width := max(1, a.helpViewport.Width)
	if len(rows) == 0 {
		return ansi.Truncate("No matching keybindings", width, "…")
	}
	descriptionStyle := lipgloss.NewStyle()
	key := lipgloss.NewStyle().Foreground(lipgloss.Color("6"))
	hdr := lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)
	keyWidth := 0
	for i := range rows {
		if !rows[i].section {
			rows[i].k = formatHelpKey(rows[i].k)
			keyWidth = max(keyWidth, lipgloss.Width(rows[i].k))
		}
	}
	keyWidth = min(keyWidth, max(1, width-2))
	position, _ := a.helpPosition()
	binding := 0
	var sb strings.Builder
	for i, r := range rows {
		if r.section {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(ansi.Truncate(strings.Repeat(" ", keyWidth+1)+hdr.Render("─── "+r.k), width, "…"))
		} else {
			binding++
			label := ansi.Truncate(r.k, keyWidth, "…")
			label = strings.Repeat(" ", max(0, keyWidth-lipgloss.Width(label))) + label
			description := ansi.Truncate(r.desc, max(0, width-keyWidth-1), "…")
			keyStyle, descStyle := key, descriptionStyle
			if query == a.helpFilter.Value() && binding == position {
				background := lipgloss.Color("#292a2e")
				keyStyle = keyStyle.Background(background).Bold(true)
				descStyle = descStyle.Background(background).Bold(true).Width(max(0, width-keyWidth-1))
			}
			sb.WriteString(ansi.Truncate(keyStyle.Render(label+" ")+descStyle.Render(description), width, "…"))
		}
		if i < len(rows)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

func formatHelpKey(keys string) string {
	parts := strings.Split(keys, " / ")
	for i, key := range parts {
		switch key {
		case "enter", "space", "tab", "esc":
			parts[i] = "<" + key + ">"
		}
	}
	return strings.Join(parts, " / ")
}

// --- async commands ---

func (a *App) cmdInfo() tea.Cmd {
	a.infoRequest++
	a.currentCLRequest++
	a.streamsRequest++
	a.fetchRequest++
	a.shelvedRequest++
	request := a.infoRequest
	client := *a.client
	return a.withActivity("Connecting", func() tea.Msg {
		// Auto-login from keychain before fetching info.
		if err := client.EnsureLoggedIn(nil); err != nil && p4.IsAuthError(err) {
			return infoFetchedMsg{request: request, err: err}
		}
		info, err := client.Info()
		return infoFetchedMsg{request: request, info: info, err: err}
	})
}

func (a *App) cmdLoadShelved() tea.Cmd {
	a.shelvedRequest++
	request := a.shelvedRequest
	client := *a.client
	return a.withActivity("Loading shelves", func() tea.Msg {
		cls, err := client.ShelvedCLs()
		return shelvedDoneMsg{request: request, cls: cls, err: err}
	})
}

func (a *App) cmdUnshelveAndDelete(clID string) tea.Cmd {
	client := *a.client
	return a.withActivity("Unshelving", func() tea.Msg {
		err := client.UnshelveAndDelete(clID)
		return unshelveDeleteDoneMsg{clID: clID, err: err}
	})
}

func (a *App) cmdDeleteShelf(clID string) tea.Cmd {
	client := *a.client
	return a.withActivity("Deleting shelf", func() tea.Msg {
		err := client.DeleteShelf(clID)
		return deleteShelfDoneMsg{clID: clID, err: err}
	})
}

func (a *App) cmdStreams(streamPath string) tea.Cmd {
	a.streamsRequest++
	request := a.streamsRequest
	client := *a.client
	return a.withActivity("Loading streams", func() tea.Msg {
		depotPath := p4.DepotFromStream(streamPath)
		streams, err := client.Streams(depotPath)
		return streamsFetchedMsg{request: request, streams: streams, err: err}
	})
}

func (a *App) cmdFetchCurrentCL() tea.Cmd {
	a.currentCLRequest++
	request := a.currentCLRequest
	client := *a.client
	return a.withActivity("Loading revision", func() tea.Msg {
		return currentCLFetchedMsg{request: request, cl: client.CurrentCL()}
	})
}

func (a *App) cmdFetch() tea.Cmd {
	a.statusPane.SetFetching()
	a.streamsPane.SetPending(-1)
	a.fetchRequest++
	request := a.fetchRequest
	client := *a.client
	return a.withActivity("Fetching", func() tea.Msg {
		count, err := client.SyncDryRun()
		return fetchDoneMsg{request: request, count: count, err: err}
	})
}

func (a *App) refresh() tea.Cmd {
	a.refreshRequest++
	request := a.refreshRequest
	client := *a.client
	return a.withActivity("Refreshing", func() tea.Msg {
		files, err := client.OpenedFiles()
		if err != nil {
			return refreshDoneMsg{request: request, err: err}
		}
		// Mark files that need resolve.
		if conflicts, err := client.ResolveList(""); err == nil && len(conflicts) > 0 {
			// Conflict paths may be local (/tmp/root/rel) or depot (//depot/rel).
			// Opened file ClientFile is //clientname/rel.
			// Normalise both to their relative suffix for comparison.
			relPath := func(p string) string {
				if client.Root != "" {
					if rel := strings.TrimPrefix(p, client.Root+"/"); rel != p {
						return rel
					}
				}
				if strings.HasPrefix(p, "//") {
					if idx := strings.Index(p[2:], "/"); idx >= 0 {
						return p[2+idx+1:]
					}
				}
				return p
			}
			needsResolve := make(map[string]bool, len(conflicts))
			for _, c := range conflicts {
				needsResolve[relPath(c.ClientFile)] = true
			}
			for i := range files {
				if needsResolve[relPath(files[i].ClientFile)] {
					files[i].NeedsResolve = true
				}
			}
		}
		// Mark edit files that have actual local changes vs the have revision.
		diffStatus, err := client.FilesDiffStatus()
		if err != nil {
			return refreshDoneMsg{request: request, err: err}
		}
		for i := range files {
			if files[i].Action == p4.ActionEdit {
				files[i].HasChanges = diffStatus[files[i].DepotFile]
			}
		}
		cls := p4.GroupByChangelist(files)
		if descs, err := client.PendingDescriptions(); err == nil {
			for i, cl := range cls {
				if desc, ok := descs[cl.ID]; ok {
					cls[i].Description = desc
				}
			}
		}
		return refreshDoneMsg{request: request, cls: cls}
	})
}

func (a *App) cmdDiff(clientFile string) tea.Cmd {
	a.diffRequest++
	request := a.diffRequest
	client := *a.client
	a.diff.SetContent("")
	return a.withActivity("Loading diff", func() tea.Msg {
		out, err := client.Diff(clientFile)
		return diffDoneMsg{request: request, content: out, err: err}
	})
}

func (a *App) cmdSelectionDetails() tea.Cmd {
	var file string
	switch a.active {
	case paneFileList:
		if f := a.fileList.SelectedFile(); f != nil {
			file = f.ClientFile
		}
	case paneBrowser:
		if sel := a.browserPane.SelectedEntry(); sel != nil && !sel.IsDir {
			file = sel.DepotPath
		}
	default:
		return nil
	}
	var cmds []tea.Cmd
	if file != "" && (a.active == paneFileList || !a.historyMode) {
		cmds = append(cmds, a.cmdDiff(file))
	} else {
		a.diffRequest++
		a.diff.SetContent("")
	}
	if a.historyMode {
		if cmd := a.cmdFilelogForSelection(); cmd != nil {
			cmds = append(cmds, cmd)
		} else {
			a.logRequest++
			a.logPath = ""
			a.log.SetEntries(nil)
		}
	}
	return tea.Batch(cmds...)
}

func (a *App) cancelBrowserNavigation() {
	a.browserNavRequest++
	a.browserNavTarget = ""
}

func (a *App) cmdBrowserLoadFollowup(previousPath, autoExpand string, mode panes.BrowserMode) tea.Cmd {
	var load tea.Cmd
	if autoExpand != "" && a.browserNavTarget == "" {
		load = a.cmdBrowserLoad(autoExpand, mode)
	} else if a.browserNavTarget != "" {
		if a.browserPane.NavigateTo(a.browserNavTarget) {
			a.browserNavTarget = ""
		} else if next := a.browserPane.FirstUnloadedAncestor(a.browserNavTarget); next != "" {
			load = a.cmdBrowserLoad(next, mode)
		} else {
			a.browserNavTarget = ""
		}
	}
	if a.active == paneBrowser && previousPath != a.browserPane.SelectedPath() {
		return tea.Batch(load, a.cmdSelectionDetails())
	}
	return load
}

// cmdFilelogForSelection returns a filelog command for whatever is currently selected,
// used when switching panes in history mode.
func (a *App) cmdFilelogForSelection() tea.Cmd {
	if a.active == paneFileList {
		if a.fileList.SelectedIsHeader() {
			if root := a.browserPane.RootPath(); root != "" {
				return a.cmdFilelogMax(depotWildcard(root), 100)
			}
		} else if dp := a.fileList.SelectedDepotPath(); dp != "" {
			if strings.HasSuffix(dp, "/...") {
				return a.cmdFilelogMax(dp, 100)
			}
			return a.cmdFilelog(dp)
		}
	}
	if a.active == paneBrowser {
		if path := a.browserPane.SelectedPath(); path != "" {
			max := 0
			if strings.HasSuffix(path, "/...") {
				max = 100
			}
			return a.cmdFilelogMax(path, max)
		}
	}
	return nil
}

func (a *App) cmdFilelog(depotFile string) tea.Cmd {
	return a.cmdFilelogMax(depotFile, 0)
}

func (a *App) cmdFilelogMax(depotFile string, max int) tea.Cmd {
	a.logPath = depotFile
	a.logMax = max
	a.logRequest++
	request := a.logRequest
	client := *a.client
	a.log.SetEntries(nil)
	return a.withActivity("Loading history", func() tea.Msg {
		var entries []p4.FilelogEntry
		var err error
		if strings.HasSuffix(depotFile, "/...") || strings.HasSuffix(depotFile, "...") {
			entries, err = client.Changes(depotFile, max)
		} else {
			entries, err = client.Filelog(depotFile, max)
		}
		return logDoneMsg{request: request, entries: entries, err: err}
	})
}

// depotWildcard returns the recursive wildcard for a depot root path.
// Handles the special case of "//" (server root) where appending "/..." would give "///...".
func depotWildcard(root string) string {
	if root == "//" {
		return "//..."
	}
	return root + "/..."
}

func (a *App) cmdBrowserSearch(root string, mode panes.BrowserMode) tea.Cmd {
	epoch := a.browserEpoch
	client := *a.client
	return a.withActivity("Searching", func() tea.Msg {
		wildcard := depotWildcard(root)
		var files []string
		var err error
		if mode == panes.BrowserModeWorkspace {
			files, err = client.BrowserWorkspaceFiles(wildcard)
		} else {
			files, err = client.BrowserDepotFiles(wildcard)
		}
		return browserSearchDoneMsg{epoch: epoch, files: files, err: err}
	})
}

func (a *App) cmdBrowserLoad(path string, mode panes.BrowserMode) tea.Cmd {
	epoch := a.browserEpoch
	client := *a.client
	wildcard := strings.TrimSuffix(path, "/") + "/*"
	if mode == panes.BrowserModeWorkspace {
		return tea.Batch(
			a.withActivity("Loading browser", func() tea.Msg {
				dirs, files, err := client.BrowserWorkspaceFast(path)
				return browserFastMsg{epoch: epoch, parentPath: path, dirs: dirs, files: files, err: err}
			}),
			a.withActivity("Loading browser", func() tea.Msg {
				var haveFiles, depotDirs []string
				var missingFiles []p4.WorkspaceEntry
				var othersOpen map[string]bool
				var statusErr error
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					haveFiles, depotDirs, missingFiles, statusErr = client.BrowserWorkspaceStatus(path)
				}()
				go func() { defer wg.Done(); othersOpen, _ = client.OpenedByOthers(wildcard) }()
				wg.Wait()
				return browserStatusMsg{epoch: epoch, parentPath: path, haveFiles: haveFiles, depotDirs: depotDirs, missingFiles: missingFiles, othersOpen: othersOpen, err: statusErr}
			}),
		)
	}
	return a.withActivity("Loading browser", func() tea.Msg {
		var dirs, files []string
		var othersOpen map[string]bool
		var dirsErr, filesErr error
		var wg sync.WaitGroup
		wg.Add(1)
		go func() { defer wg.Done(); dirs, dirsErr = client.BrowserDirs(wildcard) }()
		if path != "//" {
			wg.Add(2)
			go func() { defer wg.Done(); othersOpen, _ = client.OpenedByOthers(wildcard) }()
			go func() { defer wg.Done(); files, filesErr = client.BrowserDepotFiles(wildcard) }()
		}
		wg.Wait()
		if dirsErr != nil {
			return browserLoadedMsg{epoch: epoch, parentPath: path, err: dirsErr}
		}
		return browserLoadedMsg{epoch: epoch, parentPath: path, dirs: dirs, files: files, othersOpen: othersOpen, err: filesErr}
	})
}

func (a *App) cmdResolveList(path string) tea.Cmd {
	a.resolvePath = path
	a.resolveRequest++
	request := a.resolveRequest
	client := *a.client
	return a.withActivity("Loading conflicts", func() tea.Msg {
		conflicts, err := client.ResolveList(path)
		return conflictsDoneMsg{request: request, conflicts: conflicts, err: err}
	})
}

func (a *App) cmdAutoResolve(files, flags []string) tea.Cmd {
	if a.opRunning || len(files) == 0 {
		return nil
	}
	a.opRunning = true
	a.opName = "Resolving"
	a.opID++
	id := a.opID
	client := *a.client
	return func() tea.Msg {
		for _, file := range files {
			if file == "" {
				return resolveDoneMsg{id: id, err: fmt.Errorf("resolve requires a selected file")}
			}
			if err := client.AutoResolve(file, flags); err != nil {
				return resolveDoneMsg{id: id, err: err}
			}
		}
		return resolveDoneMsg{id: id}
	}
}

type revertCheckMsg struct {
	id          uint64
	infoRequest uint64
	clientFile  string
	hasChanges  bool
	clID        string
}

func (a *App) cmdRevertCheck(clientFile, clID string) tea.Cmd {
	id := a.opID
	infoRequest := a.infoRequest
	client := *a.client
	return a.withActivity("Checking changes", func() tea.Msg {
		hasChanges, err := client.HasChanges(clientFile)
		if err != nil {
			// if we can't determine, assume changed and show modal
			hasChanges = true
		}
		return revertCheckMsg{id: id, infoRequest: infoRequest, clientFile: clientFile, hasChanges: hasChanges, clID: clID}
	})
}

// cmdRevertUnchangedFiles runs p4 revert -a on the client files of the given
// opened files, letting p4 decide which are actually unchanged.
func (a *App) cmdRevertUnchangedFiles(files []p4.OpenedFile) tea.Cmd {
	client := *a.client
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.ClientFile
	}
	sourceCLs := a.sourceCLsForClientFiles(paths)
	return a.withActivity("Reverting unchanged", func() tea.Msg {
		if err := client.RevertUnchangedPaths(paths); err != nil {
			return opDoneMsg{"revert unchanged failed: " + err.Error(), "p4 revert -a", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = client.DeleteChange(cl)
		}
		return revertDoneMsg{files: paths}
	})
}

// cmdRevertUnchangedPath runs p4 revert -a on a local path (supports wildcards).
func (a *App) cmdRevertUnchangedPath(localPath string) tea.Cmd {
	client := *a.client
	return a.withActivity("Reverting unchanged", func() tea.Msg {
		if err := client.RevertUnchangedPaths([]string{localPath}); err != nil {
			return opDoneMsg{"revert unchanged failed: " + err.Error(), "p4 revert -a", "error: " + err.Error()}
		}
		return revertDoneMsg{}
	})
}

func (a *App) cmdRevertUnchangedCL(clID string) tea.Cmd {
	client := *a.client
	return a.withActivity("Reverting unchanged", func() tea.Msg {
		if err := client.RevertUnchanged(clID); err != nil {
			return opDoneMsg{"revert unchanged failed: " + err.Error(), "p4 revert -a -c " + clID, "error: " + err.Error()}
		}
		return revertDoneMsg{}
	})
}

func (a *App) cmdRevert(clientFiles []string, clID string) tea.Cmd {
	client := *a.client
	sourceCLs := a.sourceCLsForClientFiles(clientFiles)
	return a.withActivity("Reverting", func() tea.Msg {
		var err error
		if clID != "" {
			_, err = client.RevertCL(clID, clientFiles)
		} else {
			_, err = client.RevertFiles(clientFiles)
		}
		if err != nil {
			return opDoneMsg{"revert failed: " + err.Error(), "p4 revert", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = client.DeleteChange(cl)
		}
		return revertDoneMsg{files: clientFiles}
	})
}

func (a *App) cmdRevertAndDeleteLocal(clientFiles, localFiles []string, clID string) tea.Cmd {
	client := *a.client
	sourceCLs := a.sourceCLsForClientFiles(clientFiles)
	return a.withActivity("Discarding", func() tea.Msg {
		result, err := client.RevertForDiscard(clID, clientFiles)
		if err != nil {
			return opDoneMsg{"revert failed: " + err.Error(), "p4 revert", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = client.DeleteChange(cl)
		}
		confirmed := make(map[string]bool, len(result.AddedLocalFiles))
		for _, local := range result.AddedLocalFiles {
			confirmed[filepath.Clean(local)] = true
		}
		for _, local := range localFiles {
			if confirmed[filepath.Clean(local)] {
				_ = os.Remove(local)
			}
		}
		return revertDoneMsg{files: result.Files}
	})
}

// clientToLocal converts a Perforce client path (//workspace/rel/path) to the
// absolute local path using the workspace root.
func clientToLocal(root, workspace, clientFile string) string {
	prefix := "//" + workspace + "/"
	rel := strings.TrimPrefix(clientFile, prefix)
	rel = strings.NewReplacer("%40", "@", "%23", "#", "%2A", "*", "%25", "%").Replace(rel)
	return filepath.Join(root, filepath.FromSlash(rel))
}

// localPathsForAdds returns local paths for files with ActionAdd from the given slice.
func localPathsForAdds(root, workspace string, files []p4.OpenedFile) []string {
	var result []string
	for _, f := range files {
		if f.Action == p4.ActionAdd {
			result = append(result, clientToLocal(root, workspace, f.ClientFile))
		}
	}
	return result
}

// sourceCLsForClientFiles returns unique non-default CL IDs that contain any of the given client paths.
func (a *App) sourceCLsForClientFiles(clientFiles []string) []string {
	fileSet := make(map[string]bool, len(clientFiles))
	for _, f := range clientFiles {
		fileSet[f] = true
	}
	seen := make(map[string]bool)
	var cls []string
	for _, cl := range a.fileList.Changelists() {
		if cl.ID == "default" {
			continue
		}
		for _, f := range cl.Files {
			if fileSet[f.ClientFile] {
				if !seen[cl.ID] {
					seen[cl.ID] = true
					cls = append(cls, cl.ID)
				}
				break
			}
		}
	}
	return cls
}

type revertDoneMsg struct {
	files []string
}

func opErrLine(err error) string {
	if errors.Is(err, context.Canceled) {
		return "\x00" + context.Canceled.Error()
	}
	if err != nil {
		return "\x00" + err.Error()
	}
	return "\x00"
}

func (a *App) cmdOpenFile(depotPath string) tea.Cmd {
	client := *a.client
	return a.withActivity("Opening file", func() tea.Msg {
		local, err := client.WhereLocal(depotPath)
		if err != nil {
			return openFileDoneMsg{err: fmt.Errorf("p4 where: %w", err)}
		}
		if err := openWithDefault(local); err != nil {
			return openFileDoneMsg{err: err}
		}
		return openFileDoneMsg{}
	})
}

// browserLocalPath returns the local filesystem path for the currently selected
// browser entry, appending /... for directories. Mapping failures have no target.
func (a *App) browserLocalPath() string {
	if sel := a.browserPane.SelectedEntry(); sel != nil {
		if sel.LocalPath != "" {
			if sel.IsDir {
				return sel.LocalPath + string(filepath.Separator) + "..."
			}
			return sel.LocalPath
		}
		if local, err := a.client.WhereLocal(sel.DepotPath); err == nil {
			return local
		}
	}
	return ""
}

func (a *App) cmdReconcile(path string) tea.Cmd {
	client := *a.client
	return a.withActivity("Reconciling", func() tea.Msg {
		_, err := client.Reconcile(path)
		result := fileActionDoneMsg{op: "reconcile", path: path, err: err}
		if err == nil {
			result.readOnlyCount, result.readOnlyErr = client.RestoreReadOnly(path)
		}
		return result
	})
}

func (a *App) cmdEdit(localPath string) tea.Cmd {
	client := *a.client
	return a.withActivity("Checking out", func() tea.Msg {
		_, err := client.Edit(localPath)
		return fileActionDoneMsg{op: "edit", path: localPath, err: err}
	})
}

type browserDeleteDoneMsg struct {
	path string
	err  error
}

func (a *App) cmdBrowserDelete(path string) tea.Cmd {
	client := *a.client
	return a.withActivity("Deleting", func() tea.Msg {
		err := client.DeletePath(path)
		return browserDeleteDoneMsg{path: path, err: err}
	})
}

func (a *App) cmdPromoteStream(source string) tea.Cmd {
	a.status = "Promoting " + source + "..."
	client := *a.client
	return a.withActivity("Promoting", func() tea.Msg {
		out, err := client.MergeStream(source)
		_ = out
		return integrateDoneMsg{op: "copy", src: source, err: err}
	})
}

func (a *App) cmdPullStream(target string) tea.Cmd {
	a.status = "Pulling into " + target + "..."
	client := *a.client
	return a.withActivity("Pulling", func() tea.Msg {
		out, err := client.CopyStream(target)
		_ = out
		return integrateDoneMsg{op: "merge", src: target, err: err}
	})
}

func (a *App) cmdIntegrateClassic(source, target string) tea.Cmd {
	client := *a.client
	a.status = "Integrating " + source + " → " + target + "..."
	return a.withActivity("Integrating", func() tea.Msg {
		out, err := client.IntegrateClassic(source, target)
		_ = out
		return integrateDoneMsg{op: "integrate", src: source + " → " + target, err: err}
	})
}

func (a *App) cmdSyncPath(path string) tea.Cmd {
	client := *a.client
	return a.withActivity("Syncing", func() tea.Msg {
		_, err := client.SyncPath(path)
		return forceSyncDoneMsg{path: path, err: err}
	})
}

func (a *App) cmdForceSync(path string) tea.Cmd {
	client := *a.client
	return a.withActivity("Force syncing", func() tea.Msg {
		_, err := client.ForceSyncPath(path)
		return forceSyncDoneMsg{path: path, err: err}
	})
}

func (a *App) cmdSyncToCL(stream, cl string) tea.Cmd {
	if a.opRunning {
		a.status = a.opName + " in progress"
		return nil
	}
	a.opRunning = true
	a.opName = "Checking out"
	a.opTotal, a.opDone = 0, 0
	a.opID++
	id := a.opID
	client := *a.client
	a.status = fmt.Sprintf("Syncing to CL %s...", cl)
	return func() tea.Msg {
		err := client.SyncToCL(stream, cl)
		return syncToCLDoneMsg{id: id, cl: cl, err: err}
	}
}

// cmdSwitchToStream moves any numbered-CL files to the default CL (p4 switch requires it),
// then switches. p4 switch handles default-CL files natively.
func (a *App) cmdSwitchToStream(stream string) tea.Cmd {
	client := *a.client
	a.status = "Switching to " + stream + "..."
	var numberedFiles []p4.OpenedFile
	for _, cl := range a.fileList.Changelists() {
		if cl.ID != "default" {
			numberedFiles = append(numberedFiles, cl.Files...)
		}
	}
	return a.withActivity("Switching stream", func() tea.Msg {
		for _, f := range numberedFiles {
			if _, err := client.Reopen("default", f.ClientFile); err != nil {
				return streamSwitchedMsg{stream: stream, err: err}
			}
		}
		err := client.SwitchToStream(stream)
		return streamSwitchedMsg{stream: stream, err: err}
	})
}

// cmdShelveForCheckout consolidates all open files into one shelf, reverts them, then syncs.
func (a *App) cmdShelveForCheckout(co *checkoutModal) tea.Cmd {
	if a.opRunning {
		a.status = a.opName + " in progress"
		return nil
	}
	client := *a.client
	a.checkout = nil
	a.status = fmt.Sprintf("Shelving open files before sync to CL %s...", co.cl)
	stream := co.stream
	cl := co.cl
	var allFiles []p4.OpenedFile
	for _, c := range a.fileList.Changelists() {
		allFiles = append(allFiles, c.Files...)
	}
	a.opRunning = true
	a.opName = "Checking out"
	a.opTotal, a.opDone = len(allFiles), 0
	a.opID++
	id := a.opID
	return func() tea.Msg {
		currentCL := client.CurrentCL()
		desc := "CL " + currentCL + " → CL " + cl
		if currentCL == "" {
			desc = "→ CL " + cl
		}
		clID, err := client.CreateChange(desc)
		if err != nil {
			return syncToCLDoneMsg{id: id, cl: cl, err: err}
		}
		for _, f := range allFiles {
			if _, err := client.Reopen(clID, f.ClientFile); err != nil {
				return syncToCLDoneMsg{id: id, cl: cl, err: err}
			}
		}
		if _, err := client.Shelve(clID); err != nil {
			return syncToCLDoneMsg{id: id, cl: cl, err: err}
		}
		clientFiles := make([]string, len(allFiles))
		for i, f := range allFiles {
			clientFiles[i] = f.ClientFile
		}
		if _, err := client.RevertCL(clID, clientFiles); err != nil {
			return syncToCLDoneMsg{id: id, cl: cl, err: err}
		}
		return syncToCLDoneMsg{id: id, cl: cl, err: client.SyncToCL(stream, cl)}
	}
}

func (a *App) cmdSyncStart() tea.Cmd {
	if a.opRunning {
		return nil
	}
	a.opRunning = true
	a.opID++
	client := *a.client
	a.opName = "Syncing"
	a.opTotal = 0
	a.opDone = 0
	if a.client.Stream != "" {
		a.syncPath = a.client.Stream + "/..."
	} else {
		a.syncPath = "//..."
	}
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		err := client.SyncStreaming(ctx, ch)
		ch <- opErrLine(err)
		close(ch)
	})
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
	ch, id := a.opCh, a.opID
	if ch == nil {
		return nil
	}
	return func() tea.Msg {
		line, ok := <-ch
		if !ok {
			return opEndMsg{id: id}
		}
		if len(line) > 0 && line[0] == '\x00' {
			errStr := line[1:]
			if errStr == context.Canceled.Error() {
				return opEndMsg{id: id, err: context.Canceled}
			}
			if errStr != "" {
				return opEndMsg{id: id, err: fmt.Errorf("%s", errStr)}
			}
			return opEndMsg{id: id}
		}
		return opLineMsg{id: id, line: line}
	}
}

// filesToMove returns files to act on for the move-CL operation.
func (a *App) filesToMove() []p4.OpenedFile {
	if marked := a.fileList.MarkedFiles(); len(marked) > 0 {
		return marked
	}
	if f := a.fileList.SelectedFile(); f != nil {
		return []p4.OpenedFile{*f}
	}
	if clID := a.fileList.SelectedCL(); clID != "" {
		return a.fileList.FilesForCL(clID)
	}
	return nil
}

func (a *App) execMoveToCL() tea.Cmd {
	if a.opRunning {
		a.status = a.opName + " in progress"
		return nil
	}
	client := *a.client
	m := a.moveModal
	a.moveModal = nil
	name := strings.TrimSpace(m.input.Value())
	files := m.files
	clientFiles := make([]string, len(files))
	for i, f := range files {
		clientFiles[i] = f.ClientFile
	}
	sourceCLs := uniqueNonDefaultCLs(files)
	return a.withActivity("Moving files", func() tea.Msg {
		clID := "default"
		if name != "" {
			if isNumeric(name) {
				clID = name
			} else {
				found, err := client.FindCLByDescription(name)
				if err != nil {
					return moveDoneMsg{err: err}
				}
				if found == "" {
					found, err = client.CreateChange(name)
					if err != nil {
						return moveDoneMsg{err: err}
					}
				}
				clID = found
			}
		}
		if _, err := client.ReopenFiles(clID, clientFiles); err != nil {
			if strings.Contains(err.Error(), "unknown") && isNumeric(clID) {
				return moveDoneMsg{err: fmt.Errorf("CL %s does not exist — a number-only input is treated as a CL ID, not a name", clID), userErr: true}
			}
			return moveDoneMsg{err: err}
		}
		for _, cl := range sourceCLs {
			_ = client.DeleteChange(cl)
		}
		return moveDoneMsg{count: len(files), clID: clID}
	})
}

// uniqueNonDefaultCLs returns deduplicated non-default CL IDs from a slice of opened files.
func uniqueNonDefaultCLs(files []p4.OpenedFile) []string {
	seen := make(map[string]bool)
	var cls []string
	for _, f := range files {
		if f.Change != "default" && !seen[f.Change] {
			seen[f.Change] = true
			cls = append(cls, f.Change)
		}
	}
	return cls
}

func (a *App) renderMoveModal() string {
	return a.renderModalBox(fmt.Sprintf("Move %d file(s) to CL", len(a.moveModal.files)), a.modalInputView(a.moveModal.input))
}

func (a *App) cmdSubmitStart(clID, description string) tea.Cmd {
	if a.opRunning {
		return nil
	}
	a.opRunning = true
	a.opID++
	client := *a.client
	a.opName = "Submitting"
	a.opTotal = 0
	a.opDone = 0
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		if err := client.RevertUnchangedContext(ctx, clID); err != nil {
			ch <- opErrLine(err)
			close(ch)
			return
		}
		err := client.Submit(ctx, clID, description, ch)
		ch <- opErrLine(err)
		close(ch)
	})
}

type submitReadyMsg struct {
	id          uint64
	ctx         context.Context
	files       []p4.OpenedFile
	description string
	err         error
}

func (a *App) cmdSubmitMarkedFilter(files []p4.OpenedFile, description string) tea.Cmd {
	if a.opRunning {
		return nil
	}
	a.opRunning = true
	a.opName = "Preparing submit"
	a.opTotal = len(files)
	a.opDone = 0
	a.opID++
	id := a.opID
	client := *a.client
	ctx, cancel := context.WithCancel(context.Background())
	a.opCancel = cancel
	return func() tea.Msg {
		ready := submitReadyMsg{id: id, ctx: ctx, description: description}
		var toSubmit []p4.OpenedFile
		var toRevert []string
		for _, f := range files {
			if err := ctx.Err(); err != nil {
				ready.err = err
				return ready
			}
			if f.Action == p4.ActionEdit {
				changed, err := client.HasChangesContext(ctx, f.ClientFile)
				if err != nil {
					ready.err = err
					return ready
				}
				if !changed {
					toRevert = append(toRevert, f.ClientFile)
					continue
				}
			}
			toSubmit = append(toSubmit, f)
		}
		if len(toRevert) > 0 {
			if err := client.RevertUnchangedPathsContext(ctx, toRevert); err != nil {
				ready.err = err
				return ready
			}
		}
		ready.files, ready.err = toSubmit, ctx.Err()
		return ready
	}
}

func (a *App) cmdSubmitMarkedStart(files []p4.OpenedFile, description string) tea.Cmd {
	client := *a.client
	a.opRunning = true
	a.opName = "Submitting"
	a.opTotal = len(files)
	a.opDone = 0
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		err := client.SubmitMarked(ctx, files, description, ch)
		ch <- opErrLine(err)
		close(ch)
	})
}

// isNumeric returns true if s consists entirely of digits.
func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

// revealInExplorer opens the file manager at the given local path, highlighting it if possible.
// On Linux, fileManager overrides auto-detection when non-empty.
func revealInExplorer(localPath, fileManager string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		winPath := filepath.FromSlash(localPath)
		cmd = exec.Command("cmd", "/c", fmt.Sprintf(`explorer /select,"%s"`, winPath))
	case "darwin":
		cmd = exec.Command("open", "-R", localPath)
	default:
		dir := localPath
		info, err := os.Stat(localPath)
		if err != nil || !info.IsDir() {
			dir = filepath.Dir(localPath)
		}
		if fileManager == "" {
			fileManager = detectLinuxFileManager()
		}
		cmd = exec.Command(fileManager, dir)
	}
	return cmd.Start()
}

// detectLinuxFileManager returns the first available file manager from a known list.
func detectLinuxFileManager() string {
	for _, fm := range []string{"nemo", "nautilus", "dolphin", "thunar", "pcmanfm", "xdg-open"} {
		if _, err := exec.LookPath(fm); err == nil {
			return fm
		}
	}
	return "xdg-open"
}

// openWithDefault launches the given local file with the OS default application in the background.
func openWithDefault(localPath string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "windows":
		winPath := filepath.FromSlash(localPath)
		ext := strings.ToLower(filepath.Ext(winPath))
		if ext == ".bat" || ext == ".cmd" {
			// Use cmd /c so the spawned window closes when the script finishes.
			cmd = exec.Command("cmd", "/c", "start", "", "cmd", "/c", winPath)
		} else {
			cmd = exec.Command("cmd", "/c", "start", "", winPath)
		}
	case "darwin":
		cmd = exec.Command("open", localPath)
	default:
		cmd = exec.Command("xdg-open", localPath)
	}
	return cmd.Start()
}
