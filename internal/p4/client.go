package p4

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// Client wraps p4 CLI invocations.
type Client struct {
	Port   string
	User   string
	Workspace string
}

// globalFlags returns the p4 global flags (-p, -u, -c).
func (c *Client) globalFlags() []string {
	var base []string
	if c.Port != "" {
		base = append(base, "-p", c.Port)
	}
	if c.User != "" {
		base = append(base, "-u", c.User)
	}
	if c.Workspace != "" {
		base = append(base, "-c", c.Workspace)
	}
	return base
}

// run executes a p4 command and returns stdout + stderr combined on error.
func (c *Client) run(args ...string) (string, error) {
	cmd := exec.Command("p4", append(c.globalFlags(), args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runZtag executes a p4 command with -ztag as a global flag.
func (c *Client) runZtag(args ...string) (string, error) {
	flags := append([]string{"-ztag"}, c.globalFlags()...)
	cmd := exec.Command("p4", append(flags, args...)...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runWithStdin executes a p4 command with data piped to stdin.
func (c *Client) runWithStdin(stdin string, args ...string) (string, error) {
	cmd := exec.Command("p4", append(c.globalFlags(), args...)...)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// parseZtag parses -ztag output into a slice of field maps.
// Records are separated by blank lines.
func parseZtag(output string) []map[string]string {
	var records []map[string]string
	current := map[string]string{}
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			if len(current) > 0 {
				records = append(records, current)
				current = map[string]string{}
			}
			continue
		}
		if strings.HasPrefix(line, "... ") {
			rest := line[4:]
			idx := strings.Index(rest, " ")
			if idx == -1 {
				current[rest] = ""
			} else {
				current[rest[:idx]] = rest[idx+1:]
			}
		}
	}
	if len(current) > 0 {
		records = append(records, current)
	}
	return records
}

// OpenedFiles returns all files currently open in any changelist.
func (c *Client) OpenedFiles() ([]OpenedFile, error) {
	out, err := c.runZtag("opened")
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	files := make([]OpenedFile, 0, len(records))
	for _, r := range records {
		rev, _ := strconv.Atoi(r["rev"])
		files = append(files, OpenedFile{
			DepotFile:  r["depotFile"],
			ClientFile: r["clientFile"],
			Action:     Action(r["action"]),
			Type:       r["type"],
			Change:     r["change"],
			Revision:   rev,
		})
	}
	return files, nil
}

// GroupByChangelist organizes opened files into Changelist structs.
func GroupByChangelist(files []OpenedFile) []Changelist {
	order := []string{}
	byID := map[string]*Changelist{}
	for _, f := range files {
		id := f.Change
		if id == "" {
			id = "default"
		}
		if _, ok := byID[id]; !ok {
			order = append(order, id)
			byID[id] = &Changelist{ID: id}
		}
		byID[id].Files = append(byID[id].Files, f)
	}
	result := make([]Changelist, 0, len(order))
	for _, id := range order {
		result = append(result, *byID[id])
	}
	return result
}

// Diff returns the unified diff output for a single file.
// Returns a human-readable message for binary files instead of raw bytes.
func (c *Client) Diff(clientFile string) (string, error) {
	out, err := c.run("diff", "-du", clientFile)
	if err != nil {
		// p4 diff exits non-zero for binary files - surface a clean message.
		if strings.Contains(err.Error(), "binary") || strings.Contains(out, "(binary)") {
			return "(binary file - diff not available)", nil
		}
		return "", err
	}
	if strings.Contains(out, "(binary)") {
		return "(binary file - diff not available)", nil
	}
	return out, nil
}

// Sync runs p4 sync.
func (c *Client) Sync() (string, error) {
	return c.run("sync")
}

// SyncStreaming runs p4 sync and sends each output line to lines as it arrives.
func (c *Client) SyncStreaming(ctx context.Context, lines chan<- string) error {
	return c.streamCommand(ctx, lines, append(c.globalFlags(), "sync")...)
}

// Shelve shelves all files in the given changelist.
func (c *Client) Shelve(clID string) (string, error) {
	return c.run("shelve", "-c", clID)
}

// Submit submits the given changelist.
func (c *Client) Submit(clID, description string) (string, error) {
	if description != "" {
		return c.run("submit", "-d", description, "-c", clID)
	}
	return c.run("submit", "-c", clID)
}

// SubmitStreaming submits a changelist and streams output lines into lines.
func (c *Client) SubmitStreaming(ctx context.Context, clID, description string, lines chan<- string) error {
	args := c.globalFlags()
	if description != "" {
		args = append(args, "submit", "-d", description, "-c", clID)
	} else {
		args = append(args, "submit", "-c", clID)
	}
	return c.streamCommand(ctx, lines, args...)
}

// SubmitMarkedStreaming moves files into a new CL and submits it, streaming output.
func (c *Client) SubmitMarkedStreaming(ctx context.Context, files []OpenedFile, description string, lines chan<- string) error {
	clID, err := c.CreateChange(description)
	if err != nil {
		return fmt.Errorf("create change: %w", err)
	}
	for _, f := range files {
		if _, err := c.Reopen(clID, f.ClientFile); err != nil {
			return fmt.Errorf("reopen %s: %w", f.ClientFile, err)
		}
	}
	return c.SubmitStreaming(ctx, clID, description, lines)
}

// streamCommand runs a p4 command and sends each stdout line to lines.
func (c *Client) streamCommand(ctx context.Context, lines chan<- string, args ...string) error {
	cmd := exec.CommandContext(ctx, "p4", args...)
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			select {
			case lines <- line:
			case <-ctx.Done():
				cmd.Process.Kill()
				cmd.Wait()
				return ctx.Err()
			}
		}
	}
	return cmd.Wait()
}

// RevertFiles discards local changes for the given files in a single p4 revert call.
func (c *Client) RevertFiles(clientFiles []string) (string, error) {
	args := append([]string{"revert"}, clientFiles...)
	return c.run(args...)
}

// Reopen moves a file to a different changelist.
func (c *Client) Reopen(clID, clientFile string) (string, error) {
	return c.run("reopen", "-c", clID, clientFile)
}

// CreateChange creates a new pending changelist and returns its ID.
func (c *Client) CreateChange(description string) (string, error) {
	spec := "Change:\tnew\nDescription:\t" + description + "\n"
	out, err := c.runWithStdin(spec, "change", "-i")
	if err != nil {
		return "", err
	}
	// output: "Change N created."
	parts := strings.Fields(strings.TrimSpace(out))
	if len(parts) >= 2 {
		return parts[1], nil
	}
	return "", fmt.Errorf("unexpected change output: %s", out)
}

// SubmitMarked moves the given files into a new CL and submits it.
// Files not in the list remain in their original changelists.
func (c *Client) SubmitMarked(files []OpenedFile, description string) error {
	clID, err := c.CreateChange(description)
	if err != nil {
		return fmt.Errorf("create change: %w", err)
	}
	for _, f := range files {
		if _, err := c.Reopen(clID, f.ClientFile); err != nil {
			return fmt.Errorf("reopen %s: %w", f.ClientFile, err)
		}
	}
	_, err = c.Submit(clID, description)
	return err
}

// Filelog returns revision history for a depot file.
// Pass max > 0 to limit results (p4 filelog -m max).
func (c *Client) Filelog(depotFile string, max int) ([]FilelogEntry, error) {
	args := []string{"filelog"}
	if max > 0 {
		args = append(args, "-m", strconv.Itoa(max))
	}
	args = append(args, depotFile)
	out, err := c.runZtag(args...)
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	entries := make([]FilelogEntry, 0, len(records))
	for _, r := range records {
		rev, _ := strconv.Atoi(r["rev0"])
		entries = append(entries, FilelogEntry{
			DepotFile:   r["depotFile"],
			Rev:         rev,
			Change:      r["change0"],
			Action:      Action(r["action0"]),
			Date:        r["time0"],
			Author:      r["user0"],
			Description: strings.TrimSpace(r["desc0"]),
		})
	}
	return entries, nil
}

// ResolveList returns files that need resolving.
func (c *Client) ResolveList() ([]ConflictFile, error) {
	out, err := c.run("resolve", "-n")
	if err != nil {
		// p4 resolve -n exits non-zero when nothing to resolve on some servers
		if strings.Contains(err.Error(), "No file(s) to resolve") {
			return nil, nil
		}
		return nil, err
	}
	var conflicts []ConflictFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Format: //depot/file - merging //depot/file#1,2
		parts := strings.SplitN(line, " - merging ", 2)
		if len(parts) == 2 {
			conflicts = append(conflicts, ConflictFile{
				ClientFile: parts[0],
				FromFile:   parts[1],
			})
		}
	}
	return conflicts, nil
}

// Info returns basic workspace/server information from p4 info.
func (c *Client) Info() (WorkspaceInfo, error) {
	out, err := c.runZtag("info")
	if err != nil {
		return WorkspaceInfo{}, err
	}
	records := parseZtag(out)
	if len(records) == 0 {
		return WorkspaceInfo{}, nil
	}
	r := records[0]
	return WorkspaceInfo{
		Client:     r["clientName"],
		Stream:     r["clientStream"],
		ServerAddr: r["serverAddress"],
	}, nil
}

// SyncDryRun runs p4 sync -n and returns the number of files that would be updated.
func (c *Client) SyncDryRun() (int, error) {
	out, err := c.run("sync", "-n")
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "up-to-date") || strings.Contains(msg, "no such file") {
			return 0, nil
		}
		return 0, err
	}
	count := 0
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line != "" {
			count++
		}
	}
	return count, nil
}

// IsAuthError returns true when output indicates an expired/invalid ticket.
func IsAuthError(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	return strings.Contains(msg, "Perforce password (P4PASSWD) invalid or unset") ||
		strings.Contains(msg, "Your session has expired") ||
		strings.Contains(msg, "ticket expired")
}
