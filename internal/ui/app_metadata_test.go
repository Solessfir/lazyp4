package ui

import (
	"errors"
	"os"
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/solessfir/lazyp4/internal/p4"
)

func TestMetadataRejectsObsoleteResponses(t *testing.T) {
	for _, kind := range []string{"current CL", "streams", "fetch", "shelves"} {
		for _, invalidate := range []string{"new query", "info query"} {
			t.Run(kind+"/"+invalidate, func(t *testing.T) {
				fakeP4(t)
				a := New(&p4.Client{Workspace: "workspace", Stream: "//streams/current"}, 0, "", false)
				a.width, a.height = 120, 40
				a.relayout()
				a.log.SetEntries([]p4.FilelogEntry{{Change: "current"}, {Change: "obsolete"}})
				var oldCmd, currentCmd tea.Cmd
				switch kind {
				case "current CL":
					oldCmd, currentCmd = a.cmdFetchCurrentCL(), a.cmdFetchCurrentCL()
				case "streams":
					oldCmd, currentCmd = a.cmdStreams(a.client.Stream), a.cmdStreams(a.client.Stream)
				case "fetch":
					oldCmd, currentCmd = a.cmdFetch(), a.cmdFetch()
				case "shelves":
					oldCmd, currentCmd = a.cmdLoadShelved(), a.cmdLoadShelved()
				}
				old, current := oldCmd(), currentCmd()
				switch kind {
				case "current CL":
					o, c := old.(currentCLFetchedMsg), current.(currentCLFetchedMsg)
					o.cl, c.cl = "obsolete", "current"
					old, current = o, c
				case "streams":
					o, c := old.(streamsFetchedMsg), current.(streamsFetchedMsg)
					o.streams = []p4.StreamInfo{{Path: "//obsolete/a"}, {Path: "//obsolete/b"}}
					c.streams = []p4.StreamInfo{{Path: "//current/a"}, {Path: "//current/b"}}
					old, current = o, c
				case "fetch":
					o, c := old.(fetchDoneMsg), current.(fetchDoneMsg)
					o.count, c.count = 99, 4
					old, current = o, c
				case "shelves":
					o, c := old.(shelvedDoneMsg), current.(shelvedDoneMsg)
					o.cls, c.cls = []p4.ShelvedCL{{ID: "obsolete"}}, []p4.ShelvedCL{{ID: "current"}}
					old, current = o, c
				}
				a.Update(current)
				before := a.View()
				if invalidate == "info query" {
					a.cmdInfo()
				}
				a.Update(old)
				if a.View() != before || kind == "streams" && (len(a.streams) != 2 || a.streams[0].Path != "//current/a") {
					t.Fatal("obsolete metadata replaced the current result")
				}
			})
		}
	}
}

func TestFetchRejectsObsoleteErrors(t *testing.T) {
	fakeP4(t)
	a := New(&p4.Client{}, 0, "", false)
	oldCmd, currentCmd := a.cmdFetch(), a.cmdFetch()
	current := currentCmd().(fetchDoneMsg)
	current.count = 4
	a.Update(current)
	status := a.status
	old := oldCmd().(fetchDoneMsg)
	old.err = errors.New("Connect to server failed")
	a.Update(old)
	if a.offlineMode || a.status != status {
		t.Fatal("obsolete fetch error changed current status")
	}
	current.err = old.err
	a.Update(current)
	if !a.offlineMode {
		t.Fatal("current fetch error was hidden")
	}
}

func TestStreamSwitchQueriesMetadataAfterInfo(t *testing.T) {
	log := fakeP4(t)
	a := New(&p4.Client{Workspace: "workspace", Stream: "//streams/previous"}, 0, "", false)
	_, cmd := a.Update(streamSwitchedMsg{stream: "//streams/current"})
	batch := cmd().(tea.BatchMsg)
	for _, command := range batch {
		command()
	}
	data, _ := os.ReadFile(log)
	if strings.Contains(string(data), "//streams/previous") {
		t.Fatalf("post-switch metadata queried the previous stream before info: %s", data)
	}
	_, followup := a.Update(infoFetchedMsg{request: a.infoRequest, info: p4.WorkspaceInfo{Client: "workspace", Stream: "//streams/current"}})
	batch = followup().(tea.BatchMsg)
	seenCL, seenFetch, seenShelves := false, false, false
	for _, command := range batch {
		switch command().(type) {
		case currentCLFetchedMsg:
			seenCL = true
		case fetchDoneMsg:
			seenFetch = true
		case shelvedDoneMsg:
			seenShelves = true
		}
	}
	data, _ = os.ReadFile(log)
	if !seenCL || !seenFetch || !seenShelves || !strings.Contains(string(data), "//streams/current/...@workspace") {
		t.Fatalf("authoritative info did not query metadata using the current stream: %s", data)
	}
}
