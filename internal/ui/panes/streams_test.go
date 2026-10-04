package panes

import (
	"regexp"
	"strings"
	"testing"
	"unicode/utf8"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
	"github.com/solessfir/lazyp4/internal/p4"
)

func streamFixture() []p4.StreamInfo {
	return []p4.StreamInfo{
		{Path: "//depot/main", Name: "main", Type: "mainline"},
		{Path: "//depot/feature", Parent: "//depot/main", Name: "feature", Type: "development"},
		{Path: "//depot/release", Parent: "//depot/main", Name: "release", Type: "release"},
		{Path: "//depot/task", Parent: "//depot/feature", Name: "task", Type: "task"},
		{Path: "//other/main", Name: "other", Type: "mainline"},
		{Path: "//other/dev", Parent: "//other/main", Name: "other-dev", Type: "development"},
	}
}

func streamViewLines(t *testing.T, p *StreamsPane, width, height int) []string {
	t.Helper()
	lines := strings.Split(ansi.Strip(p.View()), "\n")
	if len(lines) != height {
		t.Fatalf("pane wrapped or changed height: got %d lines, want %d: %q", len(lines), height, lines)
	}
	for i, line := range lines {
		if !utf8.ValidString(line) || ansi.StringWidth(line) != width {
			t.Fatalf("line %d has invalid text or width %d, want %d: %q", i, ansi.StringWidth(line), width, line)
		}
	}
	return lines
}

func streamBodyRow(line string) string {
	return strings.TrimRight(strings.TrimSuffix(strings.TrimPrefix(line, "│"), "│"), " ")
}

func TestStreamsRowsAlignMarkersAndPreserveHierarchy(t *testing.T) {
	p := NewStreamsPane()
	p.SetSize(48, 8)
	p.SetStreams(streamFixture(), "//depot/feature")
	lines := streamViewLines(t, p, 48, 8)
	want := []string{
		"  main",
		"* ├── feature",
		"  │   └── task",
		"  └── release",
		"  other",
		"  └── other-dev",
	}
	for i, row := range want {
		if got := streamBodyRow(lines[i+1]); got != row {
			t.Errorf("row %d = %q, want %q", i, got, row)
		}
	}
	if strings.Contains(strings.Join(lines[1:7], "\n"), "(development)") {
		t.Fatal("stream type still appears beside the name")
	}
	if strings.Index(lines[2], "feature") != strings.Index(lines[4], "release") {
		t.Fatal("sibling stream names are misaligned by the current marker")
	}
	if !strings.Contains(lines[7], "development") || !strings.Contains(lines[7], "2 of 6") {
		t.Fatalf("footer omits selected type or position: %q", lines[7])
	}

	paths := []string{"//depot/main", "//depot/feature", "//depot/task", "//depot/release", "//other/main", "//other/dev"}
	for i, path := range paths {
		p.SetCursor(i)
		if got := p.SelectedStream(); got != path {
			t.Errorf("cursor %d selected %q, want %q", i, got, path)
		}
	}
}

func TestStreamsTruncateUnicodeNamesWithoutWrapping(t *testing.T) {
	p := NewStreamsPane()
	p.SetSize(20, 5)
	longName := "日本語 long stream name with spaces and 終端"
	p.SetStreams([]p4.StreamInfo{
		{Path: "//depot/main", Name: longName, Type: "mainline"},
		{Path: "//depot/child", Parent: "//depot/main", Name: longName, Type: "development"},
	}, "//depot/main")
	for _, focused := range []bool{false, true} {
		p.SetFocused(focused)
		lines := streamViewLines(t, p, 20, 5)
		if strings.Contains(strings.Join(lines, "\n"), longName) {
			t.Fatal("long stream name was not truncated")
		}
		if !strings.HasPrefix(streamBodyRow(lines[1]), "* 日本語") || !strings.HasPrefix(streamBodyRow(lines[2]), "  └── 日本語") {
			t.Fatalf("truncation lost the marker, hierarchy, or Unicode name: %q", lines[1:3])
		}
		if p.SelectedStream() != "//depot/main" {
			t.Fatal("truncation changed the selected stream")
		}
	}
}

func TestStreamsScrollToSelectionAcrossNavigationAndResize(t *testing.T) {
	p := NewStreamsPane()
	p.SetSize(32, 5)
	p.SetStreams(streamFixture(), "//depot/main")
	for range 5 {
		p.Update(tea.KeyMsg{Type: tea.KeyDown})
	}
	lines := streamViewLines(t, p, 32, 5)
	if p.SelectedStream() != "//other/dev" || p.ScrollOffset() != 3 || !strings.Contains(lines[3], "other-dev") {
		t.Fatalf("keyboard selection is hidden or reordered: path=%q, offset=%d, rows=%q", p.SelectedStream(), p.ScrollOffset(), lines[1:4])
	}
	if !strings.Contains(lines[4], "development") || !strings.Contains(lines[4], "6 of 6") {
		t.Fatalf("footer does not follow selection: %q", lines[4])
	}
	p.SetSize(32, 4)
	lines = streamViewLines(t, p, 32, 4)
	if p.ScrollOffset() != 4 || !strings.Contains(lines[2], "other-dev") {
		t.Fatalf("resize hid the selection: offset=%d, rows=%q", p.ScrollOffset(), lines[1:3])
	}
	for range 5 {
		p.Update(tea.MouseMsg{Button: tea.MouseButtonWheelUp})
	}
	lines = streamViewLines(t, p, 32, 4)
	if p.SelectedStream() != "//depot/main" || p.ScrollOffset() != 0 || !strings.Contains(lines[1], "main") {
		t.Fatalf("mouse navigation did not scroll back: path=%q, offset=%d, rows=%q", p.SelectedStream(), p.ScrollOffset(), lines[1:3])
	}
	p.SetSize(16, 4)
	p.SetCursor(1)
	lines = streamViewLines(t, p, 16, 4)
	if !strings.Contains(lines[3], "2 of 6") || strings.Contains(lines[3], "development") {
		t.Fatalf("narrow footer should retain the counter: %q", lines[3])
	}
}

func TestStreamsCurrentSelectionRetainsGreen(t *testing.T) {
	profile := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(profile) })

	p := NewStreamsPane()
	p.SetSize(32, 4)
	p.SetStreams([]p4.StreamInfo{{Path: "//depot/main", Name: "main", Type: "mainline"}}, "//depot/main")
	p.SetFocused(true)
	row := strings.Split(p.View(), "\n")[1]
	if !regexp.MustCompile("\x1b\\[(?:[0-9]+;)*(?:32|92)(?:;[0-9]+)*m").MatchString(row) {
		t.Fatalf("focused current stream lost its green foreground: %q", row)
	}
}
