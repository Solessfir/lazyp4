package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestQueuedDiscardCheckRejectsNewerOperation(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, timing := range []string{"before", "during", "after"} {
			t.Run(fmt.Sprintf("changed=%v/%s", changed, timing), func(t *testing.T) {
				log := fakeP4(t)
				if changed {
					t.Setenv("LAZYP4_UI_DIFF_ERROR", "1")
				}
				a := New(&p4.Client{}, 0, "", false)
				check := a.cmdRevertCheck("//workspace/selected.txt", "41")
				msg := check().(revertCheckMsg)
				if msg.hasChanges != changed {
					t.Fatalf("discard check returned changed=%v", msg.hasChanges)
				}
				if timing != "before" {
					prepare := a.cmdSubmitMarkedFilter([]p4.OpenedFile{{ClientFile: "//workspace/selected.txt", Action: p4.ActionAdd}}, "description")
					t.Cleanup(a.opCancel)
					if timing == "after" {
						a.opCancel()
						a.Update(prepare())
						if a.opRunning {
							t.Fatal("cancelled preparation did not finish")
						}
					}
				}
				_, cmd := a.Update(msg)
				if timing != "before" {
					if cmd != nil || a.confirm != nil {
						t.Fatal("obsolete check started discard or opened confirmation")
					}
				} else {
					if changed {
						if a.confirm == nil || a.confirm.clID != "41" {
							t.Fatal("current check did not preserve its confirmation scope")
						}
						_, cmd = a.Update(tea.KeyMsg{Type: tea.KeyEnter})
					}
					if cmd == nil {
						t.Fatal("current discard did not run")
					}
					cmd()
				}
				commands, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				if strings.Contains(string(commands), "revert -c 41 //workspace/selected.txt") != (timing == "before") {
					t.Fatalf("unexpected discard commands: %s", commands)
				}
			})
		}
	}
}

func TestBusyDiscardConfirmationPreservesScopeForRetry(t *testing.T) {
	for _, key := range []string{"enter", "y", "Y", "l", "d"} {
		t.Run(key, func(t *testing.T) {
			log := fakeP4(t)
			root := t.TempDir()
			local := filepath.Join(root, "selected.txt")
			if err := os.WriteFile(local, []byte("preserve until confirmed"), 0o600); err != nil {
				t.Fatal(err)
			}
			t.Setenv("LAZYP4_UI_REVERT_OUTPUT", fmt.Sprintf("{\"oldAction\":\"add\",\"action\":\"abandoned\",\"clientFile\":%q}\n", local))
			a := New(&p4.Client{Workspace: "workspace", Root: root}, 0, "", false)
			confirmation := &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/selected.txt"}, localToDelete: []string{local}, clID: "41"}
			a.confirm = confirmation
			prepare := a.cmdSubmitMarkedFilter([]p4.OpenedFile{{ClientFile: "//workspace/other.txt", Action: p4.ActionAdd}}, "description")
			t.Cleanup(a.opCancel)
			input := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(key)}
			if key == "enter" {
				input = tea.KeyMsg{Type: tea.KeyEnter}
			}
			_, cmd := a.Update(input)
			if cmd != nil || a.confirm != confirmation {
				t.Fatal("busy acceptance ran discard or lost its confirmation")
			}
			if _, err := os.Stat(local); err != nil {
				t.Fatalf("busy acceptance removed local file: %v", err)
			}
			if commands, err := os.ReadFile(log); err == nil && len(commands) > 0 {
				t.Fatalf("busy acceptance invoked p4: %s", commands)
			}
			a.opCancel()
			a.Update(prepare())
			a.fileList.SetChangelists([]p4.Changelist{{ID: "99", Files: []p4.OpenedFile{{ClientFile: "//workspace/other.txt"}}}})
			a.fileList.SetCursor(1)
			_, cmd = a.Update(input)
			if cmd == nil || a.confirm != nil {
				t.Fatal("idle retry did not accept the preserved confirmation")
			}
			cmd()
			commands, err := os.ReadFile(log)
			if err != nil || !strings.Contains(string(commands), "revert -c 41 //workspace/selected.txt") || strings.Contains(string(commands), "other.txt") {
				t.Fatalf("retry lost captured discard scope: %s, error %v", commands, err)
			}
			if _, err := os.Stat(local); os.IsNotExist(err) != (key == "d") {
				t.Fatalf("retry local deletion mismatch: %v", err)
			}
		})
	}
}

func TestBusyDiscardConfirmationCanCancel(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.confirm = &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/selected.txt"}}
	a.cmdSubmitMarkedFilter([]p4.OpenedFile{{ClientFile: "//workspace/other.txt", Action: p4.ActionAdd}}, "description")
	t.Cleanup(a.opCancel)
	_, cmd := a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if cmd != nil || a.confirm != nil || !a.opRunning {
		t.Fatal("Escape did not cancel the confirmation independently of the operation")
	}
}

func TestQueuedDiscardCheckRejectsStreamSwitch(t *testing.T) {
	for _, changed := range []bool{false, true} {
		for _, phase := range []string{"confirmation", "switching", "completed"} {
			t.Run(fmt.Sprintf("changed=%v/%s", changed, phase), func(t *testing.T) {
				log := fakeP4(t)
				if changed {
					t.Setenv("LAZYP4_UI_DIFF_ERROR", "1")
				}
				a := New(&p4.Client{Workspace: "workspace", Stream: "//streams/old"}, 0, "", false)
				check := a.cmdRevertCheck("//workspace/selected.txt", "41")
				msg := check().(revertCheckMsg)
				id := a.opID
				a.streamSwitch = &streamSwitchModal{stream: "//streams/new", switching: phase != "confirmation"}
				if phase == "completed" {
					a.Update(streamSwitchedMsg{stream: "//streams/new"})
				}
				if a.opID != id {
					t.Fatal("stream switch unexpectedly changed operation generation")
				}
				_, cmd := a.Update(msg)
				if cmd != nil || a.confirm != nil {
					t.Fatal("queued check started discard or opened confirmation across stream switch")
				}
				commands, err := os.ReadFile(log)
				if err != nil || strings.Contains(string(commands), "revert") {
					t.Fatalf("queued discard mutated files across stream switch: %s, error %v", commands, err)
				}
			})
		}
	}
}
