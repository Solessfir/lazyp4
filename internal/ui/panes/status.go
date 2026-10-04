package panes

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"

	"github.com/solessfir/lazyp4/internal/p4"
)

// StatusHeight is the fixed outer height of the status pane (top border + 1 content + bottom border).
const StatusHeight = 3

var (
	styleStatusPending = lipgloss.NewStyle().Foreground(lipgloss.Color("3"))
	styleStatusArrow   = lipgloss.NewStyle()
	styleStatusName    = lipgloss.NewStyle()
	styleStatusStream  = lipgloss.NewStyle().Foreground(lipgloss.Color("2"))
	styleStatusOffline = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)
)

// StatusPane displays workspace info and pending sync count.
type StatusPane struct {
	info     p4.WorkspaceInfo
	pending  int  // -1 = not yet fetched
	fetching bool // fetch in progress
	offline  bool
	width    int
}

func NewStatusPane() *StatusPane {
	return &StatusPane{pending: -1}
}

func (p *StatusPane) SetWidth(w int)        { p.width = w }
func (p *StatusPane) CurrentStream() string { return p.info.Stream }
func (p *StatusPane) Pending() int          { return p.pending }

func (p *StatusPane) SetInfo(info p4.WorkspaceInfo) {
	if p.info.Client != info.Client || p.info.Stream != info.Stream {
		p.pending = -1
	}
	p.info = info
}

func (p *StatusPane) SetPending(n int) {
	p.pending = n
	p.fetching = false
}

func (p *StatusPane) SetFetching() {
	p.fetching = true
	p.pending = -1
}

func (p *StatusPane) SetOffline(offline bool) {
	p.offline = offline
	p.fetching = false
	if offline {
		p.pending = -1
	}
}

func (p *StatusPane) View() string {
	innerW := p.width - 2
	if innerW < 1 {
		innerW = 1
	}
	rendered := styleBlurBorder.Width(innerW).Height(1).Render(ansi.Truncate(p.line(), innerW, "…"))
	return injectTitle(rendered, "", "Status", p.width, false)
}

func (p *StatusPane) line() string {
	if p.offline {
		offlineBadge := styleStatusOffline.Render("OFFLINE")
		if p.info.Client != "" {
			return offlineBadge + " " + styleStatusName.Render(p.info.Client)
		}
		return offlineBadge + " " + styleStatusArrow.Render("p4 server unreachable")
	}
	var parts []string

	if p.pending > 0 {
		parts = append(parts, styleStatusPending.Render("↓"+fmt.Sprintf("%d", p.pending)))
	}

	stream := p.info.Stream
	if stream != "" {
		trimmed := strings.TrimPrefix(stream, "//")
		idx := strings.LastIndex(trimmed, "/")
		var streamName string
		if idx == -1 {
			streamName = trimmed
		} else {
			streamName = trimmed[idx+1:]
		}
		client := p.info.Client
		if client == "" {
			client = trimmed
		}
		if p.width > 0 {
			prefix := strings.Join(parts, " ")
			available := p.width - 2 - lipgloss.Width(prefix) - lipgloss.Width(streamName) - 3
			if prefix != "" {
				available--
			}
			client = ansi.Truncate(client, max(1, available), "…")
		}
		parts = append(parts, styleStatusName.Render(client)+" "+
			styleStatusArrow.Render("→")+" "+
			styleStatusStream.Render(streamName))
	} else if p.info.Client != "" {
		parts = append(parts, styleStatusName.Render(p.info.Client))
	}

	if len(parts) == 0 {
		if p.fetching {
			return styleStatusArrow.Render("Fetching...")
		}
		return styleStatusArrow.Render("press f to fetch")
	}
	return strings.Join(parts, " ")
}
