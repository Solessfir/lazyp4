package panes

import (
	"regexp"
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/solessfir/lazyp4/internal/p4"
)

func forcePaneColors(t *testing.T) {
	t.Helper()
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })
}

func checkPaneLines(t *testing.T, view string, width, height int) []string {
	t.Helper()
	lines := strings.Split(ansi.Strip(view), "\n")
	if len(lines) != height {
		t.Fatalf("pane height = %d, want %d: %q", len(lines), height, lines)
	}
	for i, line := range lines {
		if !utf8.ValidString(line) || ansi.StringWidth(line) != width {
			t.Fatalf("line %d has invalid text or width %d, want %d: %q", i, ansi.StringWidth(line), width, line)
		}
	}
	return lines
}

func TestHistoryCompactRowsAndSelectedMetadata(t *testing.T) {
	forcePaneColors(t)
	p := NewLogPane("5", "History")
	p.SetSize(64, 6)
	p.SetEntries([]p4.FilelogEntry{
		{Change: "120", Author: "solessfir", Client: "desktop", Description: "Update docs"},
		{Change: "100", Author: "Jane Doe", Client: "build", Description: "Fix build"},
	})
	p.SetCurrentCL("100")
	p.SetFocused(true)
	view := p.View()
	lines := checkPaneLines(t, view, 64, 6)
	if !strings.Contains(lines[1], "CL 120 so ○ Update docs") || !strings.Contains(lines[2], "CL 100 JD ● Fix build") {
		t.Fatalf("history lost its compact ID, author, node, or description: %q", lines[1:3])
	}
	if !strings.Contains(lines[5], "solessfir@desktop") || !strings.Contains(lines[5], "1 of 2") {
		t.Fatalf("selected metadata missing from footer: %q", lines[5])
	}
	firstRow := strings.Split(view, "\n")[1]
	idPart := strings.Split(firstRow, "CL 120")[0]
	if !regexp.MustCompile("\x1b\\[(?:[0-9]+;)*34(?:;[0-9]+)*m").MatchString(idPart) {
		t.Fatalf("newer-than-have ID lost its blue foreground: %q", firstRow)
	}
	p.SetCursorByLine(1)
	lines = checkPaneLines(t, p.View(), 64, 6)
	if p.SelectedChange() != "100" || !strings.Contains(lines[5], "Jane Doe@build") || !strings.Contains(lines[5], "2 of 2") {
		t.Fatalf("click selection or footer did not follow the entry: change=%q, footer=%q", p.SelectedChange(), lines[5])
	}
}

func TestHistoryAuthorColorsMatchLazygit(t *testing.T) {
	want := map[string]lipgloss.Color{
		"solessfir": "#efe83b",
		"Jane Doe":  "#502ed7",
		"张伟":        "#b6dd1b",
		"bob":       "#f813e1",
	}
	seen := map[lipgloss.Color]string{}
	for author, color := range want {
		if got := authorColor(author); got != color || authorColor(author) != got {
			t.Errorf("author %q color = %q, want stable %q", author, got, color)
		}
		if other, exists := seen[authorColor(author)]; exists {
			t.Errorf("authors %q and %q unexpectedly share a color", author, other)
		}
		seen[authorColor(author)] = author
	}
}

func TestHistoryWrappingPreservesDescriptionAndEntryMapping(t *testing.T) {
	const description = "日本語の説明 with several spaced words and a verylongunbrokenidentifier終端"
	entries := []p4.FilelogEntry{
		{Change: "120", Author: "solessfir", Description: description},
		{Change: "119", Author: "bob", Description: "Second entry"},
		{Change: "118", Author: "Jane Doe", Description: "Third entry"},
	}
	withoutSpace := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	for _, width := range []int{8, 16, 32} {
		content, offsets := buildLogContent(entries[:1], 0, width, true, "")
		lines := strings.Split(ansi.Strip(content), "\n")
		if len(offsets) != 1 || offsets[0] != 0 {
			t.Fatalf("wrapped entry offsets = %v", offsets)
		}
		for _, line := range lines {
			if !utf8.ValidString(line) || ansi.StringWidth(line) != width {
				t.Fatalf("width %d wrapped invalid or overflowing line: %q", width, line)
			}
		}
		prefix := "CL 120 so ○ "
		if ansi.StringWidth(prefix) >= width {
			lines = lines[1:]
		} else {
			lines[0] = strings.TrimPrefix(lines[0], prefix)
		}
		if got := withoutSpace(strings.Join(lines, "")); got != withoutSpace(description) {
			t.Fatalf("width %d lost description text: got %q, want %q", width, got, withoutSpace(description))
		}
	}

	p := NewLogPane("5", "History")
	p.SetSize(40, 14)
	p.SetEntries(entries)
	lines := checkPaneLines(t, p.View(), 40, 14)
	secondLine := -1
	for i, line := range lines[1 : len(lines)-1] {
		if strings.Contains(line, "CL 119") {
			secondLine = i
		}
	}
	if secondLine < 1 {
		t.Fatalf("first description did not wrap before second entry: %q", lines)
	}
	p.SetCursorByLine(secondLine - 1)
	if p.SelectedChange() != "120" {
		t.Fatal("clicking a wrapped continuation selected the next entry")
	}
	p.SetCursorByLine(secondLine)
	if p.SelectedChange() != "119" {
		t.Fatal("clicking the second entry used obsolete line offsets")
	}
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	p.SetSize(28, 4)
	lines = checkPaneLines(t, p.View(), 28, 4)
	if p.SelectedChange() != "118" || p.ScrollOffset() == 0 || !strings.Contains(strings.Join(lines[1:3], "\n"), "CL 118") {
		t.Fatalf("resize hid or changed the keyboard selection: change=%q, offset=%d, rows=%q", p.SelectedChange(), p.ScrollOffset(), lines[1:3])
	}
}

func TestDiffWrapPreservesUnicodeTabsAndHunkContextStyle(t *testing.T) {
	forcePaneColors(t)
	const raw = "+日本語 with a long unbrokenidentifier終端\n-\tTabbed 日本語\n@@ -1,2 +1,3 @@ function_context"
	withoutSpace := func(s string) string {
		return strings.Map(func(r rune) rune {
			if unicode.IsSpace(r) {
				return -1
			}
			return r
		}, s)
	}
	for _, width := range []int{8, 20} {
		colored := colorize(raw, width)
		lines := strings.Split(ansi.Strip(colored), "\n")
		for _, line := range lines {
			if !utf8.ValidString(line) || ansi.StringWidth(line) > width {
				t.Fatalf("width %d overflowing or invalid diff line: %q", width, line)
			}
		}
		if got := withoutSpace(strings.Join(lines, "")); got != withoutSpace(raw) {
			t.Fatalf("width %d lost diff contents: got %q, want %q", width, got, withoutSpace(raw))
		}
	}
	if got := ansi.Strip(colorize("-\tTabbed 日本語", 80)); got != "-       Tabbed 日本語" {
		t.Fatalf("tab expansion lost indentation: %q", got)
	}
	hunk := colorize("@@ -1,2 +1,3 @@ function_context", 80)
	if !regexp.MustCompile("\x1b\\[(?:[0-9]+;)*36(?:;[0-9]+)*m").MatchString(hunk) {
		t.Fatalf("hunk range lost its cyan foreground: %q", hunk)
	}
	if !strings.Contains(hunk, "\x1b[0m function_context") {
		t.Fatalf("hunk context inherited the range color: %q", hunk)
	}
}

func TestDiffResizePreservesScrollAndNewContentResetsIt(t *testing.T) {
	p := NewDiffPane()
	p.SetSize(40, 6)
	p.SetContent(strings.Repeat("+long line 日本語 with more words\n", 20))
	p.Update(tea.KeyMsg{Type: tea.KeyDown})
	if p.viewport.YOffset == 0 {
		t.Fatal("diff navigation did not scroll")
	}
	offset := p.viewport.YOffset
	p.SetSize(28, 6)
	checkPaneLines(t, p.View(), 28, 6)
	if p.viewport.YOffset != offset {
		t.Fatalf("resize reset diff scroll from %d to %d", offset, p.viewport.YOffset)
	}
	p.SetContent("+replacement")
	if p.viewport.YOffset != 0 || !strings.Contains(ansi.Strip(p.View()), "+replacement") {
		t.Fatal("new diff did not reset to its first line")
	}
}

func TestStatusIdentityRemainsVisibleWhileFetching(t *testing.T) {
	p := NewStatusPane()
	p.SetInfo(p4.WorkspaceInfo{Client: "desktop", User: "solessfir", Stream: "//depot/main"})
	p.SetPending(12)
	p.SetFetching()
	for _, width := range []int{18, 32} {
		p.SetWidth(width)
		lines := checkPaneLines(t, p.View(), width, StatusHeight)
		if !strings.Contains(lines[1], "desktop") || !strings.Contains(lines[1], "main") || strings.Contains(lines[1], "↓12") {
			t.Fatalf("fetch replaced identity or retained stale behind count: %q", lines[1])
		}
	}
}
