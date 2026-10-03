package p4

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// Opt in with LAZYP4_TEST_P4D pointing to p4d and p4 available in PATH.
// The fixture uses a temporary database and loopback port, never an existing server.
func TestPerforceIntegration(t *testing.T) {
	p4d := os.Getenv("LAZYP4_TEST_P4D")
	if p4d == "" {
		t.Skip("set LAZYP4_TEST_P4D and install p4 to run local-server integration tests")
	}
	if _, err := exec.LookPath("p4"); err != nil {
		t.Fatal(err)
	}
	base := t.TempDir()
	db := filepath.Join(base, "db")
	root := filepath.Join(base, "workspace with spaces")
	for _, dir := range []string{db, root} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := listener.Addr().String()
	listener.Close()
	for _, setting := range []string{"P4CONFIG", "P4PASSWD", "P4CHARSET", "P4HOST"} {
		t.Setenv(setting, "")
	}
	t.Setenv("P4PORT", port)
	t.Setenv("P4USER", "audit")
	t.Setenv("P4CLIENT", "audit")
	t.Setenv("P4ENVIRO", filepath.Join(base, "enviro"))
	t.Setenv("P4TICKETS", filepath.Join(base, "tickets"))
	t.Setenv("P4TRUST", filepath.Join(base, "trust"))
	server := exec.Command(p4d, "-r", db, "-p", port, "-v", "security=0", "-L", filepath.Join(base, "server.log"), "-J", filepath.Join(base, "journal"))
	configureP4Command(server)
	if err := server.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Process.Kill(); server.Wait() })
	c := &Client{Port: port, User: "audit", Workspace: "audit", Root: root}
	deadline := time.Now().Add(10 * time.Second)
	for {
		if _, err := c.run("info"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("local p4d did not start")
		}
		time.Sleep(20 * time.Millisecond)
	}
	run := func(args ...string) string {
		t.Helper()
		out, err := c.run(args...)
		if err != nil {
			t.Fatalf("p4 %v: %v", args, err)
		}
		return out
	}
	input := func(spec string, args ...string) {
		t.Helper()
		if _, err := c.runWithStdin(spec, args...); err != nil {
			t.Fatalf("p4 %v: %v", args, err)
		}
	}
	write := func(path, content string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	input(fmt.Sprintf("Client: audit\nOwner: audit\nRoot: %s\nView:\n\t//depot/... //audit/...\n\t//depot/old/... //audit/remapped/...\n\t-//depot/excluded.txt //audit/excluded.txt\n", root), "client", "-i")
	a, b := filepath.Join(root, "a.txt"), filepath.Join(root, "b.txt")
	mapped := filepath.Join(root, "remapped", "file with spaces.txt")
	for _, path := range []string{a, b, mapped} {
		write(path, "base\n")
		run("add", path)
	}
	run("submit", "-d", "initial")
	run("edit", a)
	write(a, "second revision\n")
	run("submit", "-d", "second revision")
	t.Run("history includes every revision", func(t *testing.T) {
		entries, err := c.Filelog("//depot/a.txt", 0)
		if err != nil || len(entries) != 2 || entries[0].Rev != 2 || entries[1].Rev != 1 {
			t.Fatalf("history = %#v, error %v", entries, err)
		}
	})
	t.Run("displayed diff compares outdated have revision", func(t *testing.T) {
		run("sync", "//depot/a.txt#1")
		run("edit", a)
		write(a, "base\nlocal addition\n")
		diff, err := c.Diff("//audit/a.txt")
		if err != nil || !strings.Contains(diff, "+local addition") || strings.Contains(diff, "second revision") {
			t.Fatalf("diff included changes beyond have revision: %q, error %v", diff, err)
		}
		run("revert", a)
		run("sync", "//depot/a.txt")
	})
	t.Run("where resolves spaces and overrides", func(t *testing.T) {
		local, err := c.WhereLocal("//depot/old/file with spaces.txt")
		if err != nil || filepath.Clean(local) != mapped {
			t.Fatalf("where = %q, error %v; want %q", local, err, mapped)
		}
		client, err := c.WhereClient("//depot/old/file with spaces.txt")
		if err != nil || client != "//audit/remapped/file with spaces.txt" {
			t.Fatalf("client mapping = %q, error %v", client, err)
		}
	})
	t.Run("browser maps classic remapped files", func(t *testing.T) {
		_, files, err := c.BrowserWorkspaceFast("//audit/remapped")
		if err != nil || len(files) != 1 || files[0].DepotPath != "//depot/old/file with spaces.txt" || files[0].ClientPath != "//audit/remapped/file with spaces.txt" || filepath.Clean(files[0].LocalPath) != mapped {
			t.Fatalf("browser files = %#v, error %v", files, err)
		}
		have, _, missing, err := c.BrowserWorkspaceStatus("//audit/remapped")
		if err != nil || len(have) != 1 || len(missing) != 0 {
			t.Fatalf("browser status = %v missing %#v, error %v", have, missing, err)
		}
		if err := os.Remove(mapped); err != nil {
			t.Fatal(err)
		}
		_, _, missing, err = c.BrowserWorkspaceStatus("//audit/remapped")
		if err != nil || len(missing) != 1 || missing[0].ClientPath != files[0].ClientPath || filepath.Clean(missing[0].LocalPath) != mapped {
			t.Fatalf("missing entries = %#v, error %v", missing, err)
		}
		run("sync", "-f", "//depot/old/...")
		paths, err := c.BrowserWorkspaceFiles("//audit/...")
		if err != nil || !contains(paths, "//audit/remapped/file with spaces.txt") {
			t.Fatalf("search paths = %v, error %v", paths, err)
		}
	})
	t.Run("browser handles excluded and special filenames", func(t *testing.T) {
		write(filepath.Join(root, "excluded.txt"), "unmapped\n")
		write(filepath.Join(root, "odd#@%.txt"), "untracked\n")
		_, files, err := c.BrowserWorkspaceFast("//audit")
		if err != nil {
			t.Fatal(err)
		}
		foundSpecial := false
		for _, file := range files {
			if file.ClientPath == "//audit/excluded.txt" {
				t.Fatal("browser exposed an excluded file as mapped")
			}
			if file.DepotPath == "//depot/odd%23%40%25.txt" && filepath.Base(file.LocalPath) == "odd#@%.txt" {
				foundSpecial = true
			}
		}
		if !foundSpecial {
			t.Fatalf("special filename lost its mapping: %#v", files)
		}
		run("add", "-f", filepath.Join(root, "odd#@%.txt"))
		run("submit", "-d", "reserved filename")
		have, err := c.BrowserHaveFiles("//audit/*")
		if err != nil || !contains(have, "//depot/odd%23%40%25.txt") {
			t.Fatalf("have lost reserved filename: %v, error %v", have, err)
		}
		have, err = c.BrowserHaveFiles("//audit/not-synced/*")
		if err != nil || len(have) != 0 {
			t.Fatalf("empty have pattern = %v, error %v", have, err)
		}
	})
	t.Run("revert unchanged preserves modifications", func(t *testing.T) {
		run("edit", a, b)
		write(a, "keep this edit\n")
		if err := c.RevertUnchanged("default"); err != nil {
			t.Fatal(err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//depot/a.txt" {
			t.Fatalf("opened after revert unchanged = %#v, error %v", opened, err)
		}
		run("revert", "//...")
	})
	t.Run("selected shelving preserves unselected files", func(t *testing.T) {
		cl, err := c.CreateChange("selected shelf")
		if err != nil {
			t.Fatal(err)
		}
		run("edit", "-c", cl, a, b)
		write(a, "selected shelf A\n")
		write(b, "unselected shelf B\n")
		if _, err := c.ShelveFiles(cl, []string{"//audit/a.txt"}); err != nil {
			t.Fatal(err)
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 1 || files[0].DepotFile != "//depot/a.txt" {
			t.Fatalf("shelf included unselected files: %#v, error %v", files, err)
		}
		if _, err := c.RevertCL(cl, []string{"//audit/a.txt", "//audit/b.txt"}); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteShelf(cl); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteChange(cl); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("confirmed changelist revert preserves later opened files", func(t *testing.T) {
		cl, err := c.CreateChange("confirmed revert")
		if err != nil {
			t.Fatal(err)
		}
		run("edit", "-c", cl, a)
		confirmed := []string{"//audit/a.txt"}
		run("edit", "-c", cl, b)
		write(b, "opened after confirmation\n")
		if _, err := c.RevertCL(cl, confirmed); err != nil {
			t.Fatal(err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//depot/b.txt" || opened[0].Change != cl {
			t.Fatalf("late opened file was reverted: %#v, error %v", opened, err)
		}
		if _, err := c.RevertCL(cl, []string{"//audit/b.txt"}); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteChange(cl); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("streamed submit stores default and numbered descriptions", func(t *testing.T) {
		for _, clID := range []string{"default", "numbered"} {
			if clID == "numbered" {
				var err error
				clID, err = c.CreateChange("old description")
				if err != nil {
					t.Fatal(err)
				}
			}
			run("edit", "-c", clID, a)
			description := "streamed submit " + clID
			write(a, description+"\n")
			lines := make(chan string, 256)
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			err := c.Submit(ctx, clID, description, lines)
			cancel()
			if err != nil {
				t.Fatal(err)
			}
			if len(lines) == 0 {
				t.Fatal("submit did not stream output")
			}
			entries, err := c.Filelog("//depot/a.txt", 1)
			if err != nil || len(entries) != 1 || entries[0].Description != description {
				t.Fatalf("submit description = %#v, error %v", entries, err)
			}
		}
	})
	t.Run("marked submit leaves other changelist files open", func(t *testing.T) {
		otherCL, err := c.CreateChange("keep other file")
		if err != nil {
			t.Fatal(err)
		}
		run("edit", a)
		run("edit", "-c", otherCL, b)
		write(a, "submit selected A\n")
		write(b, "keep unselected B\n")
		lines := make(chan string, 256)
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		err = c.SubmitMarked(ctx, []OpenedFile{{ClientFile: "//audit/a.txt", DepotFile: "//depot/a.txt", Change: "default"}}, "marked submit", lines)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//depot/b.txt" || opened[0].Change != otherCL {
			t.Fatalf("marked submit touched other files: %#v, error %v", opened, err)
		}
		entries, err := c.Filelog("//depot/a.txt", 1)
		if err != nil || len(entries) != 1 || entries[0].Description != "marked submit" {
			t.Fatalf("marked submit description = %#v, error %v", entries, err)
		}
		if _, err := c.RevertCL(otherCL, []string{"//audit/b.txt"}); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteChange(otherCL); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("change detection compares text and binary have revisions", func(t *testing.T) {
		binary := filepath.Join(root, "asset.bin")
		write(binary, "\x00binary base\x01")
		run("add", "-t", "binary", binary)
		run("submit", "-d", "binary initial")
		run("edit", a, mapped, binary)
		for _, path := range []string{a, mapped, binary} {
			changed, err := c.HasChanges(path)
			if err != nil || changed {
				t.Fatalf("unchanged %s reported %v, error %v", path, changed, err)
			}
		}
		status, err := c.FilesDiffStatus()
		if err != nil || len(status) != 0 {
			t.Fatalf("unchanged status = %#v, error %v", status, err)
		}
		write(a, "text modification\n")
		write(mapped, "text with spaces modification\n")
		write(binary, "\x00binary modification\x02")
		for _, path := range []string{a, mapped, binary} {
			changed, err := c.HasChanges(path)
			if err != nil || !changed {
				t.Fatalf("changed %s reported %v, error %v", path, changed, err)
			}
		}
		status, err = c.FilesDiffStatus()
		if err != nil || len(status) != 3 || !status["//depot/a.txt"] || !status["//depot/asset.bin"] || !status["//depot/old/file with spaces.txt"] {
			t.Fatalf("changed status = %#v, error %v", status, err)
		}
		run("revert", "//...")
	})
	cl, err := c.CreateChange("first line\n\nsecond paragraph")
	if err != nil {
		t.Fatal(err)
	}
	run("edit", "-c", cl, a, b)
	write(a, "shelved A\n")
	write(b, "shelved B\n")
	run("shelve", "-c", cl)
	t.Run("multiline shelf description preserves files", func(t *testing.T) {
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 2 {
			t.Fatalf("shelf files = %#v, error %v", files, err)
		}
		descriptions, err := c.PendingDescriptions()
		if err != nil || descriptions[cl] != "first line\n\nsecond paragraph" {
			t.Fatalf("descriptions = %#v, error %v", descriptions, err)
		}
	})
	run("revert", a, b)
	run("delete", b)
	t.Run("partial unshelve preserves entire shelf", func(t *testing.T) {
		if err := c.UnshelveAndDelete(cl); err == nil {
			t.Fatal("partial unshelve unexpectedly succeeded")
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 2 {
			t.Fatalf("shelf was lost: files %#v, error %v", files, err)
		}
	})
	run("revert", "//...")
	t.Run("complete unshelve deletes shelf", func(t *testing.T) {
		if err := c.UnshelveAndDelete(cl); err != nil {
			t.Fatal(err)
		}
		files, err := c.shelvedFiles(cl)
		if err == nil && len(files) != 0 {
			t.Fatalf("completed unshelve kept shelf: %#v", files)
		}
	})
	run("revert", "//...")
	input("Depot: streams\nOwner: audit\nDescription:\n\tIntegration fixture\nType: stream\nStreamDepth: //streams/1\nMap: streams/...\n", "depot", "-i")
	input("Stream: //streams/main\nOwner: audit\nName: main\nParent: none\nType: mainline\nParentView: inherit\nOptions: allsubmit unlocked toparent fromparent mergedown\nPaths:\n\tshare ...\n", "stream", "-i")
	input("Stream: //streams/dev\nOwner: audit\nName: dev\nParent: //streams/main\nType: development\nParentView: inherit\nOptions: allsubmit unlocked toparent fromparent mergedown\nPaths:\n\tshare ...\n\timport libs/... //depot/old/...\nRemapped:\n\t... project/...\n", "stream", "-i")
	streamRoot := filepath.Join(base, "stream workspace")
	write(filepath.Join(streamRoot, "main.txt"), "parent revision\n")
	input(fmt.Sprintf("Client: stream-parent\nOwner: audit\nRoot: %s\nStream: //streams/main\n", streamRoot), "client", "-i")
	c.Workspace, c.Root, c.Stream = "stream-parent", streamRoot, "//streams/main"
	run("add", filepath.Join(streamRoot, "main.txt"))
	run("submit", "-d", "parent initial")
	childRoot := filepath.Join(base, "child workspace")
	if err := os.MkdirAll(childRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	input(fmt.Sprintf("Client: stream-child\nOwner: audit\nRoot: %s\nStream: //streams/dev\n", childRoot), "client", "-i")
	c.Workspace, c.Root, c.Stream = "stream-child", childRoot, "//streams/dev"
	t.Run("pull and promotion respect target workspace", func(t *testing.T) {
		if _, err := c.MergeStream("//streams/dev"); err == nil || !strings.Contains(err.Error(), "parent //streams/main") {
			t.Fatalf("promotion from child workspace was accepted: %v", err)
		}
		if _, err := c.CopyStream(""); err != nil {
			t.Fatal(err)
		}
		run("resolve", "-at", "//...")
		run("submit", "-d", "pull parent")
		childFile := filepath.Join(childRoot, "project", "main.txt")
		run("edit", childFile)
		write(childFile, "child revision\n")
		run("submit", "-d", "child change")
		c.Workspace, c.Root, c.Stream = "stream-parent", streamRoot, "//streams/main"
		if _, err := c.CopyStream("//streams/dev"); err == nil {
			t.Fatal("pull from wrong workspace was accepted")
		}
		if _, err := c.MergeStream("//streams/dev"); err != nil {
			t.Fatal(err)
		}
		run("submit", "-d", "promote child")
		content, err := os.ReadFile(filepath.Join(streamRoot, "main.txt"))
		if err != nil || strings.ReplaceAll(string(content), "\r\n", "\n") != "child revision\n" {
			t.Fatalf("parent content %q, error %v", content, err)
		}
	})
	c.Workspace, c.Root, c.Stream = "stream-child", childRoot, "//streams/dev"
	run("sync")
	t.Run("browser maps stream imports and remappings", func(t *testing.T) {
		_, files, err := c.BrowserWorkspaceFast("//stream-child/project/libs")
		if err != nil || len(files) != 1 || files[0].DepotPath != "//depot/old/file with spaces.txt" || files[0].ClientPath != "//stream-child/project/libs/file with spaces.txt" {
			t.Fatalf("import files %#v, error %v", files, err)
		}
		have, _, _, err := c.BrowserWorkspaceStatus("//stream-child/project/libs")
		if err != nil || len(have) != 1 || have[0] != files[0].DepotPath {
			t.Fatalf("import status %v, error %v", have, err)
		}
	})
	t.Run("workspace info remains available before login", func(t *testing.T) {
		password := "FixturePassword123!"
		run("passwd", "-P", password)
		if err := c.Login(password); err != nil {
			t.Fatal(err)
		}
		run("configure", "set", "security=3")
		run("logout")
		if c.TicketValid() {
			t.Fatal("fixture retained its login ticket")
		}
		info, err := c.Info()
		if err != nil || info.Client != c.Workspace || info.User != c.User {
			t.Fatalf("unauthenticated info %#v, error %v", info, err)
		}
	})
}

func contains(values []string, expected string) bool {
	for _, value := range values {
		if value == expected {
			return true
		}
	}
	return false
}
