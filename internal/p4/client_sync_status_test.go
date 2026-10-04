package p4

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func TestSyncDryRunMappedChangelists(t *testing.T) {
	for _, test := range []struct {
		name      string
		have      string
		head      string
		haveError string
		headError string
		count     int
		next      int
		current   string
		wantError bool
	}{
		{name: "gapped changelists", have: "Change 100 on date by user@client 'have'\n", head: "Change 180 on date by user@client 'head'\nChange 105 on date by user@client 'older'\n", count: 2, next: 101, current: "100"},
		{name: "already at highest changelist", have: "Change 100 on date by user@client 'have'\n", next: 101, current: "100"},
		{name: "empty have counts all mapped changes", head: "Change 180 on date by user@client 'head'\nChange 105 on date by user@client 'older'\n", count: 2, next: 1},
		{name: "empty mapped depot", next: 1},
		{name: "have connection error", haveError: "Connect to server failed", wantError: true},
		{name: "head connection error", have: "Change 100 on date by user@client 'have'\n", headError: "Connect to server failed", next: 101, current: "100", wantError: true},
		{name: "no such file error is not a zero count", have: "Change 100 on date by user@client 'have'\n", headError: "no such file", next: 101, current: "100", wantError: true},
		{name: "unexpected have record", have: "unexpected output\n", wantError: true},
		{name: "invalid have number", have: "Change not-a-number on date\n", wantError: true},
		{name: "zero have number", have: "Change 0 on date\n", wantError: true},
		{name: "multiple have records", have: "Change 100 on date\nChange 99 on date\n", wantError: true},
		{name: "unexpected head record after success", have: "Change 100 on date\n", head: "Change 105 on date\nunexpected output\n", next: 101, current: "100", wantError: true},
		{name: "invalid head number", have: "Change 100 on date\n", head: "Change -1 on date\n", next: 101, current: "100", wantError: true},
		{name: "head warning is not a zero count", have: "Change 100 on date\n", head: "file(s) up-to-date.\n", next: 101, current: "100", wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			log := authCLI(t)
			t.Setenv("LAZYP4_AUTH_CHANGES_HELPER", "1")
			t.Setenv("LAZYP4_AUTH_CHANGES_HAVE", test.have)
			t.Setenv("LAZYP4_AUTH_CHANGES_HEAD", test.head)
			t.Setenv("LAZYP4_AUTH_CHANGES_HAVE_ERROR", test.haveError)
			t.Setenv("LAZYP4_AUTH_CHANGES_HEAD_ERROR", test.headError)
			client := &Client{Workspace: "mapped-client", Stream: "//streams/main"}
			count, err := client.SyncDryRun()
			if count != test.count || (err != nil) != test.wantError {
				t.Fatalf("SyncDryRun() = %d, %v; want %d, error=%v", count, err, test.count, test.wantError)
			}
			if current := client.CurrentCL(); current != test.current {
				t.Fatalf("CurrentCL() = %q; want %q", current, test.current)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			haveCommand := "-c mapped-client changes -s submitted -m1 //mapped-client/...#have"
			wantCommands := []string{haveCommand}
			if test.next != 0 {
				wantCommands = append(wantCommands, "-c mapped-client changes -s submitted -e "+strconv.Itoa(test.next)+" //mapped-client/...")
			}
			wantCommands = append(wantCommands, haveCommand)
			if commands := strings.Split(strings.TrimSpace(string(data)), "\n"); !reflect.DeepEqual(commands, wantCommands) {
				t.Fatalf("commands = %q; want %q", commands, wantCommands)
			}
		})
	}
}

func TestSyncStatusScopeFallback(t *testing.T) {
	for _, test := range []struct {
		client Client
		want   string
	}{
		{client: Client{Workspace: "workspace", Stream: "//streams/main"}, want: "//workspace/..."},
		{client: Client{Stream: "//streams/main"}, want: "//streams/main/..."},
		{want: "//..."},
	} {
		t.Run(test.want, func(t *testing.T) {
			log := authCLI(t)
			t.Setenv("LAZYP4_AUTH_CHANGES_HELPER", "1")
			t.Setenv("LAZYP4_AUTH_CHANGES_HAVE", "")
			t.Setenv("LAZYP4_AUTH_CHANGES_HEAD", "Change 5 on date\n")
			t.Setenv("LAZYP4_AUTH_CHANGES_HAVE_ERROR", "")
			t.Setenv("LAZYP4_AUTH_CHANGES_HEAD_ERROR", "")
			if count, err := test.client.SyncDryRun(); err != nil || count != 1 {
				t.Fatalf("SyncDryRun() = %d, %v; want 1", count, err)
			}
			data, err := os.ReadFile(log)
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(string(data), "-m1 "+test.want+"#have\n") || !strings.HasSuffix(string(data), "-e 1 "+test.want+"\n") {
				t.Fatalf("unexpected fallback commands: %s", data)
			}
		})
	}
}

func testNativeSyncStatus(t *testing.T, client *Client) {
	t.Helper()
	run := func(c *Client, args ...string) {
		t.Helper()
		if _, err := c.run(args...); err != nil {
			t.Fatalf("p4 %v: %v", args, err)
		}
	}
	makeClient := func(name, view string) *Client {
		t.Helper()
		c := &Client{Port: client.Port, User: client.User, Workspace: name, Root: t.TempDir()}
		spec := fmt.Sprintf("Client: %s\nOwner: %s\nRoot: %s\nView:\n%s", name, c.User, c.Root, view)
		if _, err := c.runWithStdin(spec, "client", "-i"); err != nil {
			t.Fatal(err)
		}
		return c
	}
	writer := makeClient("sync-status-writer", "\t//depot/... //sync-status-writer/...\n")
	write := func(name, content string) string {
		t.Helper()
		path := filepath.Join(writer.Root, name)
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return path
	}
	status := *client
	status.Stream = "//streams/unrelated"
	baseline := status.CurrentCL()
	if baseline == "" {
		t.Fatal("native #have query did not identify the synced changelist")
	}
	excluded := write("excluded.txt", "excluded from the audited mapping\n")
	run(writer, "add", excluded)
	run(writer, "submit", "-d", "excluded changelist gap")
	if current := status.CurrentCL(); current != baseline {
		t.Fatalf("excluded change moved current CL from %s to %s", baseline, current)
	}
	if count, err := status.SyncDryRun(); err != nil || count != 0 {
		t.Fatalf("excluded change counted as mapped: %d, %v", count, err)
	}
	included := write("sync-status.txt", "first mapped revision\n")
	run(writer, "add", included)
	run(writer, "submit", "-d", "new mapped file")
	run(writer, "edit", included)
	write("sync-status.txt", "second mapped revision\n")
	run(writer, "submit", "-d", "mapped update")
	if count, err := status.SyncDryRun(); err != nil || count != 2 {
		t.Fatalf("gapped mapped changelist count = %d, %v; want 2", count, err)
	}
	empty := makeClient("sync-status-empty", "\t//depot/... //sync-status-empty/...\n\t-//depot/excluded.txt //sync-status-empty/excluded.txt\n")
	if current := empty.CurrentCL(); current != "" {
		t.Fatalf("empty have reported current CL %s", current)
	}
	if count, err := empty.SyncDryRun(); err != nil || count != 4 {
		t.Fatalf("empty have mapped changelist count = %d, %v; want 4", count, err)
	}
	run(writer, "delete", included, excluded)
	run(writer, "submit", "-d", "remove sync status fixture files")
}
