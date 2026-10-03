package ui

import (
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

func TestMain(m *testing.M) {
	if os.Getenv("LAZYP4_UI_HELPER") == "1" {
		f, err := os.OpenFile(os.Getenv("LAZYP4_UI_COMMANDS"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
		if err != nil {
			panic(err)
		}
		fmt.Fprintln(f, strings.Join(os.Args[1:], " "))
		f.Close()
		if strings.Contains(strings.Join(os.Args[1:], " "), "change -i") {
			fmt.Println("Change 12 created.")
		}
		if strings.Contains(strings.Join(os.Args[1:], " "), "diff") && os.Getenv("LAZYP4_UI_DIFF_ERROR") == "1" {
			fmt.Fprintln(os.Stderr, "temporary diff failure")
			os.Exit(1)
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
