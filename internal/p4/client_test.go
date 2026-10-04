package p4

import (
	"context"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestScopedOperationsRejectEmptySelections(t *testing.T) {
	client := &Client{}
	for _, clID := range []string{"", "default", "not-a-number", "0"} {
		if _, err := client.ShelveFiles(clID, []string{"//client/file.txt"}); err == nil || !strings.Contains(err.Error(), "numbered changelist") {
			t.Fatalf("invalid shelf changelist %q: %v", clID, err)
		}
	}
	if _, err := client.ShelveFiles("123", nil); err == nil || !strings.Contains(err.Error(), "select files") {
		t.Fatalf("empty shelf selection: %v", err)
	}
	for _, input := range []struct {
		clID  string
		files []string
	}{{"", []string{"//client/file.txt"}}, {"default", nil}} {
		if _, err := client.RevertCL(input.clID, input.files); err == nil || !strings.Contains(err.Error(), "selected files") {
			t.Fatalf("empty revert scope: %v", err)
		}
	}
	if err := client.SubmitMarked(context.Background(), nil, "description", nil); err == nil || !strings.Contains(err.Error(), "select files") {
		t.Fatalf("empty marked submit: %v", err)
	}
	if _, err := client.BrowserHaveFiles(""); err == nil || !strings.Contains(err.Error(), "file pattern") {
		t.Fatalf("empty have pattern: %v", err)
	}
}

func TestStructuredOutputRejectsIncompleteRecords(t *testing.T) {
	for _, output := range []string{"", " \n", "{\"change\":1}\n{\"change\":2}\n"} {
		if got, err := validateStructuredOutput(output); err != nil || got != output {
			t.Fatalf("valid output %q: got %q, error %v", output, got, err)
		}
	}
	for _, output := range []string{"{\"change\":1}\n{\"change\":", "garbage", "null", "[]"} {
		if got, err := validateStructuredOutput(output); err == nil || got != "" {
			t.Fatalf("invalid output %q: got %q, error %v", output, got, err)
		}
	}
}

func TestStructuredRecordsPreserveDescriptions(t *testing.T) {
	output := "{\"change\":123,\"desc\":\"first line\\n\\nlast paragraph\\n\"}\n" +
		"{\"data\":\"informational message\",\"severity\":1}\n" +
		"{\"depotFile\":\"//depot/file\",\"rev0\":2,\"rev1\":1}\n"
	records := parseZtag(output)
	if len(records) != 2 || records[0]["change"] != "123" || records[0]["desc"] != "first line\n\nlast paragraph\n" || records[1]["rev1"] != "1" {
		t.Fatalf("structured output lost fields: %#v", records)
	}
}

func TestResolveCommandKeepsScopeAndConnection(t *testing.T) {
	c := &Client{Port: "server:1666", User: "user", Workspace: "workspace", Root: t.TempDir()}
	cmd, err := c.ResolveCommand("//depot/one file.txt")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"p4", "-p", c.Port, "-u", c.User, "-c", c.Workspace, "resolve", "//depot/one file.txt"}
	if !reflect.DeepEqual(cmd.Args, want) || cmd.Dir != c.Root {
		t.Fatalf("resolve command = %#v, dir %q", cmd.Args, cmd.Dir)
	}
	if _, err := c.ResolveCommand(""); err == nil {
		t.Fatal("interactive resolve accepted an empty scope")
	}
	local := filepath.Join(c.Root, "odd#@%.txt")
	cmd, err = c.ResolveCommand(local)
	if err != nil || cmd.Args[len(cmd.Args)-1] != escapeFileSpec(local) {
		t.Fatalf("local resolve command = %#v, error %v", cmd, err)
	}
}

func TestFileSpecsPreserveServerSyntaxAndLocalScope(t *testing.T) {
	for path, want := range map[string]string{
		"odd#@%.txt":                       "odd%23%40%25.txt",
		"literal%23.txt":                   "literal%2523.txt",
		"folder#@%/...":                    "folder%23%40%25/...",
		"folder#@%/*":                      "folder%23%40%25/*",
		`C:\folder#@%\*`:                   `C:\folder%23%40%25\*`,
		"*":                                "*",
		"...":                              "...",
		"/tmp/literal*name.txt":            "/tmp/literal%2Aname.txt",
		"//depot/odd%23%40%25.txt#2":       "//depot/odd%23%40%25.txt#2",
		"//workspace/odd%23%40%25.txt@123": "//workspace/odd%23%40%25.txt@123",
		"//workspace/folder%23%40%25/...":  "//workspace/folder%23%40%25/...",
	} {
		if got := fileSpec(path); got != want {
			t.Errorf("fileSpec(%q) = %q; want %q", path, got, want)
		}
	}
}

func TestWorkspaceDirectoryRejectsTraversal(t *testing.T) {
	c := &Client{Workspace: "workspace", Root: t.TempDir()}
	if _, _, err := c.workspaceDirectory("//workspace/../outside"); err == nil {
		t.Fatal("workspace path escaped its root")
	}
}
