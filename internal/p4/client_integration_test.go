package p4

import (
	"context"
	"errors"
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
	c := &Client{Port: port, User: "audit", Workspace: "audit", Root: root}
	startServer := func() *exec.Cmd {
		t.Helper()
		server := exec.Command(p4d, "-r", db, "-p", port, "-L", filepath.Join(base, "server.log"), "-J", filepath.Join(base, "journal"))
		configureP4Command(server)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { server.Process.Kill(); server.Wait() })
		deadline := time.Now().Add(10 * time.Second)
		for {
			if _, err := c.run("info"); err == nil {
				return server
			}
			if time.Now().After(deadline) {
				t.Fatal("local p4d did not start")
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	server := startServer()
	server.Process.Kill()
	server.Wait()
	// New databases on p4d 2026.1 apply secure defaults during their first startup.
	for _, setting := range []string{"security=0", "dm.user.noautocreate=0", "dm.user.setinitialpasswd=1"} {
		cmd := exec.Command(p4d, "-r", db, "-cset "+setting)
		configureP4Command(cmd)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("configure fixture: %v: %s", err, out)
		}
	}
	startServer()
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
	t.Run("sync status respects mapping and empty have", func(t *testing.T) {
		testNativeSyncStatus(t, c)
	})
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
	t.Run("local operations preserve reserved filenames and wildcard scope", func(t *testing.T) {
		special := filepath.Join(root, "odd#@%.txt")
		if _, err := c.Edit(special); err != nil {
			t.Fatal(err)
		}
		if err := c.RevertUnchangedPaths([]string{special}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Edit("//audit/odd%23%40%25.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.RevertFiles([]string{special}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(special, 0o644); err != nil {
			t.Fatal(err)
		}
		write(special, "offline edit\n")
		modified := time.Now().Add(2 * time.Second)
		if err := os.Chtimes(special, modified, modified); err != nil {
			t.Fatal(err)
		}
		reconcileOut, err := c.Reconcile(special)
		if err != nil {
			t.Fatal(err)
		}
		changed, err := c.HasChanges(special)
		if err != nil || !changed {
			opened, _ := c.OpenedFiles()
			t.Fatalf("reserved local change = %v, error %v, opened %#v, reconcile %q", changed, err, opened, reconcileOut)
		}
		diff, err := c.Diff(special)
		if err != nil || !strings.Contains(diff, "+offline edit") {
			t.Fatalf("reserved local diff = %q, error %v", diff, err)
		}
		if count, err := c.RestoreReadOnly(special); err != nil || count != 0 {
			t.Fatalf("opened reserved file restore count = %d, error %v", count, err)
		}
		if _, err := c.RevertFiles([]string{special}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(special, 0o644); err != nil {
			t.Fatal(err)
		}
		if count, err := c.RestoreReadOnly(special); err != nil || count != 1 {
			t.Fatalf("unopened reserved file restore count = %d, error %v", count, err)
		}
		if err := c.DeletePath(special); err != nil {
			t.Fatal(err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].Action != ActionDelete || opened[0].DepotFile != "//depot/odd%23%40%25.txt" {
			t.Fatalf("reserved local delete = %#v, error %v", opened, err)
		}
		if _, err := c.RevertFiles([]string{special}); err != nil {
			t.Fatal(err)
		}
		folder := filepath.Join(root, "folder#@%")
		direct, nested := filepath.Join(folder, "direct#@%.txt"), filepath.Join(folder, "nested", "nested#@%.txt")
		write(direct, "reserved direct file\n")
		write(nested, "reserved nested file\n")
		if _, err := c.Reconcile(direct); err != nil {
			t.Fatal(err)
		}
		opened, err = c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].Action != ActionAdd || opened[0].DepotFile != "//depot/folder%23%40%25/direct%23%40%25.txt" {
			t.Fatalf("reserved untracked reconcile = %#v, error %v", opened, err)
		}
		if _, err := c.Reconcile(filepath.Join(folder, "...")); err != nil {
			t.Fatal(err)
		}
		opened, err = c.OpenedFiles()
		if err != nil || len(opened) != 2 {
			t.Fatalf("recursive reserved reconcile = %#v, error %v", opened, err)
		}
		run("submit", "-d", "reserved directory files")
		if _, err := c.Edit(filepath.Join(folder, "*")); err != nil {
			t.Fatal(err)
		}
		opened, err = c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//depot/folder%23%40%25/direct%23%40%25.txt" {
			t.Fatalf("direct wildcard escaped its scope: %#v, error %v", opened, err)
		}
		if err := c.RevertUnchangedPaths([]string{filepath.Join(folder, "*")}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Edit(filepath.Join(folder, "...")); err != nil {
			t.Fatal(err)
		}
		opened, err = c.OpenedFiles()
		if err != nil || len(opened) != 2 {
			t.Fatalf("recursive reserved edit = %#v, error %v", opened, err)
		}
		if _, err := c.RevertFiles([]string{filepath.Join(folder, "...")}); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(nested, 0o644); err != nil {
			t.Fatal(err)
		}
		write(nested, "offline nested edit\n")
		if err := os.Chtimes(nested, modified, modified); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Reconcile(filepath.Join(folder, "...")); err != nil {
			t.Fatal(err)
		}
		if count, err := c.RestoreReadOnly(filepath.Join(folder, "...")); err != nil || count != 1 {
			t.Fatalf("recursive restore touched opened file: count %d, error %v", count, err)
		}
		if _, err := c.RevertFiles([]string{nested}); err != nil {
			t.Fatal(err)
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
	t.Run("discard identifies only successfully reverted adds", func(t *testing.T) {
		cl, err := c.CreateChange("discard confirmed adds")
		if err != nil {
			t.Fatal(err)
		}
		otherCL, err := c.CreateChange("preserve moved add")
		if err != nil {
			t.Fatal(err)
		}
		selected := filepath.Join(root, "discard space#@%23.txt")
		moved := filepath.Join(root, "discard moved.txt")
		write(selected, "discard this add\n")
		write(moved, "preserve moved content\n")
		run("add", "-f", "-c", cl, selected, moved)
		confirmed := []string{"//audit/discard space%23%40%2523.txt", "//audit/discard moved.txt"}
		if _, err := c.Reopen(otherCL, confirmed[1]); err != nil {
			t.Fatal(err)
		}
		result, err := c.RevertForDiscard(cl, confirmed)
		if err != nil || len(result.Files) != 1 || result.Files[0] != selected || len(result.AddedLocalFiles) != 1 || result.AddedLocalFiles[0] != selected {
			t.Fatalf("confirmed discarded adds = %#v, error %v", result, err)
		}
		result, err = c.RevertForDiscard(cl, []string{confirmed[1]})
		if err != nil || len(result.Files) != 0 || len(result.AddedLocalFiles) != 0 {
			t.Fatalf("skipped add was reported as reverted: %#v, error %v", result, err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].Change != otherCL {
			t.Fatalf("moved add lost changelist: %#v, error %v", opened, err)
		}
		content, err := os.ReadFile(moved)
		if err != nil || string(content) != "preserve moved content\n" {
			t.Fatalf("moved add lost content: %q, error %v", content, err)
		}
		if _, err := c.RevertFiles([]string{confirmed[1]}); err != nil {
			t.Fatal(err)
		}
		for _, id := range []string{cl, otherCL} {
			if err := c.DeleteChange(id); err != nil {
				t.Fatal(err)
			}
		}
		run("edit", a)
		result, err = c.RevertForDiscard("", []string{"//audit/a.txt"})
		if err != nil || len(result.Files) != 1 || result.Files[0] != a || len(result.AddedLocalFiles) != 0 {
			t.Fatalf("tracked edit revert result = %#v, error %v", result, err)
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
	t.Run("canceled submit preserves pending changelist", func(t *testing.T) {
		cl, err := c.CreateChange("keep description")
		if err != nil {
			t.Fatal(err)
		}
		run("edit", "-c", cl, a)
		write(a, "keep canceled edit\n")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := c.Submit(ctx, cl, "canceled description", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled submit error = %v", err)
		}
		descriptions, err := c.PendingDescriptions()
		if err != nil || descriptions[cl] != "keep description" {
			t.Fatalf("canceled submit changed description: %#v, error %v", descriptions, err)
		}
		files, err := c.OpenedFiles()
		if err != nil || len(files) != 1 {
			t.Fatalf("canceled submit opened files = %#v, error %v", files, err)
		}
		if err := c.SubmitMarked(ctx, files, "canceled marked submit", nil); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled marked submit error = %v", err)
		}
		files, err = c.OpenedFiles()
		if err != nil || len(files) != 1 || files[0].Change != cl {
			t.Fatalf("canceled marked submit moved files: %#v, error %v", files, err)
		}
		descriptions, err = c.PendingDescriptions()
		if err != nil || len(descriptions) != 1 {
			t.Fatalf("canceled marked submit created a change: %#v, error %v", descriptions, err)
		}
		run("revert", a)
		if err := c.DeleteChange(cl); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("submit surfaces server errors", func(t *testing.T) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := c.Submit(ctx, "default", "empty submit", make(chan string, 256)); err == nil || !strings.Contains(err.Error(), "No files to submit") {
			t.Fatalf("empty submit error = %v", err)
		}
	})
	t.Run("scoped resolve preserves other conflicts", func(t *testing.T) {
		selectedPath := filepath.Join(root, "odd#@%.txt")
		if _, err := c.Edit(selectedPath); err != nil {
			t.Fatal(err)
		}
		run("edit", b)
		write(selectedPath, "remote A\n")
		write(b, "remote B\n")
		run("submit", "-d", "resolve head revisions")
		run("sync", "//depot/odd%23%40%25.txt#1", "//depot/b.txt#1")
		if _, err := c.Edit(selectedPath); err != nil {
			t.Fatal(err)
		}
		run("edit", b)
		write(selectedPath, "local A\n")
		write(b, "local B\n")
		if _, err := c.SyncPath(selectedPath); err != nil {
			t.Fatal(err)
		}
		run("sync", b)
		conflicts, err := c.ResolveList("")
		if err != nil || len(conflicts) != 2 {
			t.Fatalf("conflicts = %#v, error %v", conflicts, err)
		}
		selected, err := c.ResolveList(selectedPath)
		if err != nil || len(selected) != 1 || filepath.Clean(selected[0].ClientFile) != selectedPath {
			t.Fatalf("selected conflicts = %#v, error %v", selected, err)
		}
		if err := c.AutoResolve(selected[0].ClientFile, []string{"-at"}); err != nil {
			t.Fatal(err)
		}
		remaining, err := c.ResolveList("")
		if err != nil || len(remaining) != 1 || filepath.Clean(remaining[0].ClientFile) != b {
			t.Fatalf("unselected resolve was lost: %#v, error %v", remaining, err)
		}
		content, err := os.ReadFile(b)
		if err != nil || string(content) != "local B\n" {
			t.Fatalf("unselected content = %q, error %v", content, err)
		}
		if _, err := c.RevertFiles([]string{selectedPath, b}); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("partial unshelve does not count filename success markers", func(t *testing.T) {
		blocked := filepath.Join(root, "blocked - unshelved.txt")
		write(blocked, "blocked base\n")
		run("add", blocked)
		run("submit", "-d", "unshelve marker base")
		cl, err := c.CreateChange("unshelve marker shelf")
		if err != nil {
			t.Fatal(err)
		}
		run("edit", "-c", cl, a, blocked)
		write(a, "shelved selected edit\n")
		write(blocked, "preserve blocked shelf content\n")
		if _, err := c.Shelve(cl); err != nil {
			t.Fatal(err)
		}
		run("revert", a, blocked)
		run("delete", blocked)
		if err := c.UnshelveAndDelete(cl); err == nil {
			t.Fatal("partial unshelve succeeded because a filename contained the success marker")
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 2 {
			t.Fatalf("partial unshelve lost shelf: %#v, error %v", files, err)
		}
		run("revert", a, blocked)
		if err := c.DeleteShelf(cl); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteChange(cl); err != nil {
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
	t.Run("failed unshelve preserves entire shelf", func(t *testing.T) {
		run("delete", a, b)
		if err := c.UnshelveAndDelete(cl); err == nil {
			t.Fatal("failed unshelve unexpectedly succeeded")
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 2 {
			t.Fatalf("failed unshelve lost shelf: files %#v, error %v", files, err)
		}
		run("revert", a, b)
	})
	run("delete", b)
	t.Run("partial unshelve preserves entire shelf", func(t *testing.T) {
		if err := c.UnshelveAndDelete(cl); err == nil {
			t.Fatal("partial unshelve unexpectedly succeeded")
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 2 {
			t.Fatalf("shelf was lost: files %#v, error %v", files, err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 2 {
			t.Fatalf("partial unshelve opened files = %#v, error %v", opened, err)
		}
		for _, file := range opened {
			if (file.DepotFile == "//depot/a.txt" && file.Action != "edit") || (file.DepotFile == "//depot/b.txt" && file.Action != "delete") {
				t.Fatalf("partial unshelve changed the blocked file: %#v", opened)
			}
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
	t.Run("invalid stream combinations leave workspace unchanged", func(t *testing.T) {
		for _, child := range []string{"", "//streams/main", "//streams/missing"} {
			if _, err := c.MergeStream(child); err == nil {
				t.Fatalf("invalid promotion from %q succeeded", child)
			}
		}
		for _, target := range []string{"//streams/main", "//streams/missing"} {
			if _, err := c.CopyStream(target); err == nil {
				t.Fatalf("invalid merge target %q succeeded", target)
			}
		}
		c.Workspace, c.Root, c.Stream = "audit", root, ""
		if _, err := c.MergeStream("//streams/dev"); err == nil || !strings.Contains(err.Error(), "not mapped to a stream") {
			t.Fatalf("promotion from classic workspace error = %v", err)
		}
		if _, err := c.CopyStream("//streams/dev"); err == nil || !strings.Contains(err.Error(), "not mapped to a stream") {
			t.Fatalf("merge into classic workspace error = %v", err)
		}
		c.Workspace, c.Root, c.Stream = "stream-child", childRoot, "//streams/dev"
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 0 {
			t.Fatalf("invalid stream operations opened files: %#v, error %v", opened, err)
		}
	})
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
	t.Run("scoped shelf cleanup preserves switched stream edits", func(t *testing.T) {
		previous := *c
		defer func() { *c = previous }()
		input("Stream: //streams/shelf-safe\nOwner: audit\nName: shelf-safe\nParent: //streams/main\nType: development\nParentView: inherit\nOptions: allsubmit unlocked toparent fromparent mergedown\nPaths:\n\tshare ...\n", "stream", "-i")
		run("populate", "-d", "shelf-safe base", "//streams/main/...", "//streams/shelf-safe/...")
		switchRoot := filepath.Join(base, "shelf-switch")
		if err := os.MkdirAll(switchRoot, 0o755); err != nil {
			t.Fatal(err)
		}
		input(fmt.Sprintf("Client: shelf-switch\nOwner: audit\nRoot: %s\nStream: //streams/main\n", switchRoot), "client", "-i")
		c.Workspace, c.Root, c.Stream = "shelf-switch", switchRoot, "//streams/main"
		run("sync")
		if err := c.SwitchToStream("//streams/shelf-safe"); err != nil {
			t.Fatal(err)
		}
		local := filepath.Join(switchRoot, "main.txt")
		clientPath := "//shelf-switch/main.txt"
		run("edit", clientPath)
		write(local, "preserve destination stream edits\n")
		if err := c.SwitchToStream("//streams/main"); err != nil {
			t.Fatal(err)
		}
		run("edit", clientPath)
		write(local, "selected source shelf\n")
		cl, err := c.CreateChange("selected source shelf")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := c.Reopen(cl, clientPath); err != nil {
			t.Fatal(err)
		}
		if _, err := c.ShelveFiles(cl, []string{clientPath}); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Reopen("default", clientPath); err != nil {
			t.Fatal(err)
		}
		if err := c.SwitchToStream("//streams/shelf-safe"); err != nil {
			t.Fatal(err)
		}
		if _, err := c.RevertCL(cl, []string{clientPath}); err != nil {
			t.Fatal(err)
		}
		content, err := os.ReadFile(local)
		if err != nil || string(content) != "preserve destination stream edits\n" {
			t.Fatalf("shelf cleanup discarded switched edits: %q, error %v", content, err)
		}
		opened, err := c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//streams/shelf-safe/main.txt" {
			t.Fatalf("shelf cleanup reverted destination: %#v, error %v", opened, err)
		}
		run("revert", clientPath)
		c.Stream = "//streams/shelf-safe"
		if err := c.UnshelveAndDelete(cl); err == nil {
			t.Fatal("cross-stream unshelve deleted a shelf whose identities could not be verified")
		}
		opened, err = c.OpenedFiles()
		if err != nil || len(opened) != 1 || opened[0].DepotFile != "//streams/shelf-safe/main.txt" {
			t.Fatalf("cross-stream retry did not unshelve destination: %#v, error %v", opened, err)
		}
		files, err := c.shelvedFiles(cl)
		if err != nil || len(files) != 1 || files[0].DepotFile != "//streams/main/main.txt" {
			t.Fatalf("cross-stream retry lost source shelf: %#v, error %v", files, err)
		}
		run("revert", clientPath)
		if err := c.DeleteShelf(cl); err != nil {
			t.Fatal(err)
		}
		if err := c.DeleteChange(cl); err != nil {
			t.Fatal(err)
		}
	})
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
		run("configure", "set", "security=3")
		input(password+"\n"+password+"\n", "passwd")
		if err := c.Login(password); err != nil {
			t.Fatal(err)
		}
		run("logout")
		if c.TicketValid() {
			t.Fatal("fixture retained its login ticket")
		}
		info, err := c.Info()
		if err != nil || info.Client != c.Workspace || info.User != c.User {
			t.Fatalf("unauthenticated info %#v, error %v", info, err)
		}
	})
	t.Run("TLS requires explicitly verified fingerprint", func(t *testing.T) {
		sslDir, sslDB := filepath.Join(base, "ssl"), filepath.Join(base, "ssl-db")
		for _, dir := range []string{sslDir, sslDB} {
			if err := os.MkdirAll(dir, 0o700); err != nil {
				t.Fatal(err)
			}
		}
		t.Setenv("P4SSLDIR", sslDir)
		certificate := exec.Command(p4d, "-Gc")
		configureP4Command(certificate)
		if out, err := certificate.CombinedOutput(); err != nil {
			t.Fatalf("generate fixture certificate: %v: %s", err, out)
		}
		fingerprintCmd := exec.Command(p4d, "-Gf")
		configureP4Command(fingerprintCmd)
		out, err := fingerprintCmd.Output()
		if err != nil {
			t.Fatal(err)
		}
		fingerprint := strings.TrimSpace(strings.TrimPrefix(string(out), "Fingerprint:"))
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		sslPort := "ssl:" + listener.Addr().String()
		listener.Close()
		server := exec.Command(p4d, "-r", sslDB, "-p", sslPort, "-L", filepath.Join(base, "ssl-server.log"), "-J", filepath.Join(base, "ssl-journal"))
		configureP4Command(server)
		if err := server.Start(); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { server.Process.Kill(); server.Wait() })
		secure := &Client{Port: sslPort, User: "audit", Workspace: "audit", StorePassword: true}
		deadline := time.Now().Add(10 * time.Second)
		for {
			_, err = secure.Info()
			if err != nil && strings.Contains(strings.ToLower(err.Error()), "fingerprint") {
				break
			}
			if time.Now().After(deadline) {
				t.Fatalf("TLS fixture did not reject unknown fingerprint: %v", err)
			}
			time.Sleep(20 * time.Millisecond)
		}
		mockCredentials(t, func(_, _ string) (string, error) {
			t.Fatal("untrusted server requested a stored credential")
			return "", nil
		}, func(_, _, _ string) error {
			t.Fatal("untrusted server stored a credential")
			return nil
		})
		if err := secure.EnsureLoggedIn(func(string) (string, error) {
			t.Fatal("untrusted server prompted for a credential")
			return "", nil
		}); err == nil || !strings.Contains(err.Error(), "cannot verify Perforce server") {
			t.Fatalf("unknown fingerprint authentication error = %v", err)
		}
		if trust, err := os.ReadFile(os.Getenv("P4TRUST")); err == nil && len(trust) != 0 {
			t.Fatal("unknown fingerprint was accepted automatically")
		}
		// The expected fingerprint comes directly from this owned server's certificate.
		if _, err := secure.run("trust", "-i", fingerprint); err != nil {
			t.Fatal(err)
		}
		if _, err := secure.Info(); err != nil {
			t.Fatalf("explicitly verified trust was rejected: %v", err)
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
