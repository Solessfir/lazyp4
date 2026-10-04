package ui

import (
	"errors"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestStreamActivitySurvivesBackgroundLoadsAndStopsOnCompletion(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	pull := a.withActivity("Pulling", func() tea.Msg { return statusMsg{text: "Done"} })
	background := a.withActivity("Loading browser", func() tea.Msg { return statusMsg{} })
	before := a.streamActivityView()
	if !strings.Contains(before, "Pulling") {
		t.Fatalf("background load hid stream action: %q", before)
	}
	a.updateActivity(a.activity.spinner.Tick())
	if a.streamActivityView() == before {
		t.Fatal("stream spinner did not animate with the footer")
	}
	a.opRunning, a.opName = true, "Submitting"
	if !strings.Contains(a.streamActivityView(), "Submitting") {
		t.Fatal("guarded operation did not take stream display priority")
	}
	a.opRunning = false
	a.Update(pull())
	if a.streamActivityView() != "" || a.activityView() == "" {
		t.Fatal("completed stream action did not clear independently of background load")
	}
	a.Update(background())
}

func TestStreamCountHandlesFetchBeforeStreamsAndRejectsStaleOrFailedResults(t *testing.T) {
	a := New(&p4.Client{}, 0, "", false)
	a.statusPane.SetInfo(p4.WorkspaceInfo{Client: "workspace", Stream: "//depot/main"})
	a.streamsPane.SetSize(44, 6)
	a.fetchRequest, a.streamsRequest = 2, 1
	a.Update(fetchDoneMsg{request: 2, count: 5})
	a.Update(streamsFetchedMsg{request: 1, streams: []p4.StreamInfo{
		{Path: "//depot/main", Name: "main", Type: "mainline"},
		{Path: "//depot/dev", Name: "dev", Parent: "//depot/main", Type: "development"},
	}})
	a.streamsPane.SetSize(44, 6)
	if !strings.Contains(a.streamsPane.View(), "↓5") {
		t.Fatal("streams arriving after fetch lost the current workspace count")
	}
	a.Update(fetchDoneMsg{request: 1, count: 99})
	if strings.Contains(a.streamsPane.View(), "↓99") || a.statusPane.Pending() != 5 {
		t.Fatal("obsolete fetch replaced current count")
	}
	a.Update(fetchDoneMsg{request: 2, err: errors.New("query failed")})
	if strings.Contains(a.streamsPane.View(), "↓") || a.statusPane.Pending() != -1 || !strings.Contains(a.status, "query failed") {
		t.Fatal("failed fetch retained count or claimed up to date")
	}
	a.Update(fetchDoneMsg{request: 2, count: 0})
	if a.status != "No newer CLs" || strings.Contains(a.streamsPane.View(), "✓") {
		t.Fatal("zero watermark count claimed a fully synced workspace")
	}
}
