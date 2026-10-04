package ui

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/solessfir/lazyp4/internal/p4"
)

func TestDiscardDeletesOnlyConfirmedAbandonedAdds(t *testing.T) {
	for _, test := range []struct {
		name         string
		record       map[string]string
		malformed    bool
		missingPath  bool
		wantDelete   bool
		wantReported bool
		uncaptured   bool
	}{
		{name: "mixed success and skipped", record: map[string]string{"oldAction": "add", "action": "abandoned"}, wantDelete: true, wantReported: true},
		{name: "all skipped", record: map[string]string{"data": "file(s) not opened in that changelist.", "severity": "2"}},
		{name: "became edit", record: map[string]string{"oldAction": "edit", "action": "reverted"}, wantReported: true},
		{name: "uncaptured confirmed add", record: map[string]string{"oldAction": "add", "action": "abandoned"}, uncaptured: true, wantReported: true},
		{name: "missing old action", record: map[string]string{"action": "abandoned"}},
		{name: "missing local path", record: map[string]string{"oldAction": "add", "action": "abandoned"}, missingPath: true},
		{name: "unknown result", record: map[string]string{"oldAction": "add", "action": "unknown"}},
		{name: "malformed result", malformed: true},
	} {
		for _, clID := range []string{"", "12"} {
			t.Run(test.name+"/"+clID, func(t *testing.T) {
				log := fakeP4(t)
				root := t.TempDir()
				selected := filepath.Join(root, "new space#@%23.txt")
				moved := filepath.Join(root, "moved.txt")
				uncaptured := filepath.Join(root, "uncaptured.txt")
				encoded := filepath.Join(root, "new space%23%40%2523.txt")
				for _, file := range []string{selected, moved, uncaptured, encoded} {
					if err := os.WriteFile(file, []byte("preserve until confirmed"), 0o600); err != nil {
						t.Fatal(err)
					}
				}
				output := "{"
				if !test.malformed {
					record := make(map[string]string, len(test.record)+1)
					for key, value := range test.record {
						record[key] = value
					}
					if !test.missingPath {
						record["clientFile"] = selected
						if test.uncaptured {
							record["clientFile"] = uncaptured
						}
					}
					data, err := json.Marshal(record)
					if err != nil {
						t.Fatal(err)
					}
					output = string(data) + "\n"
					output += "{\"data\":\"file(s) not opened in that changelist.\",\"severity\":2}\n"
				}
				t.Setenv("LAZYP4_UI_REVERT_OUTPUT", output)
				a := New(&p4.Client{Workspace: "workspace", Root: root}, 0, "", false)
				files := []string{"//workspace/moved.txt", "//workspace/new space%23%40%2523.txt"}
				msg := unwrapActivityResult(a.cmdRevertAndDeleteLocal(files, []string{moved, selected}, clID)())
				if _, failed := msg.(opDoneMsg); failed != test.malformed {
					t.Fatalf("unexpected discard result: %#v", msg)
				}
				if done, ok := msg.(revertDoneMsg); ok {
					wantCount := 0
					if test.wantReported {
						wantCount = 1
					}
					if len(done.files) != wantCount || len(done.files) > 0 && done.files[0] == moved {
						t.Fatalf("discard reported skipped files as reverted: %#v; want %d confirmed files", done.files, wantCount)
					}
					a.Update(done)
					if a.status != fmt.Sprintf("Reverted %d file(s)", wantCount) {
						t.Fatalf("discard completion count = %q", a.status)
					}
				}
				if _, err := os.Stat(selected); os.IsNotExist(err) != test.wantDelete {
					t.Fatalf("confirmed add deletion = %v, want %v; error %v", os.IsNotExist(err), test.wantDelete, err)
				}
				for _, file := range []string{moved, uncaptured, encoded} {
					if _, err := os.Stat(file); err != nil {
						t.Fatalf("discard deleted an unconfirmed local file %q: %v", file, err)
					}
				}
				commands, err := os.ReadFile(log)
				if err != nil {
					t.Fatal(err)
				}
				scope := "revert "
				if clID != "" {
					scope += "-c " + clID + " "
				}
				if !strings.Contains(string(commands), scope+strings.Join(files, " ")) || strings.Contains(string(commands), "//...") {
					t.Fatalf("discard changed its captured scope: %s", commands)
				}
			})
		}
	}
}
