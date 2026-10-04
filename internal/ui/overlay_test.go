package ui

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

func overlayForegroundCells(s string) []string {
	var cells []string
	foreground := ""
	previous := 0
	for _, match := range regexp.MustCompile("\x1b\\[([0-9;]*)m").FindAllStringSubmatchIndex(s, -1) {
		for range s[previous:match[0]] {
			cells = append(cells, foreground)
		}
		codes := strings.Split(s[match[2]:match[3]], ";")
		for i := 0; i < len(codes); i++ {
			switch codes[i] {
			case "", "0", "39":
				foreground = ""
			case "38":
				if i+4 < len(codes) && codes[i+1] == "2" {
					foreground = strings.Join(codes[i+2:i+5], ";")
					i += 4
				}
			}
		}
		previous = match[1]
	}
	for range s[previous:] {
		cells = append(cells, foreground)
	}
	return cells
}

func TestSpliceLinePreservesRightSideBorderForeground(t *testing.T) {
	bg := "\x1b[38;2;68;70;79m│abcdefghij\x1b[38;2;120;130;140m│\x1b[0m"
	fg := "\x1b[38;2;90;180;240m POP \x1b[0m"
	line := spliceLine(3, fg, bg)
	if plain := ansi.Strip(line); plain != "│ab POP hij│" || lipgloss.Width(line) != 12 {
		t.Fatalf("overlay changed background cells: %q", plain)
	}
	colors := overlayForegroundCells(line)
	for column, want := range []string{"68;70;79", "68;70;79", "68;70;79", "90;180;240", "90;180;240", "90;180;240", "90;180;240", "90;180;240", "68;70;79", "68;70;79", "68;70;79", "120;130;140"} {
		if colors[column] != want {
			t.Fatalf("column %d foreground = %q, want %q; overlay %q", column, colors[column], want, line)
		}
	}
}

func TestSpliceLinePopupForegroundDoesNotBleedIntoDefaultSuffix(t *testing.T) {
	bg := "\x1b[38;2;68;70;79m│abcdef\x1b[0mghij│"
	fg := "\x1b[38;2;250;10;10mPOP"
	line := spliceLine(4, fg, bg)
	if plain := ansi.Strip(line); plain != "│abcPOPghij│" {
		t.Fatalf("overlay changed background cells: %q", plain)
	}
	colors := overlayForegroundCells(line)
	for column := 7; column < len(colors); column++ {
		if colors[column] != "" {
			t.Fatalf("popup foreground leaked into suffix column %d: %q", column, line)
		}
	}
}

func TestSpliceLineUsesDisplayCellsForUnicodePlacement(t *testing.T) {
	for _, test := range []struct {
		name string
		x    int
		fg   string
		bg   string
		want string
	}{
		{name: "wide prefix and suffix", x: 2, fg: "XY", bg: "界ab🌿cd", want: "界XY🌿cd"},
		{name: "wide foreground", x: 4, fg: "特", bg: "界ab🌿cd", want: "界ab特cd"},
		{name: "partially covered wide suffix", x: 2, fg: "X", bg: "ab界cd", want: "abX cd"},
		{name: "combining prefix", x: 1, fg: "X", bg: "éab界cd", want: "éXb界cd"},
		{name: "combining character covered", x: 0, fg: "X", bg: "éab界cd", want: "Xab界cd"},
	} {
		t.Run(test.name, func(t *testing.T) {
			bg := "\x1b[38;2;68;70;79m" + test.bg + "\x1b[0m"
			fg := "\x1b[38;2;90;180;240m" + test.fg + "\x1b[0m"
			line := spliceLine(test.x, fg, bg)
			if plain := ansi.Strip(line); plain != test.want || lipgloss.Width(line) != lipgloss.Width(test.bg) {
				t.Fatalf("overlay used rune offsets instead of display cells: %q, want %q", plain, test.want)
			}
		})
	}
}
