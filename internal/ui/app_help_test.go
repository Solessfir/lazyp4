package ui

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestHelpBindingsStaySingleLineAcrossWidths(t *testing.T) {
	var rows int
	for _, size := range []tea.WindowSizeMsg{{Width: 120, Height: 40}, {Width: 40, Height: 12}, {Width: 20, Height: 8}} {
		t.Run(fmt.Sprintf("%dx%d", size.Width, size.Height), func(t *testing.T) {
			a := New(&p4.Client{}, 0, "", false)
			a.Update(size)
			a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
			content := a.helpContent()
			lines := strings.Split(content, "\n")
			if rows == 0 {
				rows = len(lines)
			} else if len(lines) != rows {
				t.Fatalf("narrow help wrapped bindings into continuation rows: got %d rows, want %d", len(lines), rows)
			}
			for _, line := range lines {
				if !utf8.ValidString(line) || lipgloss.Width(line) > a.helpViewport.Width {
					t.Fatalf("help content exceeds viewport width %d: %q", a.helpViewport.Width, line)
				}
			}
			view := a.renderHelpModal()
			if lipgloss.Height(view) > size.Height-1 || lipgloss.Width(view) > size.Width-2 {
				t.Fatalf("help popup exceeds terminal bounds: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
			}
			if size.Width == 20 && !strings.Contains(ansi.Strip(content), "…") {
				t.Fatal("narrow descriptions lack an ellipsis")
			}
		})
	}
}

func TestHelpFitsContentAndAlignsSectionHeaders(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.Update(tea.WindowSizeMsg{Width: 160, Height: 80})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	content := ansi.Strip(a.helpContent())
	if got, want := lipgloss.Height(a.renderHelpModal()), len(strings.Split(content, "\n"))+2; got != want {
		t.Fatalf("help leaves unused body rows: got popup height %d, want %d", got, want)
	}
	for _, key := range []string{"<enter>", "<space>", "<tab>"} {
		if !strings.Contains(content, key) {
			t.Fatalf("named key lacks brackets: %s", key)
		}
	}
	descriptionColumn := -1
	for _, line := range strings.Split(content, "\n") {
		if strings.Contains(line, "Navigate") {
			descriptionColumn = strings.Index(line, "Navigate")
		}
	}
	if descriptionColumn < 1 {
		t.Fatal("missing navigation binding")
	}
	for _, key := range []string{"<space>", "<tab>"} {
		for _, line := range strings.Split(content, "\n") {
			if start := strings.Index(line, key); start >= 0 && start+len(key)+1 != descriptionColumn {
				t.Fatalf("key %q is not right aligned with one gap before descriptions: %q", key, line)
			}
		}
	}
	for _, heading := range []string{"─── Local", "─── Global"} {
		found := false
		for _, line := range strings.Split(content, "\n") {
			if strings.Contains(line, heading) {
				found = true
				if lipgloss.Width(line[:strings.Index(line, heading)]) != descriptionColumn {
					t.Fatalf("heading %q does not align with descriptions at column %d: %q", heading, descriptionColumn, line)
				}
			}
		}
		if !found {
			t.Fatalf("missing section heading %q", heading)
		}
	}
}

func TestHelpSmallViewportScrollPreservesContext(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.active = paneResolve
	a.Update(tea.WindowSizeMsg{Width: 40, Height: 12})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	before := a.helpViewport.View()
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'j'}})
	if a.helpViewport.YOffset != 1 || a.helpViewport.View() == before {
		t.Fatal("help key scroll did not change the visible rows")
	}
	a.Update(tea.MouseMsg{Button: tea.MouseButtonWheelDown})
	if a.helpViewport.YOffset != 4 || !a.showHelp || a.active != paneResolve {
		t.Fatal("help wheel scroll changed pane context or failed to scroll")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'k'}})
	if a.helpViewport.YOffset != 3 {
		t.Fatal("help up key did not scroll back")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.showHelp || a.active != paneResolve {
		t.Fatal("closing help changed the underlying pane")
	}
}

func TestHelpResolveBindingsMatchActions(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.active = paneResolve
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	content := ansi.Strip(a.helpContent())
	for _, binding := range []struct{ key, action string }{{"a", "Automatic merge"}, {"t", "Accept theirs"}, {"y", "Accept yours"}, {"<esc>", "Close conflicts pane"}} {
		found := false
		for _, line := range strings.Split(content, "\n") {
			if strings.HasPrefix(strings.TrimSpace(line), binding.key+" ") && strings.Contains(line, binding.action) {
				found = true
			}
		}
		if !found {
			t.Fatalf("resolve help is missing %s %s: %q", binding.key, binding.action, content)
		}
	}
	a.active = paneBrowser
	if strings.Contains(ansi.Strip(a.helpContent()), "Automatic") {
		t.Fatal("browser help retained resolve actions")
	}
}
