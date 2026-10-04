package ui

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/p4"
)

func openSearchableHelp(t *testing.T, width, height int) *App {
	t.Helper()
	a := New(&p4.Client{}, 0, "", false)
	a.active = paneLog
	a.Update(tea.WindowSizeMsg{Width: width, Height: height})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	return a
}

func TestHelpSearchMatchesDescriptionsAndKeyAliases(t *testing.T) {
	a := openSearchableHelp(t, 120, 40)
	for _, test := range []struct{ query, want string }{
		{"chckw", "Checkout workspace"},
		{"CHECKOUT", "Checkout workspace"},
		{"@enter", "Checkout workspace"},
		{"@<space>", "Checkout workspace"},
		{"@tab", "Cycle panel focus"},
		{"@esc", "Back to browser"},
		{"@?", "Close this window"},
	} {
		t.Run(test.query, func(t *testing.T) {
			content := ansi.Strip(a.helpContentFor(test.query))
			if !strings.Contains(content, test.want) || strings.Contains(content, "───") {
				t.Fatalf("filtered help includes headings or lacks %q: %q", test.want, content)
			}
		})
	}
	if content := ansi.Strip(a.helpContentFor("@checkout")); strings.Contains(content, "Checkout workspace") {
		t.Fatal("key-only search matched a description")
	}
	if content := strings.ToLower(ansi.Strip(a.helpContentFor("definitely-no-such-binding"))); !strings.Contains(content, "no") || !strings.Contains(content, "match") {
		t.Fatalf("zero matches lacks an explanation: %q", content)
	}
}

func TestHelpSearchConsumesLettersPasteAndUnicodeEditing(t *testing.T) {
	a := openSearchableHelp(t, 40, 12)
	a.helpViewport.GotoBottom()
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("jkq@?")})
	if !a.showHelp || a.helpFilter.Value() != "jkq@?" || a.helpViewport.YOffset != 0 || a.active != paneLog || a.checkout != nil {
		t.Fatal("search letters triggered shortcuts, closed help, or failed to reset scrolling")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if !a.showHelp || a.helpFilter.Value() != "" {
		t.Fatal("first Escape did not clear the query")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("特🌿\n\r\t\x1b\x00q"), Paste: true})
	query := a.helpFilter.Value()
	if !utf8.ValidString(query) || !strings.Contains(query, "特🌿") || !strings.Contains(query, "q") {
		t.Fatalf("paste lost printable Unicode: %q", query)
	}
	for _, r := range query {
		if unicode.IsControl(r) {
			t.Fatalf("paste retained control character %U", r)
		}
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("特🌿")})
	a.Update(tea.KeyMsg{Type: tea.KeyBackspace})
	if a.helpFilter.Value() != "特" {
		t.Fatalf("backspace failed to edit Unicode: %q", a.helpFilter.Value())
	}
	a.Update(tea.KeyMsg{Type: tea.KeyLeft})
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	if a.helpFilter.Value() != "x特" {
		t.Fatal("cursor editing did not insert into the query")
	}
}

func TestHelpCloseKeysNeverExecuteUnderlyingAction(t *testing.T) {
	for _, key := range []tea.KeyMsg{{Type: tea.KeyEnter}, {Type: tea.KeyCtrlC}} {
		a := openSearchableHelp(t, 120, 40)
		a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("checkout")})
		a.Update(key)
		if a.showHelp || a.checkout != nil || a.opRunning || a.active != paneLog {
			t.Fatalf("close key %q executed an underlying action or changed focus", key.String())
		}
	}
	a := openSearchableHelp(t, 120, 40)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("@?")})
	if !a.showHelp || a.helpFilter.Value() != "@?" {
		t.Fatal("question mark inside a query closed help")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	a.Update(tea.KeyMsg{Type: tea.KeyEsc})
	if a.showHelp {
		t.Fatal("second Escape did not close help")
	}
	a = openSearchableHelp(t, 120, 40)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'?'}})
	if a.showHelp {
		t.Fatal("question mark did not close an empty-query help window")
	}
}

func TestHelpSearchKeepsPopupHeightAndQueryOnAsyncResize(t *testing.T) {
	a := openSearchableHelp(t, 120, 40)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'x'}})
	height := lipgloss.Height(a.renderHelpModal())
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune("definitely-no-such-binding")})
	if lipgloss.Height(a.renderHelpModal()) != height {
		t.Fatal("filter results changed the anchored popup height")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune(strings.Repeat("特🌿", 20)), Paste: true})
	query, position := a.helpFilter.Value(), a.helpFilter.Position()
	a.Update(statusMsg{text: "async action completed"})
	a.Update(tea.WindowSizeMsg{Width: 20, Height: 8})
	view := a.renderHelpModal()
	if !a.showHelp || a.helpFilter.Value() != query || a.helpFilter.Position() != position || a.active != paneLog {
		t.Fatal("async completion or resize changed search state or context")
	}
	if !utf8.ValidString(view) || lipgloss.Width(view) > 18 || lipgloss.Height(view) > 7 || lipgloss.Height(a.View()) > 8 {
		t.Fatalf("narrow filtered help overflows: %dx%d", lipgloss.Width(view), lipgloss.Height(view))
	}
}

func TestHelpFilteredEndShowsLastBindingAtSmallHeight(t *testing.T) {
	a := openSearchableHelp(t, 20, 8)
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'@'}})
	rows := strings.Split(ansi.Strip(a.helpContent()), "\n")
	last := rows[len(rows)-1]
	a.Update(tea.KeyMsg{Type: tea.KeyEnd})
	if a.helpFilter.Value() != "@" || !strings.Contains(ansi.Strip(a.renderHelpModal()), last) {
		t.Fatalf("End failed to expose the last binding with filter intact: last %q, view %q", last, ansi.Strip(a.renderHelpModal()))
	}
}
