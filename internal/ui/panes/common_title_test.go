package panes

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestTitlesAndFootersStayWithinNarrowBorders(t *testing.T) {
	for _, width := range []int{3, 9, 16, 21, 32} {
		box := "╭" + strings.Repeat("─", width-2) + "╮\n│" + strings.Repeat(" ", width-2) + "│\n╰" + strings.Repeat("─", width-2) + "╯"
		for _, focused := range []bool{false, true} {
			for name, rendered := range map[string]string{
				"title":  injectTitle(box, "1", "Workspace Browser 日本語", width, focused),
				"tabs":   injectDualTitle(box, "5", "History 日本語", "Diff", true, width, focused),
				"footer": injectFooter(box, "日本語 workspace details · 2 of 20", focused),
			} {
				for _, line := range strings.Split(rendered, "\n") {
					if got := ansi.StringWidth(line); got != width {
						t.Fatalf("%s at width %d expanded to %d: %q", name, width, got, ansi.Strip(line))
					}
				}
			}
		}
	}
}
