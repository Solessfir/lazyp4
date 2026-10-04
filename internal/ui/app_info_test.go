package ui

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestInfoRejectsObsoleteResponses(t *testing.T) {
	for _, kind := range []string{"success", "connection error", "auth error"} {
		t.Run(kind, func(t *testing.T) {
			fakeP4(t)
			a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
			a.browserPane.ToggleMode()
			oldCmd, latestCmd := a.cmdInfo(), a.cmdInfo()
			latest := unwrapActivityResult(latestCmd()).(infoFetchedMsg)
			latest.info = p4.WorkspaceInfo{Client: "workspace", Root: "/current", Stream: "//streams/current"}
			a.Update(latest)
			epoch := a.browserEpoch
			old := unwrapActivityResult(oldCmd()).(infoFetchedMsg)
			if old.request >= latest.request {
				t.Fatal("info commands did not capture separate request generations")
			}
			switch kind {
			case "success":
				old.info = p4.WorkspaceInfo{Client: "workspace", Root: "/obsolete", Stream: "//streams/obsolete"}
			case "connection error":
				old.err = errors.New("Connect to server failed")
			case "auth error":
				old.err = errors.New("Your session has expired")
			}
			_, cmd := a.Update(old)
			if unwrapActivityResult(cmd) != nil || a.browserEpoch != epoch || a.client.Root != "/current" || a.client.Stream != "//streams/current" || a.browserPane.RootPath() != "//streams/current" || a.offlineMode || a.authModal != nil {
				t.Fatal("obsolete info changed the current workspace or error state")
			}
		})
	}
}

func TestLoginInfoPreservesUnchangedBrowserState(t *testing.T) {
	for _, filter := range []bool{false, true} {
		t.Run(fmt.Sprint(filter), func(t *testing.T) {
			a := New(&p4.Client{Workspace: "workspace", Root: "/workspace", Stream: "//streams/main"}, 0, "", false)
			a.browserPane.SetSize(80, 20)
			a.diff.SetSize(80, 20)
			a.browserPane.SetRoots("//streams/main", "//workspace")
			a.browserPane.LoadChildrenFast("//workspace", []string{"//workspace/dir"}, nil)
			a.browserPane.LoadChildrenFast("//workspace/dir", nil, []p4.WorkspaceEntry{
				{ClientPath: "//workspace/dir/a.txt", DepotPath: "//streams/main/dir/a.txt"},
				{ClientPath: "//workspace/dir/b.txt", DepotPath: "//streams/main/dir/b.txt"},
			})
			a.browserPane.NavigateTo("//workspace/dir/b.txt")
			if filter {
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'b'}})
			}
			a.cmdDiff("//streams/main/dir/b.txt")
			a.cmdFilelog("//streams/main/dir/b.txt")
			a.Update(diffDoneMsg{request: a.diffRequest, content: "current diff"})
			a.Update(logDoneMsg{request: a.logRequest, entries: []p4.FilelogEntry{{Change: "current"}}})
			a.browserNavTarget = "//workspace/pending"
			a.browserNavRequest = 7
			view, selected := a.browserPane.View(), a.browserPane.SelectedPath()
			epoch, navRequest, diffID, logID := a.browserEpoch, a.browserNavRequest, a.diffRequest, a.logRequest
			modal := newSubmitModal("default", nil)
			modal.input.SetValue("description")
			a.modal = modal
			a.Update(authRequiredMsg{})
			a.authModal = nil
			a.Update(authDoneMsg{})
			a.Update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Root: "/workspace", Stream: "//streams/main"}})
			if a.browserPane.SelectedPath() != selected || a.browserPane.View() != view || a.browserEpoch != epoch || a.browserNavRequest != navRequest || a.browserNavTarget != "//workspace/pending" {
				t.Fatal("same-identity login reset browser selection, tree, filter or navigation")
			}
			if a.diffRequest != diffID || a.logRequest != logID || !strings.Contains(a.diff.View(), "current diff") || a.log.SelectedChange() != "current" || a.modal != modal || a.modal.input.Value() != "description" {
				t.Fatal("same-identity login replaced current details or modal input")
			}
		})
	}
}

func TestInfoUnchangedIdentityRetriesInitialBrowserLoad(t *testing.T) {
	a := New(&p4.Client{Workspace: "workspace", Root: "/workspace", Stream: "//streams/main"}, 0, "", false)
	a.browserPane.SetRoots("//streams/main", "//workspace")
	a.browserNavTarget = "//workspace/pending.txt"
	a.cmdInfo()
	_, cmd := a.update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Root: "/workspace", Stream: "//streams/main"}})
	if cmd == nil || len(cmd().(tea.BatchMsg)) != 6 || a.browserEpoch != 0 || a.browserNavTarget != "//workspace/pending.txt" {
		t.Fatal("same-identity info did not retry initial loading while preserving navigation")
	}
}

func TestInfoCurrentErrorsRemainVisible(t *testing.T) {
	for _, auth := range []bool{false, true} {
		t.Run(fmt.Sprint(auth), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.cmdInfo()
			err := errors.New("Connect to server failed")
			if auth {
				err = errors.New("Your session has expired")
			}
			a.Update(infoFetchedMsg{request: a.infoRequest, err: err})
			if auth && a.authModal == nil || !auth && !a.offlineMode {
				t.Fatal("current info error was hidden")
			}
		})
	}
}

func TestInfoRootResetRejectsBrowserDetails(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
			a.historyMode = history
			a.diff.SetSize(80, 20)
			a.cmdDiff("//streams/old/a.txt")
			a.cmdFilelog("//streams/old/a.txt")
			diffID, logID := a.diffRequest, a.logRequest
			a.cmdInfo()
			a.Update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Stream: "//streams/current"}})
			a.Update(diffDoneMsg{request: diffID, content: "obsolete diff"})
			a.Update(logDoneMsg{request: logID, entries: []p4.FilelogEntry{{Change: "obsolete"}}})
			if a.diffRequest <= diffID || a.logRequest <= logID || strings.Contains(a.diff.View(), "obsolete diff") || a.log.SelectedChange() == "obsolete" {
				t.Fatal("root reset accepted previous browser details")
			}
		})
	}
}

func TestInfoRootResetPreservesPendingDetails(t *testing.T) {
	a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
	a.active = paneFileList
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/pending.txt", DepotFile: "//streams/old/pending.txt"}}}})
	a.cmdDiff("//workspace/pending.txt")
	a.cmdFilelog("//streams/old/pending.txt")
	diffID, logID, logPath := a.diffRequest, a.logRequest, a.logPath
	a.cmdInfo()
	a.Update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Stream: "//streams/current"}})
	a.Update(logDoneMsg{request: logID, entries: []p4.FilelogEntry{{Change: "pending"}}})
	if a.diffRequest != diffID || a.logRequest != logID || a.logPath != logPath || a.log.SelectedChange() != "pending" {
		t.Fatal("browser root reset replaced active pending details")
	}
}

func TestInfoLoadsHistoryForEmptyPending(t *testing.T) {
	a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
	a.active = paneFileList
	a.cmdInfo()
	a.Update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Stream: "//streams/current"}})
	if a.logPath != "//streams/current/..." {
		t.Fatal("empty pending pane did not load root history")
	}
}

func TestLoginReloadsInitialData(t *testing.T) {
	fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	a.Update(authRequiredMsg{})
	a.authModal = nil
	_, cmd := a.Update(authDoneMsg{})
	if cmd == nil {
		t.Fatal("login did not resume initial loading")
	}
	batch, ok := unwrapActivityResult(cmd()).(tea.BatchMsg)
	if !ok || len(batch) != 2 {
		t.Fatal("login did not retry initial data commands")
	}
	loaded := map[string]bool{}
	for _, command := range batch {
		msg := unwrapActivityResult(command())
		loaded[fmt.Sprintf("%T", msg)] = true
		_, followup := a.Update(msg)
		if _, info := msg.(infoFetchedMsg); info && followup != nil {
			for _, metadata := range unwrapActivityResult(followup()).(tea.BatchMsg) {
				result := unwrapActivityResult(metadata())
				loaded[fmt.Sprintf("%T", result)] = true
				a.Update(result)
			}
		}
	}
	for _, kind := range []string{"ui.refreshDoneMsg", "ui.infoFetchedMsg", "ui.fetchDoneMsg", "ui.shelvedDoneMsg"} {
		if !loaded[kind] {
			t.Fatalf("login did not retry %s", kind)
		}
	}
	if a.browserPane.RootPath() == "" {
		t.Fatal("login left the startup browser uninitialized")
	}
}
