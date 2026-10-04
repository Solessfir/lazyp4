package ui

import (
	"fmt"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestPaneNumbersMatchDisplayedTitles(t *testing.T) {
	for _, streams := range []bool{false, true} {
		for _, right := range []activePane{paneLog, paneDiff} {
			t.Run(fmt.Sprintf("streams=%v/right=%v", streams, right), func(t *testing.T) {
				a := New(&p4.Client{}, 0, "", false)
				a.showStreams = streams
				a.historyMode = right == paneLog
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'5'}})
				if a.active != right {
					t.Fatalf("5 focused %v, want %v", a.active, right)
				}
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'4'}})
				if a.active != paneShelved {
					t.Fatalf("4 focused %v, want Shelved", a.active)
				}
				a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'3'}})
				want := paneShelved
				if streams {
					want = paneStreams
				}
				if a.active != want {
					t.Fatalf("3 focused %v, want %v", a.active, want)
				}
			})
		}
	}
}

func TestHistoryCheckoutKeysRespectBusyOperations(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyRunes, Runes: []rune{' '}}} {
		for _, busy := range []bool{false, true} {
			for _, open := range []bool{false, true} {
				t.Run(fmt.Sprintf("key=%q/busy=%v/open=%v", key.String(), busy, open), func(t *testing.T) {
					a := New(&p4.Client{}, 0, "", false)
					a.active, a.opRunning, a.opName = paneLog, busy, "Shelving"
					a.log.SetEntries([]p4.FilelogEntry{{Change: "41"}})
					if open {
						a.fileList.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{{ClientFile: "//workspace/selected.txt"}}}})
					}
					_, cmd := a.Update(key)
					if busy {
						if a.checkout != nil || cmd != nil {
							t.Fatal("history checkout started during another operation")
						}
					} else if a.checkout == nil || a.checkout.cl != "41" || a.checkout.hasFiles != open {
						t.Fatal("history key did not open the correct checkout confirmation")
					}
				})
			}
		}
	}
}
