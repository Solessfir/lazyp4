package panes

import (
	"reflect"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func filteredFileList(t *testing.T) *FileListPane {
	t.Helper()
	p := NewFileListPane()
	p.ToggleMode()
	p.SetSize(80, 20)
	p.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
		{DepotFile: "//depot/A.txt", ClientFile: "//workspace/A.txt", Change: "default", Action: p4.ActionEdit},
		{DepotFile: "//depot/B.txt", ClientFile: "//workspace/B.txt", Change: "default", Action: p4.ActionEdit},
	}}})
	p.SetCursor(1)
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'B'}})
	p.Update(tea.KeyMsg{Type: tea.KeyEnter})
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if f := p.SelectedFile(); f == nil || f.DepotFile != "//depot/B.txt" {
		t.Fatalf("filter did not select B.txt: %+v", f)
	}
	return p
}

func TestFilteredMarkTargetsVisibleFile(t *testing.T) {
	p := filteredFileList(t)
	p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
	marked := p.MarkedFiles()
	if len(marked) != 1 || marked[0].DepotFile != "//depot/B.txt" {
		t.Fatalf("selected B.txt, marked %+v", marked)
	}
}

func TestFiltersAcceptPastedTextLiterally(t *testing.T) {
	msg := tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jkbeta"), Paste: true}
	t.Run("pending", func(t *testing.T) {
		p := NewFileListPane()
		p.ToggleMode()
		p.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
			{DepotFile: "//depot/jkbeta.txt"},
		}}})
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		p.Update(msg)
		if p.filter != "jkbeta" || !p.filterMode || len(p.filteredIdxs) != 2 {
			t.Fatalf("pasted filter was lost or treated as shortcuts: filter=%q, mode=%v, rows=%v", p.filter, p.filterMode, p.filteredIdxs)
		}
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}, Paste: true})
		if p.filter != "jkbeta/" || !p.filterMode {
			t.Fatal("pasted slash exited filter input")
		}
	})
	t.Run("browser", func(t *testing.T) {
		p := NewBrowserPane()
		p.SetRoots("//depot", "//workspace")
		p.LoadSearchIndex([]string{"//workspace/jkbeta.txt"})
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}})
		p.Update(msg)
		if p.filter != "jkbeta" || !p.filterMode || len(p.filteredRows()) != 1 {
			t.Fatalf("pasted filter was lost or treated as shortcuts: filter=%q, mode=%v, rows=%d", p.filter, p.filterMode, len(p.filteredRows()))
		}
		p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'/'}, Paste: true})
		if p.filter != "jkbeta/" || !p.filterMode {
			t.Fatal("pasted slash exited filter input")
		}
	})
}

func TestFilteredRowsRebuildWithoutHiddenSelection(t *testing.T) {
	for _, mutation := range []string{"refresh", "mode"} {
		t.Run(mutation, func(t *testing.T) {
			p := filteredFileList(t)
			if mutation == "refresh" {
				p.SetChangelists([]p4.Changelist{{ID: "default", Files: []p4.OpenedFile{
					{DepotFile: "//depot/A.txt", ClientFile: "//workspace/A.txt", Change: "default", Action: p4.ActionEdit},
				}}})
			} else {
				p.ToggleMode()
			}
			_ = p.View()
			if mutation == "refresh" {
				if p.SelectedFile() != nil || p.SelectedCL() != "" || p.SelectedDepotPath() != "" || p.SelectedIsHeader() || p.IsOnCLHeader() {
					t.Fatal("empty filter has an invisible selection")
				}
				p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{' '}})
				if len(p.MarkedFiles()) != 0 {
					t.Fatal("empty filter marked an invisible file")
				}
			}
		})
	}
}

func TestWorkspaceResponsesPreserveMappedStatusInEitherOrder(t *testing.T) {
	for _, statusFirst := range []bool{false, true} {
		t.Run(map[bool]string{false: "filesystem first", true: "status first"}[statusFirst], func(t *testing.T) {
			p := NewBrowserPane()
			p.SetRoots("//depot/main", "//workspace")
			local := p4.WorkspaceEntry{ClientPath: "//workspace/A.txt", DepotPath: "//import/remapped.txt", LocalPath: "C:/workspace/A.txt", Tracked: true}
			missing := p4.WorkspaceEntry{ClientPath: "//workspace/missing.txt", DepotPath: "//import/missing.txt", LocalPath: "C:/workspace/missing.txt", Tracked: true}
			load := func() { p.LoadChildrenFast("//workspace", nil, []p4.WorkspaceEntry{local}) }
			status := func() {
				p.ApplyStatus("//workspace", []string{local.DepotPath}, nil, []p4.WorkspaceEntry{missing}, map[string]bool{local.DepotPath: true}, nil)
			}
			if statusFirst {
				status()
				load()
			} else {
				load()
				status()
			}
			node := p.findNode(p.root, local.ClientPath)
			if node == nil || !node.tracked || !node.openedByMe {
				t.Fatalf("lost mapped tracked/opened status: %+v", node)
			}
			if p.findNode(p.root, missing.ClientPath) == nil {
				t.Fatal("missing tracked file was removed")
			}
			p.NavigateTo(local.ClientPath)
			selected := p.SelectedEntry()
			if selected.DepotPath != local.DepotPath || selected.ClientPath != local.ClientPath || selected.LocalPath != local.LocalPath {
				t.Fatalf("wrong mapped selection: %+v", selected)
			}
			p.RefreshYoursOpen(nil)
			load()
			if p.findNode(p.root, local.ClientPath).openedByMe {
				t.Fatal("late filesystem response restored stale opened status")
			}
		})
	}
}

func TestBrowserModeUsesConfiguredRoot(t *testing.T) {
	p := NewBrowserPane()
	p.SetRoots("//depot/main", "//workspace")
	p.ToggleMode()
	if p.RootPath() != "//depot/main" {
		t.Fatal("depot mode kept workspace root")
	}
	p.ToggleMode()
	if p.RootPath() != "//workspace" {
		t.Fatal("workspace mode kept depot root")
	}
	p.SetRoots("//", "//workspace")
	p.ToggleMode()
	if p.SelectedPath() != "//..." || p.SelectedEntry().DepotPath != "//..." {
		t.Fatal("classic depot root wildcard is invalid")
	}
}

func TestResolveActionsRetainDisplayedScope(t *testing.T) {
	p := NewResolvePane()
	files := []string{"C:/workspace/A.txt", "C:/workspace/B.txt"}
	conflicts := []p4.ConflictFile{{ClientFile: files[0]}, {ClientFile: files[1]}, {ClientFile: files[0]}}
	for key, flag := range map[rune]string{'a': "-am", 's': "-as", 't': "-at", 'y': "-ay"} {
		p.SetConflicts(conflicts)
		cmd := p.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{key}})
		p.SetConflicts(nil)
		msg := cmd().(ResolveAutoMsg)
		if !reflect.DeepEqual(msg.Files, files) || !reflect.DeepEqual(msg.Flags, []string{flag}) {
			t.Fatalf("key %c lost scope: %+v", key, msg)
		}
	}
	p.SetConflicts(conflicts)
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	msg := p.Update(tea.KeyMsg{Type: tea.KeyEnter})().(ResolveInteractiveMsg)
	if msg.File != files[1] {
		t.Fatalf("interactive resolve targeted %q", msg.File)
	}
	p.SetSize(80, 3)
	if !strings.Contains(p.View(), "B.txt") {
		t.Fatal("selected conflict is outside visible rows")
	}
}
