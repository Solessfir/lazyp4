package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestShelvingBlocksMutationsUntilCompletion(t *testing.T) {
	for _, fail := range []bool{false, true} {
		name := "success"
		if fail {
			name = "error"
		}
		t.Run(name, func(t *testing.T) {
			log := fakeP4(t)
			a := New(&p4.Client{}, 0, "", false)
			a.shelveModal = &shelveDescModal{input: textinput.New(), files: []p4.OpenedFile{{ClientFile: "//workspace/selected.txt"}}}
			cmd := a.execShelveWithDesc()
			if cmd == nil || !a.opRunning || a.opName != "Shelving" || a.opCancel != nil {
				t.Fatal("shelving did not hold mutation guard")
			}
			id := a.opID
			a.active = paneStreams
			_, switchCmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
			if unwrapActivityResult(switchCmd) != nil || a.streamSwitch != nil {
				t.Fatal("stream switch started while shelving")
			}
			if a.execShelveWithDesc() != nil || a.cmdSubmitStart("default", "description") != nil || a.opID != id {
				t.Fatal("another mutation started while shelving")
			}
			a.Update(shelveDoneMsg{id: id + 1})
			if !a.opRunning {
				t.Fatal("uncorrelated completion released mutation guard")
			}
			msg := shelveDoneMsg{id: id, err: errors.New("shelve failed")}
			if !fail {
				msg = cmd().(shelveDoneMsg)
				commands, err := os.ReadFile(log)
				if err != nil || !strings.Contains(string(commands), "revert -c 12 //workspace/selected.txt") || strings.Contains(string(commands), "revert //workspace/") {
					t.Fatalf("shelving cleanup lost its changelist scope: %s, error %v", commands, err)
				}
			}
			a.Update(msg)
			if a.opRunning {
				t.Fatal("shelving completion retained mutation guard")
			}
		})
	}
}

func TestCheckoutBlocksMutationsUntilCompletion(t *testing.T) {
	for _, withShelf := range []bool{false, true} {
		for _, fail := range []bool{false, true} {
			name := "direct"
			if withShelf {
				name = "shelved"
			}
			if fail {
				name += "/error"
			} else {
				name += "/success"
			}
			t.Run(name, func(t *testing.T) {
				log := fakeP4(t)
				a := New(&p4.Client{}, 0, "", false)
				a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/selected.txt"}}}})
				var cmd tea.Cmd
				if withShelf {
					a.checkout = &checkoutModal{cl: "99", stream: "//depot/main", hasFiles: true}
					cmd = a.cmdShelveForCheckout(a.checkout)
				} else {
					cmd = a.cmdSyncToCL("//depot/main", "99")
				}
				if cmd == nil || !a.opRunning || a.opCancel != nil || a.checkout != nil {
					t.Fatal("checkout did not hold mutation guard")
				}
				if _, err := os.Stat(log); !os.IsNotExist(err) {
					t.Fatal("checkout ran a synchronous native command")
				}
				id := a.opID
				a.active = paneStreams
				_, switchCmd := a.Update(tea.KeyMsg{Type: tea.KeyEnter})
				if unwrapActivityResult(switchCmd) != nil || a.streamSwitch != nil {
					t.Fatal("stream switch started while checking out")
				}
				if a.cmdSyncToCL("//depot/main", "100") != nil || a.cmdShelveForCheckout(&checkoutModal{cl: "100"}) != nil || a.opID != id {
					t.Fatal("another checkout started while checking out")
				}
				a.Update(syncToCLDoneMsg{id: id + 1})
				if !a.opRunning {
					t.Fatal("uncorrelated checkout completion released mutation guard")
				}
				msg := syncToCLDoneMsg{id: id, cl: "99", err: errors.New("checkout failed")}
				if !fail {
					msg = cmd().(syncToCLDoneMsg)
					commands, err := os.ReadFile(log)
					if err != nil || !strings.Contains(string(commands), "sync -f //depot/main/...@99") {
						t.Fatalf("checkout lost its captured target: %s, error %v", commands, err)
					}
					if withShelf && !strings.Contains(string(commands), "revert -c 12 //workspace/selected.txt") {
						t.Fatalf("checkout shelf cleanup lost changelist scope: %s", commands)
					}
				}
				a.Update(msg)
				if a.opRunning {
					t.Fatal("checkout completion retained mutation guard")
				}
			})
		}
	}
}

func TestBusyCheckoutRetryStillShelvesOpenFiles(t *testing.T) {
	log := fakeP4(t)
	a := New(&p4.Client{Workspace: "workspace", Stream: "//depot/main"}, 0, "", false)
	a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/selected.txt"}}}})
	a.opRunning, a.opName = true, "Resolving"
	co := &checkoutModal{cl: "99", stream: "//depot/main", hasFiles: true}
	a.checkout = co
	key := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'y'}}
	_, cmd := a.Update(key)
	if unwrapActivityResult(cmd) != nil || a.checkout != co || !a.opRunning {
		t.Fatal("busy confirmation changed checkout or started an operation")
	}
	a.opRunning = false
	_, cmd = a.Update(key)
	if cmd == nil || a.checkout != nil || !a.opRunning {
		t.Fatal("checkout retry did not start after the other operation completed")
	}
	if msg := unwrapActivityResult(cmd()).(syncToCLDoneMsg); msg.err != nil {
		t.Fatal(msg.err)
	}
	data, err := os.ReadFile(log)
	commands := string(data)
	if err != nil || !strings.Contains(commands, "shelve -c 12") || !strings.Contains(commands, "revert -c 12 //workspace/selected.txt") || !strings.Contains(commands, "sync -f //depot/main/...@99") {
		t.Fatalf("checkout retry skipped shelving or scoped cleanup: %s, error %v", commands, err)
	}
}
