package ui

import (
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
)

type activityState struct {
	spinner spinner.Model
	pending map[uint64]string
	nextID  uint64
	ticking bool
}

func newActivityState() activityState {
	return activityState{
		spinner: spinner.New(spinner.WithSpinner(spinner.Spinner{
			Frames: []string{"●∙∙", "∙●∙", "∙∙●", "∙●∙"},
			FPS:    180 * time.Millisecond,
		})),
		pending: make(map[uint64]string),
	}
}

type activityDoneMsg struct {
	id  uint64
	msg tea.Msg
}

func (a *App) withActivity(label string, cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	a.activity.nextID++
	id := a.activity.nextID
	a.activity.pending[id] = label
	return func() tea.Msg {
		return activityDoneMsg{id: id, msg: cmd()}
	}
}

func (a *App) updateActivity(msg tea.Msg) (tea.Msg, tea.Cmd, bool) {
	switch m := msg.(type) {
	case activityDoneMsg:
		delete(a.activity.pending, m.id)
		return m.msg, nil, false
	case spinner.TickMsg:
		if m.ID > 0 && m.ID != a.activity.spinner.ID() {
			return nil, nil, true
		}
		if !a.opRunning && len(a.activity.pending) == 0 {
			a.activity.ticking = false
			return nil, nil, true
		}
		var cmd tea.Cmd
		a.activity.spinner, cmd = a.activity.spinner.Update(m)
		return nil, cmd, true
	}
	return msg, nil, false
}

func (a *App) cmdActivityTick() tea.Cmd {
	if a.activity.ticking || !a.opRunning && len(a.activity.pending) == 0 {
		return nil
	}
	a.activity.ticking = true
	return a.activity.spinner.Tick
}

func (a *App) activityLabel() string {
	if a.opRunning {
		return a.opName
	}
	var latest uint64
	for id := range a.activity.pending {
		if id > latest {
			latest = id
		}
	}
	return a.activity.pending[latest]
}

func (a *App) activityView() string {
	label := a.activityLabel()
	if label == "" {
		return ""
	}
	return styleHotkeyKey.Render(label + " " + a.activity.spinner.View())
}
