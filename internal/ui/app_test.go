package ui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
	"github.com/solessfir/lazyp4/internal/ui/panes"
)

func TestSubmitPreparationCancellation(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprint(completed), func(t *testing.T) {
			log := fakeP4(t)
			a := New(&p4.Client{}, 0, "", false)
			cmd := a.cmdSubmitMarkedFilter([]p4.OpenedFile{{ClientFile: "//workspace/added.txt", Action: p4.ActionAdd}}, "description")
			var ready submitReadyMsg
			if completed {
				ready = cmd().(submitReadyMsg)
			}
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
			if !completed {
				ready = cmd().(submitReadyMsg)
				if ready.err != context.Canceled {
					t.Fatalf("cancelled preparation returned %v", ready.err)
				}
			}
			_, next := a.Update(ready)
			if next != nil || a.opRunning || a.opCancel != nil || !strings.Contains(a.status, "cancelled") {
				t.Fatalf("cancelled preparation started submit: running=%v, command=%v, status=%q", a.opRunning, next != nil, a.status)
			}
			if data, err := os.ReadFile(log); err == nil && len(data) > 0 {
				t.Fatalf("cancelled preparation ran p4: %s", data)
			}
		})
	}
}

func TestWrappedCancellationReportsCancelled(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.opCh = make(chan string, 1)
	a.opCh <- opErrLine(fmt.Errorf("create change: %w", context.Canceled))
	msg := a.cmdReadOpLine()().(opEndMsg)
	if msg.err != context.Canceled {
		t.Fatalf("wrapped cancellation was lost: %v", msg.err)
	}
}

func TestClickSelectionRejectsPreviousDetails(t *testing.T) {
	for _, pane := range []activePane{paneBrowser, paneFileList} {
		for _, history := range []bool{false, true} {
			t.Run(fmt.Sprintf("%d/%v", pane, history), func(t *testing.T) {
				a := New(&p4.Client{}, 0, "", false)
				a.width, a.height, a.historyMode = 120, 40, history
				a.relayout()
				a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{DepotFile: "//depot/a.txt", ClientFile: "//workspace/a.txt"}}}})
				a.browserPane.SetRoots("//depot", "//workspace")
				a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"}})
				a.cmdDiff("//workspace/a.txt")
				a.cmdFilelog("//depot/a.txt")
				diffID, logID := a.diffRequest, a.logRequest
				x, y := 50, 1
				if pane == paneBrowser {
					x, y = 1, panes.StatusHeight+1
				}
				a.handleClick(x, y)
				a.Update(diffDoneMsg{request: diffID, content: "obsolete diff"})
				a.Update(logDoneMsg{request: logID, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
				if strings.Contains(a.diff.View(), "obsolete") || history && a.log.SelectedChange() == "obsolete" {
					t.Fatal("click accepted details for the previous file")
				}
			})
		}
	}
}

func TestBrowserLoadSelectionRejectsPreviousDetails(t *testing.T) {
	for _, load := range []string{"status", "fast", "depot"} {
		t.Run(load, func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.historyMode = false
			a.browserPane.SetRoots("//depot", "//workspace")
			a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"}})
			a.browserPane.NavigateTo("//workspace/a.txt")
			a.cmdDiff("//depot/a.txt")
			diffID := a.diffRequest
			var msg tea.Msg
			switch load {
			case "status":
				msg = browserStatusMsg{epoch: a.browserEpoch, parentPath: "//workspace", depotDirs: []string{"//workspace/dir"}}
			case "fast":
				msg = browserFastMsg{epoch: a.browserEpoch, parentPath: "//workspace", dirs: []string{"//workspace/dir"}, files: []p4.WorkspaceEntry{{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"}}}
			case "depot":
				msg = browserLoadedMsg{epoch: a.browserEpoch, parentPath: "//workspace", dirs: []string{"//workspace/dir"}, files: []string{"//workspace/a.txt"}}
			}
			a.Update(msg)
			if a.browserPane.SelectedEntry() == nil || !a.browserPane.SelectedEntry().IsDir {
				t.Fatal("fixture did not change the selected browser row")
			}
			a.Update(diffDoneMsg{request: diffID, content: "obsolete diff"})
			if a.diffRequest <= diffID || strings.Contains(a.diff.View(), "obsolete") {
				t.Fatal("browser load accepted details for the previous file")
			}
		})
	}
}

func TestBrowserLoadNavigationUpdatesHistory(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.browserPane.SetRoots("//depot", "//workspace")
	a.cmdFilelog("//depot/previous.txt")
	previous := a.logRequest
	a.browserNavTarget = "//workspace/a.txt"
	_, cmd := a.Update(browserFastMsg{epoch: a.browserEpoch, parentPath: "//workspace", files: []p4.WorkspaceEntry{{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"}}})
	if cmd == nil || a.browserNavTarget != "" || a.logPath != "//workspace/a.txt" {
		t.Fatalf("browser navigation did not load selected history: target=%q, log=%q", a.browserNavTarget, a.logPath)
	}
	a.Update(logDoneMsg{request: previous, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
	if a.log.SelectedChange() == "obsolete" {
		t.Fatal("browser navigation accepted previous history")
	}
}

func TestBrowserModeChangeRejectsPreviousDiff(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.diff.SetSize(80, 20)
	a.historyMode = false
	a.browserPane.SetRoots("//depot", "//workspace")
	a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"}})
	a.browserPane.NavigateTo("//workspace/a.txt")
	a.cmdSelectionDetails()
	previous := a.diffRequest
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	a.Update(diffDoneMsg{request: previous, content: "obsolete diff"})
	if strings.Contains(a.diff.View(), "obsolete") {
		t.Fatal("browser mode change accepted the previous diff")
	}
}

func TestClassicDepotRootLoadUsesRootPattern(t *testing.T) {
	log := fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	msg := a.cmdBrowserLoad("//", panes.BrowserModeDepot)().(browserLoadedMsg)
	if msg.err != nil {
		t.Fatal(msg.err)
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	commands := string(data)
	if !strings.Contains(commands, "dirs //*") || strings.Contains(commands, "files ") || strings.Contains(commands, "opened ") || strings.Contains(commands, "///*") {
		t.Fatalf("classic root used an invalid depot pattern: %s", commands)
	}
}

func TestBrowserNavigationUpdatesDetails(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.diff.SetSize(80, 20)
			a.historyMode = history
			a.browserPane.SetRoots("//depot", "//workspace")
			a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{
				{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"},
				{ClientPath: "//workspace/b.txt", DepotPath: "//depot/b.txt"},
			})
			a.browserPane.NavigateTo("//workspace/a.txt")
			a.cmdSelectionDetails()
			diffID, logID := a.diffRequest, a.logRequest
			a.Update(browserNavigateMsg{epoch: a.browserEpoch, path: "//workspace/b.txt"})
			a.Update(diffDoneMsg{request: diffID, content: "obsolete diff"})
			a.Update(logDoneMsg{request: logID, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
			if strings.Contains(a.diff.View(), "obsolete") || history && a.log.SelectedChange() == "obsolete" {
				t.Fatal("mapped browser navigation accepted details for the previous file")
			}
		})
	}
}

func TestBrowserRefreshRejectsPreviousLoad(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.browserPane.SetRoots("//depot", "//workspace")
	a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{{ClientPath: "//workspace/current.txt", DepotPath: "//depot/current.txt"}})
	a.browserPane.NavigateTo("//workspace/current.txt")
	previous := a.browserEpoch
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'r'}})
	a.Update(browserFastMsg{epoch: previous, parentPath: "//workspace", files: []p4.WorkspaceEntry{{ClientPath: "//workspace/obsolete.txt", DepotPath: "//depot/obsolete.txt"}}})
	if a.browserPane.SelectedPath() != "//workspace/current.txt" {
		t.Fatal("old browser response replaced the selected file after refresh")
	}
}

func TestPendingRefreshKeepsNewestSnapshot(t *testing.T) {
	fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	oldCmd, latestCmd := a.refresh(), a.refresh()
	old, latest := oldCmd().(refreshDoneMsg), latestCmd().(refreshDoneMsg)
	latest.cls = []p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/current.txt", DepotFile: "//depot/current.txt"}}}}
	a.Update(latest)
	a.fileList.SetCursor(1)
	a.fileList.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	a.Update(old)
	if f := a.fileList.SelectedFile(); f == nil || f.DepotFile != "//depot/current.txt" || len(a.fileList.MarkedFiles()) != 1 {
		t.Fatal("obsolete refresh replaced the current pending list or pruned its mark")
	}
	for _, err := range []error{fmt.Errorf("Connect to server failed"), fmt.Errorf("Your session has expired")} {
		old.err = err
		a.Update(old)
		if a.offlineMode || a.authModal != nil {
			t.Fatal("obsolete refresh changed connection or login state")
		}
	}
}

func TestBrowserMappingDoesNotOverrideUserNavigation(t *testing.T) {
	a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
	a.browserPane.ToggleMode()
	a.browserPane.SetRoots("//depot", "//workspace")
	a.browserPane.LoadChildren("//depot", nil, []string{"//depot/a.txt"}, nil, nil, nil)
	a.browserPane.NavigateTo("//depot/a.txt")
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
	request := a.browserNavRequest
	a.Update(browserFastMsg{epoch: a.browserEpoch, parentPath: "//workspace", files: []p4.WorkspaceEntry{
		{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"},
		{ClientPath: "//workspace/b.txt", DepotPath: "//depot/b.txt"},
	}})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j', 'j'}})
	if a.browserPane.SelectedPath() != "//workspace/b.txt" {
		t.Fatal("fixture did not select b.txt")
	}
	a.Update(browserNavigateMsg{epoch: a.browserEpoch, request: request, path: "//workspace/a.txt"})
	if a.browserPane.SelectedPath() != "//workspace/b.txt" {
		t.Fatal("delayed mapping replaced the user's newer selection")
	}
}

func TestWorkspaceSyncCanBeCancelledImmediately(t *testing.T) {
	log := fakeP4(t)
	marker := filepath.Join(t.TempDir(), "sync-started")
	t.Setenv("LAZYP4_UI_BLOCK_SYNC_MARKER", marker)
	a := New(&p4.Client{}, 0, "", false)
	a.active = paneFileList
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'p'}})
	if cmd == nil || a.opCancel == nil {
		t.Fatal("workspace sync starts without cancellation")
	}
	start := time.Now()
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Since(start) > 5*time.Second {
			a.opCancel()
			t.Fatal("native sync did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	deadline := time.After(5 * time.Second)
	for {
		result := make(chan tea.Msg, 1)
		go func() { result <- cmd() }()
		select {
		case msg := <-result:
			if end, ok := msg.(opEndMsg); ok {
				if end.err != context.Canceled {
					t.Fatalf("sync ended with %v", end.err)
				}
				a.Update(end)
				if a.opRunning || !strings.Contains(a.status, "cancelled") {
					t.Fatal("sync stayed busy after cancellation")
				}
				data, _ := os.ReadFile(log)
				if strings.Contains(string(data), "changes") {
					t.Fatal("sync ran the unused non-cancellable preflight")
				}
				return
			}
			_, cmd = a.Update(msg)
			if cmd == nil {
				t.Fatalf("sync stopped reading before completion: %T", msg)
			}
		case <-deadline:
			t.Fatal("sync did not finish after cancellation")
		}
	}
}

func TestFileActionCompletionReportsActualCommand(t *testing.T) {
	for _, op := range []string{"edit", "reconcile"} {
		for _, err := range []error{nil, fmt.Errorf("file action failed")} {
			a := New(&p4.Client{}, 0, "", true)
			a.cmdLog.SetWidth(120)
			a.Update(fileActionDoneMsg{op: op, path: "/workspace/file.txt", err: err})
			if !strings.HasPrefix(a.status, op+" ") || !strings.Contains(a.cmdLog.View(), "p4 "+op+" /workspace/file.txt") {
				t.Fatalf("%s reported a different operation: status %q, log %q", op, a.status, a.cmdLog.View())
			}
		}
	}
}

func TestMain(m *testing.M) {
	if os.Getenv("LAZYP4_UI_HELPER") == "1" {
		f, err := os.OpenFile(os.Getenv("LAZYP4_UI_COMMANDS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
		f.Close()
		if marker := os.Getenv("LAZYP4_UI_BLOCK_SYNC_MARKER"); marker != "" && strings.Contains(strings.Join(os.Args[1:], " "), "sync") {
			if err := os.WriteFile(marker, nil, 0o600); err != nil {
				panic(err)
			}
			time.Sleep(time.Minute)
		}
		if strings.Contains(strings.Join(os.Args[1:], " "), "revert") {
			fmt.Print(os.Getenv("LAZYP4_UI_REVERT_OUTPUT"))
		}
		if strings.Contains(strings.Join(os.Args[1:], " "), "change -i") {
			fmt.Println("Change 12 created.")
		}
		if strings.Contains(strings.Join(os.Args[1:], " "), "diff") && os.Getenv("LAZYP4_UI_DIFF_ERROR") == "1" {
			fmt.Fprintln(os.Stderr, "temporary diff failure")
			os.Exit(1)
		}
		if local := os.Getenv("LAZYP4_UI_WHERE_LOCAL"); local != "" && strings.Contains(strings.Join(os.Args[1:], " "), "where") {
			fmt.Printf("{\"depotFile\":\"//depot/mapped\",\"clientFile\":\"//workspace/mapped\",\"path\":%q}\n", local)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func fakeP4(t *testing.T) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(exe)
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	name := "p4"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), data, 0o700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "commands.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAZYP4_UI_HELPER", "1")
	t.Setenv("LAZYP4_UI_COMMANDS", log)
	return log
}

func TestSubmitPreparationFailsClosed(t *testing.T) {
	for _, diffError := range []bool{true, false} {
		t.Run(fmt.Sprint(diffError), func(t *testing.T) {
			log := fakeP4(t)
			if diffError {
				t.Setenv("LAZYP4_UI_DIFF_ERROR", "1")
			}
			a := New(&p4.Client{}, 0, "", false)
			files := []p4.OpenedFile{{ClientFile: "//workspace/edited.txt", Action: p4.ActionEdit}}
			cmd := a.cmdSubmitMarkedFilter(files, "description")
			if !a.opRunning || a.cmdSubmitMarkedFilter(files, "duplicate") != nil {
				t.Fatal("submit preparation did not prevent overlapping operations")
			}
			msg := cmd().(submitReadyMsg)
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			commands := string(data)
			if diffError {
				if msg.err == nil || strings.Contains(commands, "revert") {
					t.Fatalf("diff error did not preserve edits: msg=%+v, commands=%s", msg, commands)
				}
			} else if !strings.Contains(commands, "revert -a //workspace/edited.txt") || msg.err != nil {
				t.Fatalf("unchanged cleanup was not conditional: msg=%+v, commands=%s", msg, commands)
			}
			a.Update(msg)
			if a.opRunning {
				t.Fatal("preparation did not release busy state after error or empty result")
			}
		})
	}
}

func TestSelectedShelvingUsesExplicitFiles(t *testing.T) {
	for _, noRevert := range []bool{true, false} {
		t.Run(fmt.Sprint(noRevert), func(t *testing.T) {
			log := fakeP4(t)
			a := New(&p4.Client{}, 0, "", false)
			a.shelveModal = &shelveDescModal{
				input:    textinput.New(),
				files:    []p4.OpenedFile{{ClientFile: "//workspace/selected.txt", Change: "default"}},
				noRevert: noRevert,
			}
			msg := a.execShelveWithDesc()().(shelveDoneMsg)
			data, err := os.ReadFile(log)
			if err != nil || msg.err != nil {
				t.Fatalf("shelving failed: %v, %v", msg.err, err)
			}
			commands := string(data)
			if !strings.Contains(commands, "shelve -c 12 //workspace/selected.txt") {
				t.Fatalf("shelf lost explicit file scope: %s", commands)
			}
			if strings.Contains(commands, "revert //workspace/selected.txt") == noRevert {
				t.Fatalf("shelf did not honor revert preference: %s", commands)
			}
		})
	}
}

func TestChangelistDiscardKeepsConfirmedScope(t *testing.T) {
	log := fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	a.Update(revertCheckMsg{clientFile: "//workspace/selected.txt", clID: "12", hasChanges: true})
	if a.confirm == nil || a.confirm.clID != "12" {
		t.Fatal("discard confirmation lost changelist scope")
	}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("confirmed discard did not start")
	}
	if _, ok := cmd().(revertDoneMsg); !ok {
		t.Fatal("confirmed discard failed")
	}
	data, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "revert -c 12 //workspace/selected.txt") || strings.Contains(string(data), "//...") {
		t.Fatalf("discard broadened the confirmed scope: %s", data)
	}
}

func TestModalsPreserveAsyncMessages(t *testing.T) {
	modals := map[string]func(*App){
		"submit":    func(a *App) { a.modal = newSubmitModal("default", nil) },
		"move":      func(a *App) { a.moveModal = &moveCLModal{} },
		"shelve":    func(a *App) { a.shelveModal = &shelveDescModal{} },
		"checkout":  func(a *App) { a.checkout = &checkoutModal{} },
		"confirm":   func(a *App) { a.confirm = &confirmModal{} },
		"auth":      func(a *App) { a.authModal = newAuthModal() },
		"integrate": func(a *App) { a.integrateModal = newIntegrateModalClassic() },
	}
	for name, open := range modals {
		t.Run(name, func(t *testing.T) {
			a := New(&p4.Client{}, time.Second, "", false)
			open(a)
			if _, cmd := a.Update(tickMsg(time.Now())); cmd == nil {
				t.Fatal("modal stopped periodic fetch")
			}
			a.Update(tea.WindowSizeMsg{Width: 123, Height: 45})
			if a.width != 123 || a.height != 45 {
				t.Fatal("modal swallowed resize")
			}
			a.opRunning, a.opName = true, "Syncing"
			a.Update(opEndMsg{id: a.opID})
			if a.opRunning {
				t.Fatal("modal swallowed operation completion")
			}
			active := a.active
			a.Update(tea.MouseMsg{X: 1, Y: 5, Action: tea.MouseActionPress, Button: tea.MouseButtonLeft})
			if a.active != active {
				t.Fatal("mouse input reached panes behind modal")
			}
		})
	}
}

func TestModalForwardsCursorMessages(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.modal = newSubmitModal("default", nil)
	if _, cmd := a.Update(textinput.Blink()); cmd == nil {
		t.Fatal("modal did not forward cursor blink")
	}
}

func TestObsoleteResponsesCannotReplaceSelection(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.log.SetEntries([]p4.FilelogEntry{{Change: "old"}})
	a.cmdFilelog("//depot/current")
	if a.log.SelectedChange() != "" {
		t.Fatal("old checkout target remained available while loading")
	}
	a.Update(logDoneMsg{request: a.logRequest - 1, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
	if a.log.SelectedChange() != "" {
		t.Fatal("obsolete history replaced current request")
	}
	a.Update(logDoneMsg{request: a.logRequest, entries: []p4.FilelogEntry{{Change: "current"}}})
	if a.log.SelectedChange() != "current" {
		t.Fatal("current history response was ignored")
	}
	a.diff.SetSize(80, 20)
	a.cmdDiff("//depot/current")
	a.Update(diffDoneMsg{request: a.diffRequest, content: "current diff"})
	a.Update(diffDoneMsg{request: a.diffRequest - 1, content: "obsolete diff"})
	if !strings.Contains(a.diff.View(), "current diff") || strings.Contains(a.diff.View(), "obsolete diff") {
		t.Fatal("obsolete diff replaced current request")
	}
	a.browserEpoch++
	a.Update(browserSearchDoneMsg{epoch: a.browserEpoch - 1, files: []string{"//depot/obsolete"}})
}

func TestEmptyPendingSelectionInvalidatesDetails(t *testing.T) {
	for _, cause := range []string{"filter", "refresh"} {
		t.Run(cause, func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.active = paneFileList
			a.diff.SetSize(80, 20)
			a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
				{DepotFile: "//depot/A.txt", ClientFile: "//workspace/A.txt"},
			}}})
			a.fileList.SetCursor(1)
			if cause == "filter" {
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
			}
			a.cmdDiff("//workspace/A.txt")
			a.cmdFilelog("//depot/A.txt")
			diffRequest, logRequest := a.diffRequest, a.logRequest
			a.diff.SetContent("old diff")
			a.log.SetEntries([]p4.FilelogEntry{{Change: "old"}})
			if cause == "filter" {
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'z'}})
			} else {
				a.Update(refreshDoneMsg{})
			}
			if a.fileList.SelectedFile() != nil {
				t.Fatal("selection was not removed")
			}
			a.Update(diffDoneMsg{request: diffRequest, content: "obsolete diff"})
			a.Update(logDoneMsg{request: logRequest, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
			if a.log.SelectedChange() != "" || strings.Contains(a.diff.View(), "diff") {
				t.Fatal("removed selection retained or accepted obsolete details")
			}
		})
	}
}

func TestWheelSelectionLoadsCurrentDetails(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a.active = paneFileList
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
		{DepotFile: "//depot/A.txt", ClientFile: "//workspace/A.txt"},
		{DepotFile: "//depot/B.txt", ClientFile: "//workspace/B.txt"},
	}}})
	a.fileList.SetCursor(1)
	a.cmdFilelog("//depot/A.txt")
	request := a.logRequest
	_, cmd := a.Update(tea.MouseMsg{X: 60, Y: 2, Button: tea.MouseButtonWheelDown})
	if a.fileList.SelectedFile().DepotFile != "//depot/B.txt" {
		t.Fatal("wheel did not move selection")
	}
	if cmd == nil || a.logRequest <= request || a.logPath != "//depot/B.txt" {
		t.Fatal("wheel selection did not reload current history")
	}
}

func TestTreeNavigationRejectsPreviousFileDetails(t *testing.T) {
	for _, pane := range []activePane{paneBrowser, paneFileList} {
		t.Run(fmt.Sprint(pane), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", true)
			a.active = pane
			a.historyMode = false
			a.diff.SetSize(80, 20)
			a.browserPane.SetRoots("//depot", "//workspace")
			a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{
				{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"},
			})
			a.browserPane.NavigateTo("//workspace/a.txt")
			a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
				{ClientFile: "//workspace/dir/a.txt", DepotFile: "//depot/dir/a.txt"},
			}}})
			a.fileList.SetCursor(2)
			a.cmdDiff("previous file")
			request := a.diffRequest
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'h'}})
			if pane == paneFileList && a.fileList.SelectedFile() != nil || pane == paneBrowser && !a.browserPane.SelectedEntry().IsDir {
				t.Fatal("collapse did not select the parent")
			}
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'l'}})
			a.Update(diffDoneMsg{request: request, content: "obsolete diff"})
			if strings.Contains(a.diff.View(), "obsolete diff") {
				t.Fatal("parent navigation accepted previous file details")
			}
		})
	}
}

func TestSelectionPaneFocusRejectsPreviousDiff(t *testing.T) {
	for _, key := range []tea.KeyMsg{
		{Type: tea.KeyRunes, Runes: []rune{'2'}},
		{Type: tea.KeyTab},
		{Type: tea.KeyRight},
		{Type: tea.KeyLeft},
		{Type: tea.KeyEsc},
	} {
		t.Run(key.String(), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.historyMode = false
			a.diff.SetSize(80, 20)
			a.browserPane.SetRoots("//depot", "//workspace")
			a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{
				{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"},
			})
			a.browserPane.NavigateTo("//workspace/a.txt")
			a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
				{ClientFile: "//workspace/b.txt", DepotFile: "//depot/b.txt"},
			}}})
			a.fileList.SetCursor(1)
			a.active = paneBrowser
			if key.Type == tea.KeyLeft || key.Type == tea.KeyEsc {
				a.active = paneFileList
			}
			previous := a.active
			a.cmdDiff("previous file")
			request := a.diffRequest
			a.Update(key)
			if a.active == previous {
				t.Fatal("focus did not move between selection panes")
			}
			a.Update(diffDoneMsg{request: request, content: "obsolete diff"})
			if strings.Contains(a.diff.View(), "obsolete diff") {
				t.Fatal("focus change accepted previous selection's diff")
			}
		})
	}
}

func TestBrowserSearchSelectionRejectsPreviousHistory(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.browserPane.SetRoots("//depot", "//workspace")
	a.browserPane.LoadSearchIndex([]string{"//workspace/beta-old.txt"})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("beta"), Paste: true})
	request := a.logRequest
	a.Update(browserSearchDoneMsg{epoch: a.browserEpoch, files: []string{"//workspace/beta-new.txt"}})
	if a.browserPane.SelectedPath() != "//workspace/beta-new.txt" {
		t.Fatal("search completion did not change the selected result")
	}
	a.Update(logDoneMsg{request: request, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
	if a.log.SelectedChange() != "" || a.logPath != "//workspace/beta-new.txt" {
		t.Fatal("search completion retained previous selection's history")
	}
}

func TestBrowserHistoryToDiffUsesCurrentSelection(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.diff.SetSize(80, 20)
	a.browserPane.SetRoots("//depot", "//workspace")
	a.browserPane.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{
		{ClientPath: "//workspace/a.txt", DepotPath: "//depot/a.txt"},
		{ClientPath: "//workspace/b.txt", DepotPath: "//depot/b.txt"},
	})
	a.browserPane.NavigateTo("//workspace/a.txt")
	a.cmdDiff("//depot/a.txt")
	request := a.diffRequest
	a.Update(tea.KeyMsg{Type: tea.KeyDown})
	if a.browserPane.SelectedPath() != "//workspace/b.txt" {
		t.Fatal("history navigation did not select B")
	}
	a.Update(diffDoneMsg{request: request, content: "obsolete diff"})
	if strings.Contains(a.diff.View(), "obsolete diff") {
		t.Fatal("history navigation accepted previous selection's hidden diff")
	}
	request = a.diffRequest
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'g'}})
	if a.historyMode || cmd == nil || a.diffRequest <= request {
		t.Fatal("switching to diff did not request current file details")
	}
}

func TestKeyBurstPreservesShortcutsAndModalInput(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
		{DepotFile: "//depot/A.txt", ClientFile: "//workspace/A.txt"},
	}}})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("2j sdescription")})
	if a.active != paneFileList || a.modal == nil || len(a.modal.files) != 1 || a.modal.files[0].DepotFile != "//depot/A.txt" {
		t.Fatal("key burst did not focus, select, mark and open selected-file submit")
	}
	if a.modal.input.Value() != "description" {
		t.Fatalf("key burst lost modal text: %q", a.modal.input.Value())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(" pasted"), Paste: true})
	if a.modal.input.Value() != "description pasted" {
		t.Fatalf("modal lost pasted text: %q", a.modal.input.Value())
	}
}

func TestPendingOpenUsesCapturedLocalMapping(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("uses the Linux default opener")
	}
	log := fakeP4(t)
	helper, err := os.ReadFile(filepath.Join(filepath.Dir(log), "p4"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(filepath.Dir(log), "xdg-open"), helper, 0o700); err != nil {
		t.Fatal(err)
	}
	local := filepath.Join(t.TempDir(), "remapped", "a#@%.txt")
	t.Setenv("LAZYP4_UI_WHERE_LOCAL", local)
	a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
	a.active = paneFileList
	depot := "//depot/other/a%23%40%25.txt"
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
		{DepotFile: depot, ClientFile: "//workspace/local/a%23%40%25.txt"},
	}}})
	a.fileList.SetCursor(1)
	selected := a.fileList.SelectedFile()
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("pending Enter did not start an open command")
	}
	selected.DepotFile, selected.ClientFile = "//depot/replacement.txt", "//workspace/replacement.txt"
	a.client.Workspace = "replacement"
	if msg := cmd().(openFileDoneMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	var commands string
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		data, _ := os.ReadFile(log)
		commands = string(data)
		if strings.Contains(commands, local) {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if !strings.Contains(commands, "-c workspace where "+depot) || !strings.Contains(commands, local) || strings.Contains(commands, "replacement") {
		t.Fatalf("open lost the captured selection or local mapping: %s", commands)
	}
}

func TestDiscardAddedFileDeletesDecodedLocalPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows filenames cannot contain an asterisk")
	}
	log := fakeP4(t)
	root := t.TempDir()
	rel := "dir%40%23%25/new%40%23%2540%2A.txt"
	local := filepath.Join(root, "dir@#%", "new@#%40*.txt")
	encoded := filepath.Join(root, filepath.FromSlash(rel))
	for _, file := range []string{local, encoded} {
		if err := os.MkdirAll(filepath.Dir(file), 0o700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(file, []byte("keep until confirmed"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	a := New(&p4.Client{Root: root, Workspace: "workspace"}, 0, "", false)
	a.active = paneFileList
	clientFile := "//workspace/" + rel
	t.Setenv("LAZYP4_UI_REVERT_OUTPUT", fmt.Sprintf("{\"oldAction\":\"add\",\"action\":\"abandoned\",\"clientFile\":%q}\n", local))
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
		{DepotFile: "//depot/" + rel, ClientFile: clientFile, Action: p4.ActionAdd},
	}}})
	a.fileList.SetCursor(1)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}})
	if cmd == nil {
		t.Fatal("confirmed local deletion did not start")
	}
	if _, ok := cmd().(revertDoneMsg); !ok {
		t.Fatal("added file revert did not finish")
	}
	if _, err := os.Stat(local); !os.IsNotExist(err) {
		t.Fatalf("added file remained at its decoded local path: %v", err)
	}
	if _, err := os.Stat(encoded); err != nil {
		t.Fatalf("unrelated literal encoded path was deleted: %v", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "revert "+clientFile) {
		t.Fatalf("revert lost encoded Perforce path: %s, %v", commands, err)
	}
}

func TestOperationReadsCapturedChannel(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.opID = 1
	a.opCh = make(chan string, 1)
	a.opCh <- "original"
	cmd := a.cmdReadOpLine()
	a.opID = 2
	a.opCh = make(chan string, 1)
	a.opCh <- "replacement"
	msg := cmd().(opLineMsg)
	if msg.id != 1 || msg.line != "original" {
		t.Fatalf("read moved to a different operation: %+v", msg)
	}
	a.opRunning = true
	a.Update(opEndMsg{id: 1})
	if !a.opRunning {
		t.Fatal("obsolete completion cleared current operation")
	}
}

func TestOperationCountsFilesInsteadOfStatusLines(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.opRunning, a.opName = true, "Submitting"
	for _, line := range []string{"Submitting change 2.", "edit //depot/file with spaces.txt#2", "Change 2 submitted."} {
		a.Update(opLineMsg{id: a.opID, line: line})
	}
	if a.opDone != 1 {
		t.Fatalf("submission counted %d files from status output", a.opDone)
	}
	a.Update(opEndMsg{id: a.opID})
	if a.status != "Submitting complete (1 files)" {
		t.Fatalf("incorrect completed submission count: %s", a.status)
	}
}

func TestDestructiveResolveRequiresScopedConfirmation(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	files := []string{"//workspace/one.txt"}
	if _, cmd := a.Update(panes.ResolveAutoMsg{Files: files, Flags: []string{"-at"}}); cmd != nil || a.confirm == nil {
		t.Fatal("accept-theirs bypassed confirmation")
	}
	if a.confirm.kind != confirmKindResolve || len(a.confirm.files) != 1 || a.confirm.files[0] != files[0] {
		t.Fatal("confirmation lost conflict scope")
	}
	if a.cmdAutoResolve(nil, []string{"-am"}) != nil {
		t.Fatal("auto resolve accepted empty scope")
	}
}

func TestOperationGuardsAllowCancellation(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.opRunning, a.opName = true, "Syncing"
	cancelled := false
	a.opCancel = func() { cancelled = true }
	a.handleKey(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !cancelled {
		t.Fatal("busy guard swallowed cancellation")
	}
	for _, pane := range []activePane{paneStreams, paneLog} {
		a.active = pane
		if _, cmd := a.handleKey(tea.KeyMsg{Type: tea.KeyEnter}); cmd != nil {
			t.Fatal("operation allowed stream switching or checkout")
		}
	}
}
