package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestDiscardMenuDescribesSelectedChoice(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.width, a.height = 100, 40
	a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/selected.txt"}, localToDelete: []string{"selected.txt"}}
	for cursor, description := range []string{
		"Keep local files opened for add on disk.",
		"Delete local files opened for add after reverting them.",
		"Keep local changes in '//workspace/selected.txt'.",
	} {
		a.confirm.cursor = cursor
		view := ansi.Strip(a.renderDiscardModal())
		prose := strings.Join(strings.Fields(strings.ReplaceAll(view, "│", "")), " ")
		if !strings.Contains(view, fmt.Sprintf("%d of 3", cursor+1)) || strings.Count(view, "╭") != 2 || !strings.Contains(view, "\n\n") {
			t.Fatalf("discard menu lost its choice counter or separate description: %q", view)
		}
		if !strings.Contains(prose, description) || !strings.Contains(view, "x Discard changes") || !strings.Contains(view, "d Discard and delete added files") || !strings.Contains(view, "Cancel") {
			t.Fatalf("discard menu does not describe choice %d: %q", cursor, view)
		}
	}
	a.confirm.localToDelete = nil
	a.confirm.cursor = 1
	view := ansi.Strip(a.renderDiscardModal())
	if !strings.Contains(view, "2 of 2") || strings.Contains(view, "Discard and delete added files") || !strings.Contains(view, "Keep local changes") {
		t.Fatalf("discard without added files retained its delete choice: %q", view)
	}
}

func TestDiscardMenuFitsUnicodeAndNarrowBounds(t *testing.T) {
	for _, width := range []int{20, 40, 100} {
		for _, height := range []int{12, 40} {
			t.Run(fmt.Sprintf("%dx%d", width, height), func(t *testing.T) {
				a := New(&p4.Client{}, 0, "", false)
				a.width, a.height = width, height
				a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/" + strings.Repeat("特🌿é", 20)}, localToDelete: []string{"added.txt"}}
				for cursor := 0; cursor < 3; cursor++ {
					a.confirm.cursor = cursor
					view := a.renderDiscardModal()
					lines := strings.Split(view, "\n")
					popupWidth := lipgloss.Width(lines[0])
					if !utf8.ValidString(view) || len(lines) > height {
						t.Fatalf("discard menu has invalid Unicode or exceeds height: %q", view)
					}
					for i, line := range lines {
						if ansi.Strip(line) != "" && lipgloss.Width(line) != popupWidth {
							t.Fatalf("choice %d line %d width %d differs from popup width %d: %q", cursor, i, lipgloss.Width(line), popupWidth, ansi.Strip(line))
						}
						if lipgloss.Width(line) > width-2 {
							t.Fatalf("choice %d line %d exceeds modal bounds: width %d, available %d: %q", cursor, i, lipgloss.Width(line), width-2, ansi.Strip(line))
						}
					}
				}
			})
		}
	}
}

func TestDiscardMenuNavigationOnlyChangesSelection(t *testing.T) {
	for _, added := range []bool{false, true} {
		t.Run(fmt.Sprint(added), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			c := &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/selected.txt"}, clID: "41"}
			a.confirm = c
			last := 1
			if added {
				c.localToDelete = []string{"selected.txt"}
				last = 2
			}
			for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'j'}}, {Type: tea.KeyDown}, {Type: tea.KeyTab}} {
				c.cursor = 0
				for i := 0; i < 4; i++ {
					_, cmd := a.handleDiscardKey(key)
					if cmd != nil || a.confirm != c || c.cursor != min(i+1, last) || c.clID != "41" {
						t.Fatal("forward navigation ran an action or changed frozen scope")
					}
				}
			}
			for _, key := range []tea.KeyMsg{{Type: tea.KeyRunes, Runes: []rune{'k'}}, {Type: tea.KeyUp}, {Type: tea.KeyShiftTab}} {
				c.cursor = last
				for i := 0; i < 4; i++ {
					_, cmd := a.handleDiscardKey(key)
					if cmd != nil || a.confirm != c || c.cursor != max(0, last-i-1) {
						t.Fatal("backward navigation ran an action or exceeded menu bounds")
					}
				}
			}
		})
	}
}

func TestDiscardMenuCancelDoesNotCancelRunningOperation(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyEsc}, {Type: tea.KeyRunes, Runes: []rune{'n'}}} {
		a := New(&p4.Client{}, 0, "", false)
		a.confirm = &confirmModal{kind: confirmKindRevert, cursor: 2, files: []string{"//workspace/selected.txt"}, localToDelete: []string{"selected.txt"}}
		a.opRunning, a.opName, a.opID = true, "Submitting", 7
		cancelled := false
		a.opCancel = func() { cancelled = true }
		_, cmd := a.Update(key)
		if unwrapActivityResult(cmd) != nil || a.confirm != nil || !a.opRunning || a.opID != 7 || cancelled {
			t.Fatal("cancel choice cancelled the running operation or failed to close the menu")
		}
	}
}

func TestDiscardMenuBusyChoicePreservesScopeThroughCompletionAndResize(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	c := &confirmModal{kind: confirmKindRevert, cursor: 1, files: []string{"//workspace/selected.txt"}, localToDelete: []string{"selected.txt"}, clID: "41"}
	a.confirm = c
	a.opRunning, a.opName, a.opID = true, "Submitting", 7
	a.opCancel = func() {}
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	if unwrapActivityResult(cmd) != nil || a.confirm != c || c.cursor != 1 {
		t.Fatal("busy destructive choice ran or changed selection")
	}
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	a.Update(opEndMsg{id: 7})
	if a.confirm != c || c.cursor != 1 || c.clID != "41" || c.files[0] != "//workspace/selected.txt" || c.localToDelete[0] != "selected.txt" || a.opRunning {
		t.Fatal("operation completion or resize replaced the frozen confirmation")
	}
}

func TestDiscardMenuDeleteChoiceExecutesCapturedScope(t *testing.T) {
	log := fakeP4(t)
	root := t.TempDir()
	added, edited := filepath.Join(root, "added.txt"), filepath.Join(root, "edited.txt")
	for _, file := range []string{added, edited} {
		if err := os.WriteFile(file, []byte("local content"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("LAZYP4_UI_REVERT_OUTPUT", fmt.Sprintf("{\"oldAction\":\"add\",\"action\":\"abandoned\",\"clientFile\":%q}\n{\"oldAction\":\"edit\",\"action\":\"reverted\",\"clientFile\":%q}\n", added, edited))
	a := New(&p4.Client{Workspace: "workspace", Root: root}, 0, "", false)
	a.confirm = &confirmModal{kind: confirmKindRevert, cursor: 1, files: []string{"//workspace/added.txt", "//workspace/edited.txt"}, localToDelete: []string{added}, clID: "41"}
	a.fileList.SetChangelists([]p4.Changelist{{ID: "99", Files: []p4.OpenedFile{{ClientFile: "//workspace/other.txt"}}}})
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
	msg := unwrapActivityResult(cmd)
	if done, ok := msg.(revertDoneMsg); !ok || len(done.files) != 2 || a.confirm != nil {
		t.Fatalf("selected delete choice did not execute: %#v", msg)
	}
	commands, err := os.ReadFile(log)
	if err != nil || !strings.Contains(string(commands), "revert -c 41 //workspace/added.txt //workspace/edited.txt") || strings.Contains(string(commands), "other.txt") {
		t.Fatalf("delete choice lost its captured changelist and files: %s, error %v", commands, err)
	}
	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Fatalf("confirmed added file remains after delete choice: %v", err)
	}
	if _, err := os.Stat(edited); err != nil {
		t.Fatalf("delete choice removed an edited file: %v", err)
	}
}

func TestDiscardMenuLegacyDoubleDDeletesAddedFile(t *testing.T) {
	fakeP4(t)
	root := t.TempDir()
	added := filepath.Join(root, "added.txt")
	if err := os.WriteFile(added, []byte("local content"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LAZYP4_UI_REVERT_OUTPUT", fmt.Sprintf("{\"oldAction\":\"add\",\"action\":\"abandoned\",\"clientFile\":%q}\n", added))
	a := New(&p4.Client{Workspace: "workspace", Root: root}, 0, "", false)
	a.active = paneFileList
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/added.txt", Action: p4.ActionAdd}}}})
	a.fileList.SetCursor(1)
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'d'}}
	_, cmd := a.Update(key)
	if unwrapActivityResult(cmd) != nil || a.confirm == nil || a.confirm.cursor != 0 {
		t.Fatal("first d did not open the discard menu")
	}
	_, cmd = a.Update(key)
	if _, ok := unwrapActivityResult(cmd).(revertDoneMsg); !ok || a.confirm != nil {
		t.Fatal("second d did not accept the delete choice")
	}
	if _, err := os.Stat(added); !os.IsNotExist(err) {
		t.Fatalf("legacy dd did not delete the confirmed added file: %v", err)
	}
}

func TestDiscardMenuTitleHasNoQuestionMark(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.width, a.height = 120, 40
	a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/src/file.txt"}}
	title := strings.Split(ansi.Strip(a.renderDiscardModal()), "\n")[0]
	if !strings.HasPrefix(title, "╭───Discard changes─") || strings.Contains(title, "?") {
		t.Fatalf("discard title = %q", title)
	}
}

func TestDiscardMenuDisplayPathsAreRelativeWithoutChangingScope(t *testing.T) {
	root := t.TempDir()
	for _, test := range []struct {
		name string
		path string
		want string
	}{
		{name: "client alias", path: "//workspace/src/特.txt", want: "src/特.txt"},
		{name: "other client", path: "//other/src/file.txt", want: "//other/src/file.txt"},
		{name: "similar client prefix", path: "//workspace-other/src/file.txt", want: "//workspace-other/src/file.txt"},
		{name: "local file", path: filepath.Join(root, "src", "特.txt"), want: "src/特.txt"},
		{name: "outside root", path: filepath.Join(filepath.Dir(root), "outside.txt"), want: filepath.Join(filepath.Dir(root), "outside.txt")},
		{name: "similar local prefix", path: root + "-other/file.txt", want: root + "-other/file.txt"},
	} {
		t.Run(test.name, func(t *testing.T) {
			a := New(&p4.Client{Workspace: "workspace", Root: root}, 0, "", false)
			a.width, a.height = 120, 40
			c := &confirmModal{kind: confirmKindRevert, files: []string{test.path}}
			a.confirm = c
			view := ansi.Strip(a.renderDiscardModal())
			prose := strings.Join(strings.Fields(strings.ReplaceAll(view, "│", "")), "")
			if !strings.Contains(prose, "'"+strings.Join(strings.Fields(test.want), "")+"'") {
				t.Fatalf("display scope lost expected path %q: %q", test.want, prose)
			}
			if test.path != test.want && strings.Contains(prose, "'"+strings.Join(strings.Fields(test.path), "")+"'") {
				t.Fatalf("display scope retained the workspace prefix: %q", prose)
			}
			if a.confirm != c || c.files[0] != test.path {
				t.Fatal("rendering rewrote the captured discard scope")
			}
		})
	}
}

func TestDiscardMenuAnchorDoesNotDependOnDescription(t *testing.T) {
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 40}, {Width: 60, Height: 18}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			a := New(&p4.Client{Workspace: "workspace"}, 0, "", false)
			a.Update(size)
			for _, file := range []string{"//workspace/src/file.txt", "//workspace/src/" + strings.Repeat("特🌿long_name_", 60)} {
				c := &confirmModal{kind: confirmKindRevert, files: []string{file}, localToDelete: []string{"added.txt"}}
				a.confirm = c
				for cursor := 0; cursor < 3; cursor++ {
					c.cursor = cursor
					lines := strings.Split(ansi.Strip(a.View()), "\n")
					top := -1
					for i, line := range lines {
						if strings.Contains(line, "╭───Discard changes─") {
							top = i
							break
						}
					}
					// Three choices plus two borders give a five-row menu; its anchor is independent of the tooltip.
					wantTop := size.Height/2 - (5+3+1)/2
					if top != wantTop {
						t.Fatalf("choice %d menu top = %d, want %d", cursor, top, wantTop)
					}
					popupHeight := len(strings.Split(a.renderDiscardModal(), "\n"))
					if top+popupHeight > size.Height-1 {
						t.Fatalf("choice %d tooltip reaches footer: top %d, height %d, terminal height %d", cursor, top, popupHeight, size.Height)
					}
					if a.confirm != c || c.files[0] != file {
						t.Fatal("placement rewrote the captured discard scope")
					}
				}
			}
		})
	}
}
