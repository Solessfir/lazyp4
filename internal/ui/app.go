package ui

import (
	"context"
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
	input   textinput.Model
	clID    string          // non-empty = full CL submit
	files   []p4.OpenedFile // non-empty = marked files submit
}

type confirmKind int

const (
	confirmKindRevert confirmKind = iota
	confirmKindDeleteShelf
)

type confirmModal struct {
	kind          confirmKind
	files         []string // client paths to revert
	localToDelete []string // local paths to delete after revert (ActionAdd files only)
	clID          string   // for delete shelf
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
	cl        string // target CL number
	stream    string // stream root path
	hasFiles  bool   // true = open files exist, show shelve prompt first
	shelving  bool   // true = currently in "shelve then sync" phase
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
	parentStream string // stream depots: parent of current stream (for display)
	isClassic    bool   // true = classic depot, show path inputs
	step         int    // classic only: 0=source input, 1=target input
	sourceInput  textinput.Model
	targetInput  textinput.Model
}

func newIntegrateModalStream(parent string) *integrateModal {
	return &integrateModal{parentStream: parent}
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

type browserLoadedMsg struct {
	parentPath string
	dirs       []string
	files      []string // depot mode only
	othersOpen map[string]bool
	err        error
}

type browserFastMsg struct {
	parentPath string
	dirs       []string
	files      []p4.WorkspaceEntry
	err        error
}

type browserStatusMsg struct {
	parentPath   string
	haveFiles    []string
	depotDirs    []string
	missingFiles []p4.WorkspaceEntry
	othersOpen   map[string]bool
	err          error
}

type browserSearchDoneMsg struct {
	files []string
	err   error
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

type currentCLFetchedMsg struct{ cl string }

type streamsFetchedMsg struct {
	streams []p4.StreamInfo
	err     error
}

type fetchDoneMsg struct {
	count int
	err   error
}

type tickMsg time.Time

type shelveDoneMsg struct {
	count int
	clID  string // non-empty when a whole CL was shelved
	err   error
}

type shelvedDoneMsg struct {
	cls []p4.ShelvedCL
	err error
}

type unshelveDeleteDoneMsg struct {
	clID string
	err  error
}

type deleteShelfDoneMsg struct {
	clID string
	err  error
}

type syncDryDoneMsg struct {
	total int
	err   error
}

type forceSyncDoneMsg struct {
	path string
	err  error
}

type reconcileDoneMsg struct {
	path string
	err  error
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

type opLineMsg struct{ line string }
type opEndMsg struct{ err error }

type authRequiredMsg struct{}
type authDoneMsg struct{ err error }

// App is the root bubbletea model.
type App struct {
	client          *p4.Client
	fetchInterval   time.Duration
	linuxFileManager string // empty = auto-detect
	active        activePane

	// operation in progress (sync or submit)
	opRunning bool
	opName    string // "Syncing" or "Submitting"
	opTotal   int
	opDone    int
	opCancel  context.CancelFunc
	opCh      chan string
	statusPane  *panes.StatusPane
	browserPane *panes.BrowserPane
	fileList    *panes.FileListPane
	streamsPane *panes.StreamsPane
	shelvedPane *panes.ShelvedPane
	diff        *panes.DiffPane
	log         *panes.LogPane
	resolve     *panes.ResolvePane
	cmdLog      *panes.CmdLogPane

	streams          []p4.StreamInfo
	showStreams       bool   // true when stream depot with >1 stream
	isStreamDepot    bool   // false = classic depot (no streams)
	browserNavTarget string // pending nav-to path after filter clear
	historyMode      bool   // true = show History pane at bottom-right, false = Diff
	offlineMode      bool   // true = p4 server unreachable

	width  int
	height int

	status       string
	modal        *submitModal
	shelveModal  *shelveDescModal
	moveModal    *moveCLModal
	confirm      *confirmModal
	checkout     *checkoutModal
	streamSwitch    *streamSwitchModal
	integrateModal  *integrateModal
	authModal       *authModal
	showHelp     bool
	selectMode   bool // mouse disabled so terminal can select text
	helpViewport viewport.Model
}

// New creates the root App model.
func New(client *p4.Client, fetchInterval time.Duration, linuxFileManager string) *App {
	hv := viewport.New(54, 20)
	a := &App{
		client:           client,
		fetchInterval:    fetchInterval,
		linuxFileManager: linuxFileManager,
		active:        paneBrowser,
		statusPane:  panes.NewStatusPane(),
		browserPane: panes.NewBrowserPane(),
		fileList:    panes.NewFileListPane(),
		streamsPane: panes.NewStreamsPane(),
		shelvedPane: panes.NewShelvedPane(),
		diff:        panes.NewDiffPane(),
		log:         panes.NewLogPane("5", "History"),
		resolve:     panes.NewResolvePane(),
		cmdLog:      panes.NewCmdLogPane(),
		helpViewport:  hv,
	}
	a.historyMode = true
	a.updateFocus()
	return a
}

// Init triggers the first data load and workspace info fetch.
func (a *App) Init() tea.Cmd {
	cmds := []tea.Cmd{a.refresh(), a.cmdInfo(), a.cmdFetch(), a.cmdLoadShelved()}
	if a.fetchInterval > 0 {
		cmds = append(cmds, tea.Tick(a.fetchInterval, func(t time.Time) tea.Msg { return tickMsg(t) }))
	}
	return tea.Batch(cmds...)
}

// Update is the main message handler.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
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

	// Checkout modal captures all input when open.
	if a.checkout != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			co := a.checkout
			switch m.String() {
			case "esc", "ctrl+c", "n", "N", "h":
				a.checkout = nil
				a.status = "Cancelled"
			case "enter", "y", "Y", "l":
				if co.hasFiles && !co.shelving {
					co.shelving = true
					return a, a.cmdShelveForCheckout(co)
				}
				a.checkout = nil
				return a, a.cmdSyncToCL(co.stream, co.cl)
			}
		}
		return a, nil
	}

	// Confirm modal captures all input when open.
	if a.confirm != nil {
		if m, ok := msg.(tea.KeyMsg); ok {
			switch m.String() {
			case "enter", "y", "Y", "l":
				c := a.confirm
				a.confirm = nil
				switch c.kind {
				case confirmKindRevert:
					return a, a.cmdRevert(c.files)
				case confirmKindDeleteShelf:
					return a, a.cmdDeleteShelf(c.clID)
				}
			case "d":
				if a.confirm != nil && len(a.confirm.localToDelete) > 0 {
					c := a.confirm
					a.confirm = nil
					return a, a.cmdRevertAndDeleteLocal(c.files, c.localToDelete)
				}
			case "esc", "ctrl+c", "n", "N", "h":
				a.confirm = nil
				a.status = "Cancelled"
			}
		}
		return a, nil
	}

	// Move CL modal captures all input when open.
	if a.moveModal != nil {
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

	// Shelve description modal captures all input when open.
	if a.shelveModal != nil {
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
			if f := a.fileList.SelectedFile(); f != nil {
				var cmds []tea.Cmd
				cmds = append(cmds, a.cmdDiff(f.ClientFile))
				// Only refresh history from Pending if the user is actively in that pane,
				// otherwise the browser-driven history load takes priority.
				if a.historyMode && a.active == paneFileList {
					cmds = append(cmds, a.cmdFilelog(f.DepotFile))
				}
				return a, tea.Batch(cmds...)
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
		}
		return a, nil

	case conflictsDoneMsg:
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
		return a, a.cmdResolveList("")

	case panes.ResolveAutoMsg:
		return a, func() tea.Msg {
			if err := a.client.AutoResolve("", m.Flags); err != nil {
				a.cmdLog.Add("p4 resolve", "error: "+err.Error())
				return statusMsg{"auto-resolve failed — see log"}
			}
			conflicts, err := a.client.ResolveList("")
			return conflictsDoneMsg{conflicts: conflicts, err: err}
		}

	case statusMsg:
		a.status = m.text
		return a, nil

	case opDoneMsg:
		a.status = m.status
		a.cmdLog.Add(m.cmd, m.result)
		return a, nil

	case submitReadyMsg:
		if len(m.files) == 0 {
			a.opRunning = false
			a.status = "Nothing to submit — all files were unchanged"
			return a, nil
		}
		return a, a.cmdSubmitMarkedStart(m.files, m.description)

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
		opTotal := a.opTotal
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
			fileCount := opDone
			if opName == "Submitting" {
				fileCount = opTotal
			}
			a.status = fmt.Sprintf("%s complete (%d files)", opName, fileCount)
			a.cmdLog.Add(p4cmd, fmt.Sprintf("%d files", fileCount))
		}
		if opName == "Submitting" {
			a.fileList.ClearMarks()
			cmds = append(cmds, a.cmdFetch())
		}
		cmds = append(cmds, a.refresh())
		if opName == "Syncing" {
			a.statusPane.SetFetching()
			cmds = append(cmds, a.cmdFetch())
		}
		return a, tea.Batch(cmds...)

	case revertCheckMsg:
		if m.hasChanges {
			a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{m.clientFile}}
		} else {
			return a, a.cmdRevert([]string{m.clientFile})
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
		a.log.SetCurrentCL(m.cl)
		return a, nil

	case infoFetchedMsg:
		if m.err != nil && p4.IsConnectionError(m.err) {
			a.setOffline(true)
			return a, nil
		}
		if m.err == nil {
			a.client.Root = m.info.Root
			a.client.Stream = m.info.Stream
			a.statusPane.SetInfo(m.info)
			var cmds []tea.Cmd
			cmds = append(cmds, func() tea.Msg { return currentCLFetchedMsg{cl: a.client.CurrentCL()} })
			if m.info.Stream != "" {
				a.isStreamDepot = true
				cmds = append(cmds, a.cmdStreams(m.info.Stream))
				a.browserPane.SetRoot(m.info.Stream)
				cmds = append(cmds, a.cmdBrowserLoad(m.info.Stream, a.browserPane.Mode()))
				cmds = append(cmds, a.cmdBrowserSearch(m.info.Stream, a.browserPane.Mode()))
				if a.historyMode {
					cmds = append(cmds, a.cmdFilelogMax(m.info.Stream+"/...", 100))
				}
			} else {
				// Classic depot: no streams, use // as browser root.
				// Skip search index (//... could be enormous).
				a.isStreamDepot = false
				a.browserPane.SetRoot("//")
				cmds = append(cmds, a.cmdBrowserLoad("//", a.browserPane.Mode()))
				if a.historyMode {
					cmds = append(cmds, a.cmdFilelogMax("//...", 100))
				}
			}
			if len(cmds) > 0 {
				return a, tea.Batch(cmds...)
			}
		}
		return a, nil

	case streamsFetchedMsg:
		if m.err == nil && len(m.streams) > 1 {
			a.streams = m.streams
			a.showStreams = true
			a.streamsPane.SetStreams(m.streams, a.statusPane.CurrentStream())
			a.relayout()
		}
		return a, nil

	case fetchDoneMsg:
		if m.err != nil {
			if p4.IsConnectionError(m.err) {
				a.setOffline(true)
			} else {
				a.status = "fetch error: " + m.err.Error()
				a.statusPane.SetPending(0)
			}
			a.cmdLog.Add("p4 sync -n", "error: "+m.err.Error())
		} else {
			a.setOffline(false)
			a.statusPane.SetPending(m.count)
			a.status = fmt.Sprintf("Fetch: %d CL(s) behind", m.count)
			a.cmdLog.Add("p4 changes", fmt.Sprintf("↓%d CLs behind", m.count))
		}
		return a, nil

	case panes.BrowserNeedsLoadMsg:
		return a, a.cmdBrowserLoad(m.Path, m.Mode)

	case browserLoadedMsg:
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
		autoExpand := a.browserPane.LoadChildren(m.parentPath, m.dirs, m.files, nil, yoursOpen, m.othersOpen)
		a.relayout()
		if autoExpand != "" && a.browserNavTarget == "" {
			return a, a.cmdBrowserLoad(autoExpand, a.browserPane.Mode())
		}
		if a.browserNavTarget != "" {
			if a.browserPane.NavigateTo(a.browserNavTarget) {
				a.browserNavTarget = ""
			} else if next := a.browserPane.FirstUnloadedAncestor(a.browserNavTarget); next != "" {
				return a, a.cmdBrowserLoad(next, a.browserPane.Mode())
			} else {
				a.browserNavTarget = ""
			}
		}
		return a, nil

	case browserFastMsg:
		if m.err == nil {
			autoExpand := a.browserPane.LoadChildrenFast(m.parentPath, m.dirs, m.files)
			a.relayout()
			if autoExpand != "" && a.browserNavTarget == "" {
				return a, a.cmdBrowserLoad(autoExpand, panes.BrowserModeWorkspace)
			}
			if a.browserNavTarget != "" {
				if a.browserPane.NavigateTo(a.browserNavTarget) {
					a.browserNavTarget = ""
				} else if next := a.browserPane.FirstUnloadedAncestor(a.browserNavTarget); next != "" {
					return a, a.cmdBrowserLoad(next, panes.BrowserModeWorkspace)
				} else {
					a.browserNavTarget = ""
				}
			}
		}
		return a, nil

	case browserStatusMsg:
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
		autoExpand := a.browserPane.ApplyStatus(m.parentPath, m.haveFiles, m.depotDirs, m.missingFiles, yoursOpen, m.othersOpen)
		a.relayout()
		if autoExpand != "" && a.browserNavTarget == "" {
			return a, a.cmdBrowserLoad(autoExpand, panes.BrowserModeWorkspace)
		}
		if a.browserNavTarget != "" {
			if a.browserPane.NavigateTo(a.browserNavTarget) {
				a.browserNavTarget = ""
			} else if next := a.browserPane.FirstUnloadedAncestor(a.browserNavTarget); next != "" {
				return a, a.cmdBrowserLoad(next, panes.BrowserModeWorkspace)
			} else {
				a.browserNavTarget = ""
			}
		}
		return a, nil

	case panes.BrowserNeedsSearchMsg:
		return a, a.cmdBrowserSearch(m.Root, m.Mode)

	case browserSearchDoneMsg:
		if m.err == nil {
			a.browserPane.LoadSearchIndex(m.files)
		}
		return a, nil

	case forceSyncDoneMsg:
		if m.err != nil {
			a.status = "force sync failed: " + m.err.Error()
			a.cmdLog.Add("p4 sync -f "+m.path, "error: "+m.err.Error())
		} else {
			a.status = "Force sync complete: " + m.path
			a.cmdLog.Add("p4 sync -f "+m.path, "done")
		}
		return a, a.refresh()

	case reconcileDoneMsg:
		if m.err != nil {
			a.status = "reconcile failed: " + m.err.Error()
			a.cmdLog.Add("p4 reconcile "+m.path, "error: "+m.err.Error())
		} else {
			a.status = "Reconcile complete: " + m.path
			a.cmdLog.Add("p4 reconcile "+m.path, "done")
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
		if m.err != nil {
			a.status = "checkout failed: " + m.err.Error()
			a.cmdLog.Add("p4 sync @"+m.cl, "error: "+m.err.Error())
		} else {
			a.status = fmt.Sprintf("Workspace synced to CL %s", m.cl)
			a.cmdLog.Add("p4 sync @"+m.cl, "done")
		}
		return a, tea.Batch(a.refresh(), a.cmdFetch(), a.cmdLoadShelved())

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
			return a, a.refresh()
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
			return a, tea.Batch(a.cmdInfo(), a.refresh(), a.cmdFetch(), a.cmdLoadShelved())
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
			var cmd tea.Cmd
			switch a.active {
			case paneBrowser:
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
	// Any key exits select mode and re-enables mouse.
	if a.selectMode {
		a.selectMode = false
		return a, tea.EnableMouseCellMotion
	}

	if m.String() == "?" {
		a.showHelp = !a.showHelp
		if a.showHelp {
			a.helpViewport.SetContent(a.helpContent())
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

	// When the active pane is in filter mode, route all keys to the pane so
	// global shortcuts (s, d, b, …) don't fire while typing a search term.
	// Esc is intercepted here so async nav loading can be triggered.
	if a.activePaneInFilterMode() {
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
		return a, cmd
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
		return a, tea.Quit
	case "i":
		if !a.isStreamDepot {
			a.integrateModal = newIntegrateModalClassic()
			return a, textinput.Blink
		}
		if a.active != paneStreams {
			return a, nil
		}
		if parent := a.currentStreamParent(); parent != "" {
			a.integrateModal = newIntegrateModalStream(parent)
			return a, nil
		}
		a.status = "Current stream has no parent to integrate with"
		return a, nil
	case "r":
		a.status = "Refreshing..."
		cmds := []tea.Cmd{a.refresh()}
		for _, path := range a.browserPane.LoadedDirPaths() {
			cmds = append(cmds, a.cmdBrowserLoad(path, a.browserPane.Mode()))
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
		// Remember selected file so we can navigate to it in the new mode.
		if sel := a.browserPane.SelectedEntry(); sel != nil && !sel.IsDir {
			a.browserNavTarget = sel.DepotPath
		}
		a.browserPane.ToggleMode()
		if path := a.browserPane.RootPath(); path != "" {
			return a, tea.Batch(
				a.cmdBrowserLoad(path, a.browserPane.Mode()),
				a.cmdBrowserSearch(path, a.browserPane.Mode()),
			)
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
					return a, a.cmdRevert([]string{f.ClientFile})
				}
				return a, a.cmdRevertCheck(f.ClientFile)
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
						a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{f.ClientFile}, localToDelete: []string{local}}
						return a, nil
					}
					if f.Action != p4.ActionEdit {
						return a, a.cmdRevert([]string{f.ClientFile})
					}
					return a, a.cmdRevertCheck(f.ClientFile)
				}
				files := make([]string, len(cls))
				for i, ff := range cls {
					files[i] = ff.ClientFile
				}
				a.confirm = &confirmModal{
					kind:          confirmKindRevert,
					files:         files,
					localToDelete: localPathsForAdds(a.client.Root, a.client.Workspace, cls),
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
		a.status = "Calculating sync..."
		return a, a.cmdSyncDryRun()
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
		return a, nil
	case "g":
		a.historyMode = !a.historyMode
		if a.historyMode {
			// load filelog for currently selected file/dir, or stream root
			if f := a.fileList.SelectedFile(); f != nil {
				return a, a.cmdFilelog(f.DepotFile)
			}
			if dp := a.fileList.SelectedDepotPath(); dp != "" {
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
		return a, nil
	case "right":
		a.cycleFocusForward()
		return a, nil
	case "R":
		a.status = "Checking conflicts..."
		var resolvePath string
		if f := a.fileList.SelectedFile(); f != nil {
			resolvePath = f.ClientFile
		} else if dp := a.fileList.SelectedDepotPath(); dp != "" {
			resolvePath = dp + "/..."
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
		order := a.paneOrder()
		n := int(m.String()[0]-'0') - 1
		if n >= 0 && n < len(order) {
			a.active = order[n]
			a.updateFocus()
		}
		if a.historyMode {
			return a, a.cmdFilelogForSelection()
		}
		return a, nil
	case "tab":
		a.cycleFocus()
		return a, nil
	case "esc":
		a.active = paneBrowser
		a.updateFocus()
		return a, nil
	}

	var cmd tea.Cmd
	switch a.active {
	case paneBrowser:
		switch m.String() {
		case "l":
			if loadCmd := a.browserPane.ExpandCurrent(); loadCmd != nil {
				return a, loadCmd
			}
			return a, nil
		case "h":
			a.browserPane.CollapseCurrentOrParent()
			return a, nil
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
						return a, a.cmdRevert([]string{found.ClientFile})
					}
					return a, a.cmdRevertCheck(found.ClientFile)
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
		if sel := a.browserPane.SelectedEntry(); sel != nil && !sel.IsDir {
			if a.historyMode {
				return a, tea.Batch(cmd, a.cmdFilelog(sel.DepotPath))
			}
			return a, tea.Batch(cmd, a.cmdDiff(sel.DepotPath))
		} else if a.historyMode {
			if path := a.browserPane.SelectedPath(); path != "" {
				max := 0
				if strings.HasSuffix(path, "/...") {
					max = 100
				}
				return a, tea.Batch(cmd, a.cmdFilelogMax(path, max))
			}
		}
	case paneFileList:
		switch m.String() {
		case "enter":
			if f := a.fileList.SelectedFile(); f != nil {
				return a, func() tea.Msg {
					return openFileDoneMsg{err: openWithDefault(f.ClientFile)}
				}
			}
		case "l":
			a.fileList.ExpandCurrent()
			return a, nil
		case "h":
			a.fileList.CollapseCurrentOrParent()
			return a, nil
		}
		cmd = a.fileList.Update(m)
		if a.historyMode {
			var histCmd tea.Cmd
			if hp := a.fileList.SelectedHistoryPath(); hp != "" {
				histCmd = a.cmdFilelog(hp)
			}
			if histCmd != nil {
				if f := a.fileList.SelectedFile(); f != nil {
					return a, tea.Batch(cmd, a.cmdDiff(f.ClientFile), histCmd)
				}
				return a, tea.Batch(cmd, histCmd)
			}
		}
		if f := a.fileList.SelectedFile(); f != nil {
			return a, tea.Batch(cmd, a.cmdDiff(f.ClientFile))
		}
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
		if a.historyMode && (m.String() == "space" || m.String() == "enter") {
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
		if navTarget := a.browserPane.ClearFilter(); navTarget != "" {
			a.browserNavTarget = navTarget
			if next := a.browserPane.FirstUnloadedAncestor(navTarget); next != "" {
				return a, a.cmdBrowserLoad(next, a.browserPane.Mode())
			}
		}
	case paneFileList:
		a.fileList.ClearFilter()
	}
	return a, nil
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
		switch m.String() {
		case "m":
			a.integrateModal = nil
			return a, a.cmdMergeStream("")
		case "c":
			a.integrateModal = nil
			return a, a.cmdCopyStream("")
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
		save := m.String() == "ctrl+enter"
		password := a.authModal.input.Value()
		a.authModal = nil
		a.status = "Logging in..."
		return a, func() tea.Msg {
			if err := a.client.Login(password); err != nil {
				return authDoneMsg{err: err}
			}
			if save {
				user := a.client.User
				if err := p4.KeyringSet(user, password); err != nil {
					return authDoneMsg{err: fmt.Errorf("login ok but keychain save failed: %w", err)}
				}
			}
			return authDoneMsg{}
		}
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
	m := a.shelveModal
	a.shelveModal = nil
	desc := strings.TrimSpace(m.input.Value())
	if desc == "" {
		desc = "Shelved changes"
	}
	files := m.files
	noRevert := m.noRevert
	return func() tea.Msg {
		clID, err := a.client.CreateChange(desc)
		if err != nil {
			return shelveDoneMsg{err: err}
		}
		for _, f := range files {
			if _, err := a.client.Reopen(clID, f.ClientFile); err != nil {
				return shelveDoneMsg{err: err}
			}
		}
		if _, err := a.client.Shelve(clID); err != nil {
			return shelveDoneMsg{err: err}
		}
		if !noRevert {
			clientFiles := make([]string, len(files))
			for i, f := range files {
				clientFiles[i] = f.ClientFile
			}
			if _, err := a.client.RevertFiles(clientFiles); err != nil {
				return shelveDoneMsg{err: err}
			}
		}
		return shelveDoneMsg{count: len(files)}
	}
}

func (a *App) renderShelveModal() string {
	content := styleModalTitle.Render(fmt.Sprintf("Shelve %d file(s)", len(a.shelveModal.files))) +
		"\n\n" +
		a.shelveModal.input.View() +
		"\n\n" +
		styleModalHint.Render("enter - confirm   esc - cancel")
	return styleModalBox.Render(content)
}

func (a *App) execSubmit() tea.Cmd {
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
			if contentY := y - browserTop - 1; contentY >= 0 {
				a.browserPane.SetCursor(a.browserPane.ScrollOffset() + contentY)
				if sel := a.browserPane.SelectedEntry(); sel != nil {
					if sel.IsDir {
						if a.historyMode {
							return a, a.cmdFilelogMax(sel.DepotPath, 100)
						}
					} else {
						if a.historyMode {
							return a, a.cmdFilelog(sel.DepotPath)
						}
						return a, a.cmdDiff(sel.DepotPath)
					}
				}
			}
		case a.showStreams && y >= streamsTop && y < streamsBottom:
			a.active = paneStreams
			a.updateFocus()
			if contentY := y - streamsTop - 1; contentY >= 0 {
				a.streamsPane.SetCursor(contentY)
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
		pendingH := bodyH * 2 / 5
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
				if f := a.fileList.SelectedFile(); f != nil {
					if a.historyMode {
						return a, a.cmdFilelog(f.DepotFile)
					}
					return a, a.cmdDiff(f.ClientFile)
				} else if a.historyMode {
					if hp := a.fileList.SelectedHistoryPath(); hp != "" {
						return a, a.cmdFilelog(hp)
					}
				}
			}
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
	a.browserPane.SetFocused(a.active == paneBrowser)
	a.fileList.SetFocused(a.active == paneFileList)
	a.streamsPane.SetFocused(a.active == paneStreams)
	a.shelvedPane.SetFocused(a.active == paneShelved)
	a.diff.SetFocused(a.active == paneDiff)
	a.log.SetFocused(a.active == paneLog)
	a.resolve.SetFocused(a.active == paneResolve)
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
	pendingH := bodyH * 2 / 5
	if pendingH < 4 {
		pendingH = 4
	}
	diffH := bodyH - pendingH

	a.fileList.SetSize(rightW, pendingH)
	a.diff.SetSize(rightW, diffH)
	a.log.SetSize(rightW, diffH)
	a.resolve.SetSize(rightW, diffH)
	a.cmdLog.SetWidth(a.width)

	helpW := a.width*3/5 - 2
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

	leftParts := []string{a.statusPane.View(), a.browserPane.View()}
	if a.showStreams {
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
	base := lipgloss.JoinVertical(lipgloss.Left, body, a.cmdLog.View(), a.renderHotkeys())

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
	if a.selectMode {
		hint := styleHotkeyKey.Render("Select mode") +
			styleHotkeys.Render(" - select text in terminal, then press any key to restore mouse")
		return " " + hint
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
		{"History", "g"},
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

func (a *App) renderCheckoutModal() string {
	co := a.checkout
	var content string
	if co.hasFiles && !co.shelving {
		content = styleModalTitle.Render(fmt.Sprintf("Sync to CL %s", co.cl)) +
			"\n\n" +
			styleStatus.Render("You have open files in your workspace.") +
			"\n\n" +
			styleModalHint.Render("enter / y - shelve open files then sync\nesc / n   - cancel")
	} else {
		content = styleModalTitle.Render(fmt.Sprintf("Sync workspace to CL %s?", co.cl)) +
			"\n\n" +
			styleModalHint.Render("enter - confirm   esc - cancel")
	}
	return styleModalBox.Render(content)
}

func (a *App) renderAuthModal() string {
	content := styleModalTitle.Render("Session expired") +
		"\n\n" +
		a.authModal.input.View() +
		"\n\n" +
		styleModalHint.Render("enter - login   ctrl+enter - login & remember   esc - cancel")
	return styleModalBox.Render(content)
}

// currentStreamParent returns the parent stream path of the current workspace stream,
// or "" if the current stream is a root (e.g. mainline) with no parent.
func (a *App) currentStreamParent() string {
	for _, s := range a.streams {
		if s.Path == a.client.Stream && s.Parent != "" && s.Parent != "none" {
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
		current := shortName(a.client.Stream)
		content := styleModalTitle.Render("Integrate") +
			"\n\n" +
			styleModalHint.Render(
				fmt.Sprintf("m  Pull  %s → %s\nc  Push  %s → %s\nesc  cancel",
					parent, current, current, parent),
			)
		return styleModalBox.Render(content)
	}
	// Classic depot
	title := styleModalTitle.Render("Integrate")
	var body string
	if im.step == 0 {
		body = "Source path:\n" + im.sourceInput.View() +
			"\n\n" + styleModalHint.Render("enter - next   esc - cancel")
	} else {
		body = "Source: " + im.sourceInput.Value() + "\nTarget path:\n" + im.targetInput.View() +
			"\n\n" + styleModalHint.Render("enter - integrate   esc - cancel")
	}
	return styleModalBox.Render(title + "\n\n" + body)
}

func (a *App) renderStreamSwitchModal() string {
	sw := a.streamSwitch
	streamName := sw.stream
	if idx := strings.LastIndex(streamName, "/"); idx >= 0 {
		streamName = streamName[idx+1:]
	}
	title := styleModalTitle.Render("Switch to " + streamName)
	body := styleModalHint.Render("Switching...")
	return styleModalBox.Render(title + "\n\n" + body)
}

func (a *App) renderConfirmModal() string {
	var title, desc string
	switch a.confirm.kind {
	case confirmKindDeleteShelf:
		title = "Delete shelf?"
		desc = styleStatus.Render(fmt.Sprintf("CL %s", a.confirm.clID))
	default:
		title = "Discard changes?"
		if len(a.confirm.files) == 1 {
			name := a.confirm.files[0]
			if idx := strings.LastIndexAny(name, "/\\"); idx >= 0 {
				name = name[idx+1:]
			}
			desc = styleStatus.Render(name)
		} else {
			desc = styleStatus.Render(fmt.Sprintf("%d files", len(a.confirm.files)))
		}
	}
	hint := "enter - confirm   esc - cancel"
	if len(a.confirm.localToDelete) > 0 {
		hint = "enter - revert only   d - revert + delete local   esc - cancel"
	}
	content := styleModalTitle.Render(title) +
		"\n\n" +
		desc +
		"\n\n" +
		styleModalHint.Render(hint)
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

func (a *App) fileListHasFiles() bool {
	for _, cl := range a.fileList.Changelists() {
		if len(cl.Files) > 0 {
			return true
		}
	}
	return false
}

func (a *App) helpContent() string {
	dim := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))
	key := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	hdr := lipgloss.NewStyle().Foreground(lipgloss.Color("8"))

	type row struct {
		k, desc string
		section bool
	}

	var local []row
	switch a.active {
	case paneBrowser:
		local = []row{
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
				local = append(local, row{k: "o", desc: "Reveal in file manager"})
			}
			local = append(local, row{k: "u", desc: "Revert unchanged files only"})
			if !sel.IsDir {
				local = append(local, row{k: "D", desc: "Mark for delete"})
			}
		}
	case paneFileList:
		local = []row{
			{k: "t", desc: "Toggle tree / flat view"},
			{k: "/", desc: "Filter / search"},
		}
		if a.fileListHasFiles() {
			local = append([]row{
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
			local = append(local, row{k: "R", desc: "Show conflicts"})
		}
	case paneStreams:
		local = []row{
			{k: "enter / l", desc: "Switch workspace to selected stream"},
			{k: "i", desc: "Integrate (merge/copy)"},
		}
	case paneShelved:
		local = []row{
			{k: "u", desc: "Unshelve + delete shelf"},
			{k: "d", desc: "Delete shelf"},
		}
	case paneResolve:
		local = []row{
			{k: "enter", desc: "Open merge tool for selected file"},
			{k: "a", desc: "Accept theirs (or branch)"},
			{k: "y", desc: "Accept yours"},
			{k: "s", desc: "Safe auto-resolve"},
			{k: "esc", desc: "Close conflicts pane"},
		}
	case paneLog:
		local = []row{
			{k: "space / enter", desc: "Checkout workspace to selected CL"},
		}
	}

	global := []row{
		{k: "j / k", desc: "Navigate"},
		{k: "g", desc: "Toggle History / Diff pane"},
		{k: "tab", desc: "Cycle panel focus"},
		{k: "1–6", desc: "Jump to pane by number"},
		{k: "f", desc: "Fetch (dry-run sync, shows pending count)"},
		{k: "p", desc: "Sync workspace"},
		{k: "r", desc: "Refresh"},
		{k: "v", desc: "Visual / select mode (disable mouse to select text)"},
		{k: "q", desc: "Quit"},
		{k: "?", desc: "Close this window"},
	}
	if a.active != paneBrowser && a.active != paneResolve {
		global = append([]row{{k: "esc", desc: "Back to browser"}}, global...)
	}
	if a.opRunning {
		global = append(global, row{k: "c", desc: "Cancel operation (sync / submit)"})
	}
	if !a.isStreamDepot {
		global = append([]row{{k: "i", desc: "Integrate (classic depot)"}}, global...)
	}

	var rows []row
	if len(local) > 0 {
		rows = append(rows, row{k: "Local", section: true})
		rows = append(rows, local...)
	}
	rows = append(rows, row{k: "Global", section: true})
	rows = append(rows, global...)

	var sb strings.Builder
	for i, r := range rows {
		if r.section {
			if i > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(hdr.Render(fmt.Sprintf("  ── %s ──", r.k)))
		} else {
			sb.WriteString(fmt.Sprintf("  %s  %s", key.Render(fmt.Sprintf("%-14s", r.k)), dim.Render(r.desc)))
		}
		if i < len(rows)-1 {
			sb.WriteByte('\n')
		}
	}
	return sb.String()
}

// --- async commands ---

func (a *App) cmdInfo() tea.Cmd {
	return func() tea.Msg {
		// Auto-login from keychain before fetching info.
		_ = a.client.EnsureLoggedIn(nil) // nil = no interactive prompt; modal handles that
		info, err := a.client.Info()
		if err != nil && p4.IsAuthError(err) {
			return authRequiredMsg{}
		}
		return infoFetchedMsg{info: info, err: err}
	}
}

func (a *App) cmdLoadShelved() tea.Cmd {
	return func() tea.Msg {
		cls, err := a.client.ShelvedCLs()
		return shelvedDoneMsg{cls: cls, err: err}
	}
}

func (a *App) cmdUnshelveAndDelete(clID string) tea.Cmd {
	return func() tea.Msg {
		err := a.client.UnshelveAndDelete(clID)
		return unshelveDeleteDoneMsg{clID: clID, err: err}
	}
}

func (a *App) cmdDeleteShelf(clID string) tea.Cmd {
	return func() tea.Msg {
		err := a.client.DeleteShelf(clID)
		return deleteShelfDoneMsg{clID: clID, err: err}
	}
}

func (a *App) cmdStreams(streamPath string) tea.Cmd {
	return func() tea.Msg {
		depotPath := p4.DepotFromStream(streamPath)
		streams, err := a.client.Streams(depotPath)
		return streamsFetchedMsg{streams: streams, err: err}
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
			if p4.IsAuthError(err) {
				return authRequiredMsg{}
			}
			return refreshDoneMsg{err: err}
		}
		// Mark files that need resolve.
		if conflicts, err := a.client.ResolveList(""); err == nil && len(conflicts) > 0 {
			// Conflict paths may be local (/tmp/root/rel) or depot (//depot/rel).
			// Opened file ClientFile is //clientname/rel.
			// Normalise both to their relative suffix for comparison.
			relPath := func(p string) string {
				if a.client.Root != "" {
					if rel := strings.TrimPrefix(p, a.client.Root+"/"); rel != p {
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
		diffStatus := a.client.FilesDiffStatus()
		for i := range files {
			if files[i].Action == p4.ActionEdit {
				files[i].HasChanges = diffStatus[files[i].DepotFile]
			}
		}
		cls := p4.GroupByChangelist(files)
		if descs, err := a.client.PendingDescriptions(); err == nil {
			for i, cl := range cls {
				if desc, ok := descs[cl.ID]; ok {
					cls[i].Description = desc
				}
			}
		}
		return refreshDoneMsg{cls: cls}
	}
}

func (a *App) cmdDiff(clientFile string) tea.Cmd {
	return func() tea.Msg {
		out, err := a.client.Diff(clientFile)
		return diffDoneMsg{content: out, err: err}
	}
}

// cmdFilelogForSelection returns a filelog command for whatever is currently selected,
// used when switching panes in history mode.
func (a *App) cmdFilelogForSelection() tea.Cmd {
	if a.active == paneFileList {
		if hp := a.fileList.SelectedHistoryPath(); hp != "" {
			return a.cmdFilelog(hp)
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
	return func() tea.Msg {
		var entries []p4.FilelogEntry
		var err error
		if strings.HasSuffix(depotFile, "/...") || strings.HasSuffix(depotFile, "...") {
			entries, err = a.client.Changes(depotFile, max)
		} else {
			entries, err = a.client.Filelog(depotFile, max)
		}
		return logDoneMsg{entries: entries, err: err}
	}
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
	return func() tea.Msg {
		wildcard := depotWildcard(root)
		var files []string
		var err error
		if mode == panes.BrowserModeWorkspace {
			files, err = a.client.BrowserHaveFiles(wildcard)
		} else {
			files, err = a.client.BrowserDepotFiles(wildcard)
		}
		return browserSearchDoneMsg{files: files, err: err}
	}
}

func (a *App) cmdBrowserLoad(path string, mode panes.BrowserMode) tea.Cmd {
	wildcard := path + "/*"
	if mode == panes.BrowserModeWorkspace {
		return tea.Batch(
			func() tea.Msg {
				dirs, files, err := a.client.BrowserWorkspaceFast(path)
				return browserFastMsg{parentPath: path, dirs: dirs, files: files, err: err}
			},
			func() tea.Msg {
				var haveFiles, depotDirs []string
				var missingFiles []p4.WorkspaceEntry
				var othersOpen map[string]bool
				var statusErr error
				var wg sync.WaitGroup
				wg.Add(2)
				go func() {
					defer wg.Done()
					haveFiles, depotDirs, missingFiles, statusErr = a.client.BrowserWorkspaceStatus(path)
				}()
				go func() { defer wg.Done(); othersOpen, _ = a.client.OpenedByOthers(wildcard) }()
				wg.Wait()
				return browserStatusMsg{parentPath: path, haveFiles: haveFiles, depotDirs: depotDirs, missingFiles: missingFiles, othersOpen: othersOpen, err: statusErr}
			},
		)
	}
	return func() tea.Msg {
		var dirs, files []string
		var othersOpen map[string]bool
		var dirsErr, filesErr error
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { defer wg.Done(); othersOpen, _ = a.client.OpenedByOthers(wildcard) }()
		go func() { defer wg.Done(); dirs, dirsErr = a.client.BrowserDirs(wildcard) }()
		go func() { defer wg.Done(); files, filesErr = a.client.BrowserDepotFiles(wildcard) }()
		wg.Wait()
		if dirsErr != nil {
			return browserLoadedMsg{parentPath: path, err: dirsErr}
		}
		return browserLoadedMsg{parentPath: path, dirs: dirs, files: files, othersOpen: othersOpen, err: filesErr}
	}
}


func (a *App) cmdResolveList(path string) tea.Cmd {
	return func() tea.Msg {
		conflicts, err := a.client.ResolveList(path)
		return conflictsDoneMsg{conflicts: conflicts, err: err}
	}
}

type revertCheckMsg struct {
	clientFile string
	hasChanges bool
}

func (a *App) cmdRevertCheck(clientFile string) tea.Cmd {
	return func() tea.Msg {
		hasChanges, err := a.client.HasChanges(clientFile)
		if err != nil {
			// if we can't determine, assume changed and show modal
			hasChanges = true
		}
		return revertCheckMsg{clientFile: clientFile, hasChanges: hasChanges}
	}
}

// cmdRevertUnchangedFiles runs p4 revert -a on the client files of the given
// opened files, letting p4 decide which are actually unchanged.
func (a *App) cmdRevertUnchangedFiles(files []p4.OpenedFile) tea.Cmd {
	paths := make([]string, len(files))
	for i, f := range files {
		paths[i] = f.ClientFile
	}
	sourceCLs := a.sourceCLsForClientFiles(paths)
	return func() tea.Msg {
		if err := a.client.RevertUnchangedPaths(paths); err != nil {
			return opDoneMsg{"revert unchanged failed: " + err.Error(), "p4 revert -a", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = a.client.DeleteChange(cl)
		}
		return revertDoneMsg{files: paths}
	}
}

// cmdRevertUnchangedPath runs p4 revert -a on a local path (supports wildcards).
func (a *App) cmdRevertUnchangedPath(localPath string) tea.Cmd {
	return func() tea.Msg {
		if err := a.client.RevertUnchangedPaths([]string{localPath}); err != nil {
			return opDoneMsg{"revert unchanged failed: " + err.Error(), "p4 revert -a", "error: " + err.Error()}
		}
		return revertDoneMsg{}
	}
}

func (a *App) cmdRevertUnchangedCL(clID string) tea.Cmd {
	return func() tea.Msg {
		a.client.RevertUnchanged(clID)
		return revertDoneMsg{}
	}
}

func (a *App) cmdRevert(clientFiles []string) tea.Cmd {
	sourceCLs := a.sourceCLsForClientFiles(clientFiles)
	return func() tea.Msg {
		_, err := a.client.RevertFiles(clientFiles)
		if err != nil {
			return opDoneMsg{"revert failed: " + err.Error(), "p4 revert", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = a.client.DeleteChange(cl)
		}
		return revertDoneMsg{files: clientFiles}
	}
}

func (a *App) cmdRevertAndDeleteLocal(clientFiles, localFiles []string) tea.Cmd {
	sourceCLs := a.sourceCLsForClientFiles(clientFiles)
	return func() tea.Msg {
		_, err := a.client.RevertFiles(clientFiles)
		if err != nil {
			return opDoneMsg{"revert failed: " + err.Error(), "p4 revert", "error: " + err.Error()}
		}
		for _, cl := range sourceCLs {
			_ = a.client.DeleteChange(cl)
		}
		for _, local := range localFiles {
			_ = os.Remove(local)
		}
		return revertDoneMsg{files: clientFiles}
	}
}

// clientToLocal converts a Perforce client path (//workspace/rel/path) to the
// absolute local path using the workspace root.
func clientToLocal(root, workspace, clientFile string) string {
	prefix := "//" + workspace + "/"
	rel := strings.TrimPrefix(clientFile, prefix)
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
	if err != nil {
		return "\x00" + err.Error()
	}
	return "\x00"
}

func (a *App) cmdOpenFile(depotPath string) tea.Cmd {
	return func() tea.Msg {
		local, err := a.client.WhereLocal(depotPath)
		if err != nil {
			return openFileDoneMsg{err: fmt.Errorf("p4 where: %w", err)}
		}
		if err := openWithDefault(local); err != nil {
			return openFileDoneMsg{err: err}
		}
		return openFileDoneMsg{}
	}
}

// browserLocalPath returns the local filesystem path for the currently selected
// browser entry, appending /... for directories. Falls back to the workspace
// root with /... if nothing is selected. Returns "" if no local path is available.
func (a *App) browserLocalPath() string {
	if sel := a.browserPane.SelectedEntry(); sel != nil {
		if sel.LocalPath != "" {
			if sel.IsDir {
				return sel.LocalPath + string(filepath.Separator) + "..."
			}
			return sel.LocalPath
		}
	}
	if a.client.Root != "" {
		return a.client.Root + string(filepath.Separator) + "..."
	}
	return ""
}

func (a *App) cmdReconcile(path string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.Reconcile(path)
		return reconcileDoneMsg{path: path, err: err}
	}
}

func (a *App) cmdEdit(localPath string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.Edit(localPath)
		return reconcileDoneMsg{path: localPath, err: err}
	}
}

type browserDeleteDoneMsg struct {
	path string
	err  error
}

func (a *App) cmdBrowserDelete(path string) tea.Cmd {
	return func() tea.Msg {
		err := a.client.DeletePath(path)
		return browserDeleteDoneMsg{path: path, err: err}
	}
}

func (a *App) cmdMergeStream(target string) tea.Cmd {
	a.status = "Merging from " + target + "..."
	return func() tea.Msg {
		out, err := a.client.MergeStream(target)
		_ = out
		return integrateDoneMsg{op: "merge", src: target, err: err}
	}
}

func (a *App) cmdCopyStream(target string) tea.Cmd {
	a.status = "Copying to " + target + "..."
	return func() tea.Msg {
		out, err := a.client.CopyStream(target)
		_ = out
		return integrateDoneMsg{op: "copy", src: target, err: err}
	}
}

func (a *App) cmdIntegrateClassic(source, target string) tea.Cmd {
	a.status = "Integrating " + source + " → " + target + "..."
	return func() tea.Msg {
		out, err := a.client.IntegrateClassic(source, target)
		_ = out
		return integrateDoneMsg{op: "integrate", src: source + " → " + target, err: err}
	}
}

func (a *App) cmdForceSync(path string) tea.Cmd {
	return func() tea.Msg {
		_, err := a.client.ForceSyncPath(path)
		return forceSyncDoneMsg{path: path, err: err}
	}
}

func (a *App) cmdSyncToCL(stream, cl string) tea.Cmd {
	a.status = fmt.Sprintf("Syncing to CL %s...", cl)
	return func() tea.Msg {
		err := a.client.SyncToCL(stream, cl)
		return syncToCLDoneMsg{cl: cl, err: err}
	}
}

// shelveAndRevert shelves all files in a changelist and reverts them.
func (a *App) shelveAndRevert(cl p4.Changelist) error {
	if len(cl.Files) == 0 {
		return nil
	}
	if _, err := a.client.Shelve(cl.ID); err != nil {
		return fmt.Errorf("shelve CL %s: %w", cl.ID, err)
	}
	clientFiles := make([]string, len(cl.Files))
	for i, f := range cl.Files {
		clientFiles[i] = f.ClientFile
	}
	if _, err := a.client.RevertFiles(clientFiles); err != nil {
		return fmt.Errorf("revert CL %s: %w", cl.ID, err)
	}
	return nil
}


// cmdSwitchToStream moves any numbered-CL files to the default CL (p4 switch requires it),
// then switches. p4 switch handles default-CL files natively.
func (a *App) cmdSwitchToStream(stream string) tea.Cmd {
	a.status = "Switching to " + stream + "..."
	var numberedFiles []p4.OpenedFile
	for _, cl := range a.fileList.Changelists() {
		if cl.ID != "default" {
			numberedFiles = append(numberedFiles, cl.Files...)
		}
	}
	return func() tea.Msg {
		for _, f := range numberedFiles {
			if _, err := a.client.Reopen("default", f.ClientFile); err != nil {
				return streamSwitchedMsg{stream: stream, err: err}
			}
		}
		err := a.client.SwitchToStream(stream)
		return streamSwitchedMsg{stream: stream, err: err}
	}
}



// cmdShelveForCheckout consolidates all open files into one shelf, reverts them, then syncs.
func (a *App) cmdShelveForCheckout(co *checkoutModal) tea.Cmd {
	a.checkout = nil
	a.status = fmt.Sprintf("Shelving open files before sync to CL %s...", co.cl)
	stream := co.stream
	cl := co.cl
	var allFiles []p4.OpenedFile
	for _, c := range a.fileList.Changelists() {
		allFiles = append(allFiles, c.Files...)
	}
	currentCL := a.client.CurrentCL()
	desc := "CL " + currentCL + " → CL " + cl
	if currentCL == "" {
		desc = "→ CL " + cl
	}
	return func() tea.Msg {
		clID, err := a.client.CreateChange(desc)
		if err != nil {
			return syncToCLDoneMsg{cl: cl, err: err}
		}
		for _, f := range allFiles {
			if _, err := a.client.Reopen(clID, f.ClientFile); err != nil {
				return syncToCLDoneMsg{cl: cl, err: err}
			}
		}
		if _, err := a.client.Shelve(clID); err != nil {
			return syncToCLDoneMsg{cl: cl, err: err}
		}
		clientFiles := make([]string, len(allFiles))
		for i, f := range allFiles {
			clientFiles[i] = f.ClientFile
		}
		if _, err := a.client.RevertFiles(clientFiles); err != nil {
			return syncToCLDoneMsg{cl: cl, err: err}
		}
		return syncToCLDoneMsg{cl: cl, err: a.client.SyncToCL(stream, cl)}
	}
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
	m := a.moveModal
	a.moveModal = nil
	name := strings.TrimSpace(m.input.Value())
	files := m.files
	clientFiles := make([]string, len(files))
	for i, f := range files {
		clientFiles[i] = f.ClientFile
	}
	sourceCLs := uniqueNonDefaultCLs(files)
	return func() tea.Msg {
		clID := "default"
		if name != "" {
			if isNumeric(name) {
				clID = name
			} else {
				found, err := a.client.FindCLByDescription(name)
				if err != nil {
					return moveDoneMsg{err: err}
				}
				if found == "" {
					found, err = a.client.CreateChange(name)
					if err != nil {
						return moveDoneMsg{err: err}
					}
				}
				clID = found
			}
		}
		if _, err := a.client.ReopenFiles(clID, clientFiles); err != nil {
			if strings.Contains(err.Error(), "unknown") && isNumeric(clID) {
				return moveDoneMsg{err: fmt.Errorf("CL %s does not exist — a number-only input is treated as a CL ID, not a name", clID), userErr: true}
			}
			return moveDoneMsg{err: err}
		}
		for _, cl := range sourceCLs {
			_ = a.client.DeleteChange(cl)
		}
		return moveDoneMsg{count: len(files), clID: clID}
	}
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
	content := styleModalTitle.Render(fmt.Sprintf("Move %d file(s) to CL", len(a.moveModal.files))) +
		"\n\n" +
		a.moveModal.input.View() +
		"\n\n" +
		styleModalHint.Render("enter - confirm   esc - cancel")
	return styleModalBox.Render(content)
}

func (a *App) cmdSubmitStart(clID, description string) tea.Cmd {
	a.opRunning = true
	a.opName = "Submitting"
	a.opTotal = 0
	a.opDone = 0
	return a.cmdOpStart(func(ctx context.Context, ch chan<- string) {
		a.client.RevertUnchanged(clID)
		err := a.client.SubmitStreaming(ctx, clID, description, ch)
		ch <- opErrLine(err)
		close(ch)
	})
}

type submitReadyMsg struct {
	files       []p4.OpenedFile
	description string
}

func (a *App) cmdSubmitMarkedFilter(files []p4.OpenedFile, description string) tea.Cmd {
	return func() tea.Msg {
		var toSubmit []p4.OpenedFile
		var toRevert []string
		for _, f := range files {
			if f.Action == p4.ActionEdit {
				if changed, err := a.client.HasChanges(f.ClientFile); err != nil || !changed {
					toRevert = append(toRevert, f.ClientFile)
					continue
				}
			}
			toSubmit = append(toSubmit, f)
		}
		if len(toRevert) > 0 {
			a.client.RevertFiles(toRevert)
		}
		return submitReadyMsg{files: toSubmit, description: description}
	}
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
		cmd = exec.Command("cmd", "/c", "start", "", localPath)
	case "darwin":
		cmd = exec.Command("open", localPath)
	default:
		cmd = exec.Command("xdg-open", localPath)
	}
	return cmd.Start()
}
