package ui

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestActivityCompletionPreservesOverlappingWorkAndResults(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	if a.withActivity("Unused", nil) != nil || len(a.activity.pending) != 0 {
		t.Fatal("nil command started an activity")
	}
	failure := opDoneMsg{status: "discard failed", cmd: "p4 revert", result: "error"}
	first := a.withActivity("Discarding", func() tea.Msg { return failure })
	second := a.withActivity("Checking out", func() tea.Msg { return statusMsg{text: "checkout complete"} })
	if a.activityLabel() != "Checking out" || a.opRunning || a.opCancel != nil || a.opID != 0 {
		t.Fatal("activity tracking changed operation guards or selected the wrong label")
	}
	secondDone := second().(activityDoneMsg)
	a.Update(secondDone)
	if a.status != "checkout complete" || a.activityLabel() != "Discarding" || len(a.activity.pending) != 1 {
		t.Fatal("out-of-order completion cleared another activity or lost its result")
	}
	a.Update(secondDone)
	if a.activityLabel() != "Discarding" || len(a.activity.pending) != 1 {
		t.Fatal("duplicate completion cleared another activity")
	}
	a.Update(first())
	if a.status != failure.status || len(a.activity.pending) != 0 || a.activityView() != "" {
		t.Fatal("failed command lost its result or remained active")
	}

	a.fetchRequest = 2
	stale := a.withActivity("Fetching", func() tea.Msg {
		return fetchDoneMsg{request: 1, err: errors.New("obsolete fetch failure")}
	})
	a.Update(stale())
	if len(a.activity.pending) != 0 || a.status != failure.status {
		t.Fatal("obsolete response remained active or bypassed its existing request guard")
	}
}

func TestActivitySpinnerRunsOneChainAndRestarts(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	if a.activity.spinner.Spinner.FPS != 180*time.Millisecond || a.cmdActivityTick() != nil {
		t.Fatal("spinner cadence or idle state is incorrect")
	}
	done := a.withActivity("Loading", func() tea.Msg { return statusMsg{} })
	start := a.cmdActivityTick()
	if start == nil || !a.activity.ticking || a.cmdActivityTick() != nil {
		t.Fatal("activity did not start exactly one tick chain")
	}
	before := a.activity.spinner.View()
	_, next, handled := a.updateActivity(start())
	if !handled || next == nil || a.activity.spinner.View() == before || a.cmdActivityTick() != nil {
		t.Fatal("spinner did not advance and maintain its tick chain")
	}
	current := a.activity.spinner.Tick()
	a.updateActivity(current)
	before = a.activity.spinner.View()
	_, next, handled = a.updateActivity(current)
	if !handled || next != nil || a.activity.spinner.View() != before {
		t.Fatal("duplicate spinner tick started another chain")
	}
	_, next, handled = a.updateActivity(spinner.TickMsg{ID: a.activity.spinner.ID() + 1})
	if !handled || next != nil || !a.activity.ticking || a.activity.spinner.View() != before {
		t.Fatal("foreign spinner tick changed activity state")
	}
	a.updateActivity(done())
	_, next, handled = a.updateActivity(a.activity.spinner.Tick())
	if !handled || next != nil || a.activity.ticking || a.cmdActivityTick() != nil {
		t.Fatal("idle spinner did not stop its tick chain")
	}
	a.withActivity("Reloading", func() tea.Msg { return statusMsg{} })
	start = a.cmdActivityTick()
	if start == nil || a.cmdActivityTick() != nil {
		t.Fatal("new activity did not restart exactly one tick chain")
	}
	_, next, handled = a.updateActivity(start())
	if !handled || next == nil || a.activity.spinner.View() == before {
		t.Fatal("restarted spinner did not advance")
	}
}

func TestActivityOperationLabelTakesPriorityWithoutChangingGuards(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.withActivity("Refreshing", func() tea.Msg { return statusMsg{} })
	a.opRunning, a.opName, a.opID = true, "Submitting", 7
	cancelled := false
	a.opCancel = func() { cancelled = true }
	if a.activityLabel() != "Submitting" || !strings.Contains(a.activityView(), "Submitting") {
		t.Fatal("guarded operation did not take display priority")
	}
	a.activity.pending = make(map[uint64]string)
	start := a.cmdActivityTick()
	if start == nil {
		t.Fatal("guarded operation without a leaf activity did not animate")
	}
	a.updateActivity(start())
	if !a.opRunning || a.opName != "Submitting" || a.opID != 7 || cancelled || a.opCancel == nil {
		t.Fatal("spinner changed guarded operation state")
	}
	a.opRunning = false
	a.updateActivity(a.activity.spinner.Tick())
	if a.activity.ticking || a.activityView() != "" {
		t.Fatal("completed operation retained its activity indicator")
	}
}

func TestActivityFooterShowsActionUntilCompletion(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	done := a.withActivity("Reconciling", func() tea.Msg { return statusMsg{text: "reconcile complete"} })
	footer := ansi.Strip(a.renderHotkeys())
	if !strings.HasPrefix(footer, " Reconciling ●∙∙") || lipgloss.Width(footer) > a.width {
		t.Fatalf("action indicator is missing or exceeds footer width: %q", footer)
	}
	a.Update(done())
	footer = ansi.Strip(a.renderHotkeys())
	if strings.Contains(footer, "Reconciling") || strings.Contains(footer, "●") || !strings.Contains(footer, "reconcile complete") {
		t.Fatalf("completed action left an indicator or lost its result: %q", footer)
	}
}

func TestActivityProgressFooterRetainsCountsAndCancellation(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	a.opRunning, a.opName, a.opDone, a.opTotal = true, "Syncing", 3, 8
	cancelled := false
	a.opCancel = func() { cancelled = true }
	footer := ansi.Strip(a.renderHotkeys())
	for _, text := range []string{"Syncing ●∙∙", "3/8 files", "█", "░", "c - cancel"} {
		if !strings.Contains(footer, text) {
			t.Fatalf("progress footer lost %q: %q", text, footer)
		}
	}
	if lipgloss.Width(footer) > a.width || cancelled || !a.opRunning || a.opDone != 3 || a.opTotal != 8 {
		t.Fatal("activity rendering changed progress state or exceeded footer width")
	}
	a.Update(tea.KeyMsg{Type: tea.KeyRunes, Runes: []rune{'c'}})
	if !cancelled || !a.opRunning {
		t.Fatal("activity indicator changed cancellation behavior")
	}
}

func TestActivityContinuesThroughModalResizeAndStopsWhenIdle(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	confirmation := &confirmModal{kind: confirmKindRevert, files: []string{"//workspace/selected.txt"}}
	a.confirm = confirmation
	done := a.withActivity("Loading", func() tea.Msg { return statusMsg{text: "load complete"} })
	start := a.cmdActivityTick()
	a.Update(tea.WindowSizeMsg{Width: 44, Height: 18})
	before := a.activity.spinner.View()
	_, next := a.Update(start())
	footer := ansi.Strip(a.renderHotkeys())
	if next == nil || a.activity.spinner.View() == before || !strings.Contains(footer, "Loading") || lipgloss.Width(footer) > 44 {
		t.Fatalf("resized modal stopped or hid activity: %q", footer)
	}
	if a.confirm != confirmation || !strings.Contains(ansi.Strip(a.View()), "Discard changes") {
		t.Fatal("activity tick or resize dismissed the confirmation")
	}
	a.Update(done())
	if a.confirm != confirmation || a.status != "load complete" || a.activityView() != "" {
		t.Fatal("modal captured asynchronous completion or retained its indicator")
	}
	_, next = a.Update(a.activity.spinner.Tick())
	if next != nil || a.activity.ticking || a.cmdActivityTick() != nil {
		t.Fatal("completed activity kept a tick chain running behind the modal")
	}
	if strings.Contains(ansi.Strip(a.renderHotkeys()), "Loading") {
		t.Fatal("idle resized footer retained an activity label")
	}
}
