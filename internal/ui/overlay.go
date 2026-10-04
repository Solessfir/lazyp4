package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// placeOverlay renders fg on top of bg at position (x, y).
func placeOverlay(x, y int, fg, bg string) string {
	fgLines := strings.Split(fg, "\n")
	bgLines := strings.Split(bg, "\n")
	result := make([]string, len(bgLines))
	copy(result, bgLines)

	for fi, fgLine := range fgLines {
		bi := y + fi
		if bi < 0 || bi >= len(result) {
			continue
		}
		result[bi] = spliceLine(x, fgLine, result[bi])
	}
	return strings.Join(result, "\n")
}

// overlayCenter centers fg over bg and returns the merged string.
func overlayCenter(fg, bg string, bgW, bgH int) string {
	fgLines := strings.Split(fg, "\n")
	fgW := 0
	for _, l := range fgLines {
		if w := lipgloss.Width(l); w > fgW {
			fgW = w
		}
	}
	fgH := len(fgLines)
	ox := (bgW - fgW) / 2
	oy := (bgH - fgH) / 2
	if ox < 0 {
		ox = 0
	}
	if oy < 0 {
		oy = 0
	}
	return placeOverlay(ox, oy, fg, bg)
}

// spliceLine overlays fgLine onto bgLine starting at column x.
func spliceLine(x int, fg, bg string) string {
	fgW := lipgloss.Width(fg)

	// Left: bg truncated to x columns with ANSI preserved.
	left := ansiTruncate(bg, x)
	leftW := lipgloss.Width(left)
	if leftW < x {
		left += strings.Repeat(" ", x-leftW)
	}

	// Restore the background's styling after the foreground resets it.
	right := ansiSkip(bg, x+fgW)

	return left + "\033[0m" + fg + "\033[0m" + right
}

// ansiTruncate truncates s to maxWidth visible columns, preserving ANSI sequences.
func ansiTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	return ansi.Truncate(s, maxWidth, "")
}

// ansiSkip removes visible columns while retaining the background's ANSI state.
func ansiSkip(s string, skipCols int) string {
	if skipCols <= 0 {
		return s
	}
	var buf strings.Builder
	col := 0
	var state byte
	for len(s) > 0 {
		seq, width, n, next := ansi.DecodeSequence(s, state, nil)
		if col >= skipCols && width > 0 {
			break
		}
		state = next
		// Replay escape sequences, not combining marks from the removed prefix.
		if width == 0 && (seq[0] == ansi.ESC || seq[0] >= 0x80 && seq[0] <= 0x9f) {
			buf.WriteString(seq)
		} else if width > 0 && col+width > skipCols {
			// A covered half of a wide character cannot be rendered separately.
			buf.WriteString(strings.Repeat(" ", col+width-skipCols))
		}
		col += width
		s = s[n:]
	}
	return buf.String() + s
}
