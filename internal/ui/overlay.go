package ui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// placeOverlay renders fg on top of bg at position (x, y).
// Left of fg preserves bg ANSI; right of fg loses ANSI styling (acceptable for overlays).
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

	// Right: bg from column x+fgW, without ANSI (reset before to avoid bleed).
	right := ansiSkip(bg, x+fgW)

	return left + "\033[0m" + fg + "\033[0m" + right
}

// ansiTruncate truncates s to maxWidth visible columns, preserving ANSI sequences.
func ansiTruncate(s string, maxWidth int) string {
	if maxWidth <= 0 {
		return ""
	}
	var buf strings.Builder
	col := 0
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == '\033' {
			buf.WriteRune('\033')
			i++
			for i < len(runes) {
				buf.WriteRune(runes[i])
				end := isAnsiEnd(runes[i])
				i++
				if end {
					break
				}
			}
			continue
		}
		if col >= maxWidth {
			break
		}
		buf.WriteRune(runes[i])
		col++
		i++
	}
	return buf.String()
}

// ansiSkip skips the first skipCols visible columns and returns the rest (without ANSI codes).
func ansiSkip(s string, skipCols int) string {
	plain := ansiStrip(s)
	runes := []rune(plain)
	if skipCols >= len(runes) {
		return ""
	}
	return string(runes[skipCols:])
}

func ansiStrip(s string) string {
	var buf strings.Builder
	runes := []rune(s)
	for i := 0; i < len(runes); {
		if runes[i] == '\033' {
			i++
			for i < len(runes) && !isAnsiEnd(runes[i]) {
				i++
			}
			i++ // skip terminator
			continue
		}
		buf.WriteRune(runes[i])
		i++
	}
	return buf.String()
}

// isAnsiEnd returns true if r is the final byte of a CSI escape sequence.
// '[' (0x5B) is excluded because it is the CSI introducer, not a terminator.
func isAnsiEnd(r rune) bool {
	return r >= 0x40 && r <= 0x7E && r != '['
}
