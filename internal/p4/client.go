package p4

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// IsConnectionError returns true if err indicates the p4 server is unreachable.
func IsConnectionError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Connect to server failed") ||
		strings.Contains(s, "TCP connect to") ||
		strings.Contains(s, "check $P4PORT")
}

// Client wraps p4 CLI invocations.
type Client struct {
	Port      string
	User      string
	Workspace string
	Root      string   // local root of the current workspace; used as CWD for commands that require it
	Stream    string   // current stream path e.g. //depot/main
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
			DepotFile:    r["depotFile"],
			ClientFile:   r["clientFile"],
			Action:       Action(r["action"]),
			Type:         r["type"],
			Change:       r["change"],
			Revision:     rev,
			NeedsResolve: false, // set by caller via ResolveList cross-reference
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
	if cl, ok := byID["default"]; ok {
		result = append(result, *cl)
	}
	for _, id := range order {
		if id != "default" {
			result = append(result, *byID[id])
		}
	}
	return result
}

// Diff returns the unified diff output for a single file.
// Returns a human-readable message for binary files instead of raw bytes.
func (c *Client) Diff(clientFile string) (string, error) {
	out, err := c.run("diff", "-du", "-f", clientFile)
	if err != nil {
		// p4 diff exits non-zero for binary files - surface a clean message.
		if strings.Contains(err.Error(), "binary") || strings.Contains(out, "(binary)") {
			return "(binary file — diff not available)", nil
		}
		return "", err
	}
	if strings.Contains(out, "(binary)") {
		return "(binary file — diff not available)", nil
	}
	if strings.TrimSpace(out) == "" {
		return "(files are identical)", nil
	}
	return out, nil
}

// SyncToCL syncs the workspace to a specific changelist.
// For stream depots pass the stream path (e.g. "//depot/main"); for classic depots pass "".
func (c *Client) SyncToCL(streamPath, cl string) error {
	var target string
	if streamPath != "" {
		target = streamPath + "/...@" + cl
	} else {
		target = "@" + cl // syncs entire workspace client view to CL
	}
	_, err := c.run("sync", target)
	return err
}

// SyncStreaming runs p4 sync and sends each output line to lines as it arrives.
func (c *Client) SyncStreaming(ctx context.Context, lines chan<- string) error {
	return c.streamCommand(ctx, lines, append(c.globalFlags(), "sync")...)
}

// Shelve shelves all files in a changelist.
// For the default changelist pass "" or "default"; p4 does not accept -c default,
// so we use bare `p4 shelve` (no flags) which shelves the entire default CL.
func (c *Client) Shelve(clID string) (string, error) {
	if clID == "" || clID == "default" {
		return c.run("shelve")
	}
	return c.run("shelve", "-c", clID)
}


// ShelveFiles shelves specific files from a changelist.
func (c *Client) ShelveFiles(clID string, clientFiles []string) (string, error) {
	args := append([]string{"shelve", "-c", clID}, clientFiles...)
	return c.run(args...)
}

// Submit submits the given changelist.
func (c *Client) Submit(clID, description string) (string, error) {
	return c.run("submit", "-c", clID)
}

// SubmitStreaming submits a changelist and streams output lines into lines.
func (c *Client) SubmitStreaming(ctx context.Context, clID, description string, lines chan<- string) error {
	if description != "" {
		if err := c.UpdateChangeDescription(clID, description); err != nil {
			return fmt.Errorf("update description: %w", err)
		}
	}
	args := append(c.globalFlags(), "submit", "-c", clID)
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
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
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
	if err := cmd.Wait(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// RevertFiles discards local changes for the given files in a single p4 revert call.
func (c *Client) RevertFiles(clientFiles []string) (string, error) {
	args := append([]string{"revert"}, clientFiles...)
	return c.run(args...)
}

// PendingDescriptions returns a map of CL ID → description for all pending CLs.
func (c *Client) PendingDescriptions() (map[string]string, error) {
	args := []string{"changes", "-s", "pending", "-l"}
	if c.Workspace != "" {
		args = append(args, "-c", c.Workspace)
	}
	out, err := c.runZtag(args...)
	if err != nil {
		return nil, err
	}
	m := map[string]string{}
	for _, r := range parseZtag(out) {
		if id := r["change"]; id != "" {
			m[id] = strings.TrimSpace(r["desc"])
		}
	}
	return m, nil
}

// FindCLByDescription searches pending CLs for one whose description contains the given text.
// Returns the CL ID, or "" if not found.
func (c *Client) FindCLByDescription(desc string) (string, error) {
	args := []string{"changes", "-s", "pending", "-l"}
	if c.Workspace != "" {
		args = append(args, "-c", c.Workspace)
	}
	out, err := c.runZtag(args...)
	if err != nil {
		return "", err
	}
	descLower := strings.ToLower(desc)
	for _, r := range parseZtag(out) {
		clDesc := strings.ToLower(strings.TrimSpace(r["desc"]))
		if strings.Contains(clDesc, descLower) {
			return r["change"], nil
		}
	}
	return "", nil
}

// ReopenFiles moves the given files to a different changelist.
func (c *Client) ReopenFiles(clID string, clientFiles []string) (string, error) {
	args := append([]string{"reopen", "-c", clID}, clientFiles...)
	return c.run(args...)
}

// RevertCL reverts all files in the given changelist.
func (c *Client) RevertCL(clID string) (string, error) {
	return c.run("revert", "-c", clID, "//...")
}

// Reopen moves a file to a different changelist.
func (c *Client) Reopen(clID, clientFile string) (string, error) {
	return c.run("reopen", "-c", clID, clientFile)
}

// DeleteChange deletes an empty pending changelist.
func (c *Client) DeleteChange(clID string) error {
	_, err := c.run("change", "-d", clID)
	return err
}

// CreateChange creates a new pending changelist and returns its ID.
// UpdateChangeDescription updates the description of an existing numbered CL.
func (c *Client) UpdateChangeDescription(clID, description string) error {
	out, err := c.run("change", "-o", clID)
	if err != nil {
		return err
	}
	// Replace description field — everything after "Description:\t" until the next field
	lines := strings.Split(out, "\n")
	var result []string
	skip := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Description:") {
			result = append(result, "Description:\t"+description)
			skip = true
			continue
		}
		if skip && (line == "" || line[0] == '\t' || line[0] == ' ') {
			continue // skip old description continuation lines
		}
		skip = false
		result = append(result, line)
	}
	spec := strings.Join(result, "\n")
	_, err = c.runWithStdin(spec, "change", "-i")
	return err
}

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

// CurrentCL returns the highest CL currently synced in the workspace (have revision).
func (c *Client) CurrentCL() string {
	out, err := c.run("changes", "-m1", "-s", "submitted", "//...@"+c.Workspace)
	if err != nil || strings.TrimSpace(out) == "" {
		return ""
	}
	// "Change N on date by user@client 'desc'"
	parts := strings.Fields(out)
	if len(parts) >= 2 {
		return parts[1]
	}
	return ""
}

// Changes returns submitted changelists that affected the given depot path.
// Use for directories/stream wildcards (path ending in /...) to avoid duplicate entries per file.
func (c *Client) Changes(path string, max int) ([]FilelogEntry, error) {
	args := []string{"changes", "-l", "-t", "-s", "submitted"}
	if max > 0 {
		args = append(args, "-m", strconv.Itoa(max))
	}
	args = append(args, path)
	out, err := c.run(args...)
	if err != nil {
		return nil, err
	}
	// Output format:
	// Change N on YYYY/MM/DD by user@client 'desc ...'
	// <blank line>
	// \tdescription continuation
	var entries []FilelogEntry
	var current *FilelogEntry
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Change ") {
			if current != nil {
				current.Description = strings.TrimSpace(current.Description)
				entries = append(entries, *current)
			}
			// "Change N on YYYY/MM/DD by user@client 'desc'"
			parts := strings.Fields(line)
			if len(parts) < 5 {
				continue
			}
			cl := parts[1]
			// With -t: "Change N on YYYY/MM/DD HH:MM:SS by user@client"
			// Without -t: "Change N on YYYY/MM/DD by user@client"
			date := parts[3]
			userIdx := 5
			if len(parts) > 6 && strings.Contains(parts[4], ":") {
				// has time component
				if ts, err2 := time.Parse("2006/01/02 15:04:05", date+" "+parts[4]); err2 == nil {
					date = ts.Format("02 Jan 2006 15:04")
				}
				userIdx = 6
			} else if ts, err2 := time.Parse("2006/01/02", date); err2 == nil {
				date = ts.Format("02 Jan 2006")
			}
			// user@client
			userClient := parts[userIdx]
			user, client := userClient, ""
			if idx := strings.Index(userClient, "@"); idx >= 0 {
				user = userClient[:idx]
				client = userClient[idx+1:]
			}
			current = &FilelogEntry{
				Change: cl,
				Date:   date,
				Author: user,
				Client: client,
			}
			continue
		}
		if current != nil && (strings.HasPrefix(line, "\t") || line == "") {
			current.Description += strings.TrimPrefix(line, "\t") + "\n"
		}
	}
	if current != nil {
		current.Description = strings.TrimSpace(current.Description)
		entries = append(entries, *current)
	}
	return entries, nil
}

// Filelog returns revision history for a depot file.
// Pass max > 0 to limit results (p4 filelog -m max).
func (c *Client) Filelog(depotFile string, max int) ([]FilelogEntry, error) {
	args := []string{"filelog", "-l"}
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
		if r["change0"] == "" {
			continue // record has no visible revisions (e.g. limit exhausted)
		}
		rev, _ := strconv.Atoi(r["rev0"])
		date := r["time0"]
		if ts, err := strconv.ParseInt(date, 10, 64); err == nil {
			date = time.Unix(ts, 0).Format("02 Jan 2006 15:04")
		}
		entries = append(entries, FilelogEntry{
			DepotFile:   r["depotFile"],
			Rev:         rev,
			Change:      r["change0"],
			Action:      Action(r["action0"]),
			Date:        date,
			Author:      r["user0"],
			Client:      r["client0"],
			Description: strings.TrimSpace(r["desc0"]),
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		ci, _ := strconv.Atoi(entries[i].Change)
		cj, _ := strconv.Atoi(entries[j].Change)
		return ci > cj
	})
	return entries, nil
}

// ResolveList returns files that need resolving.
// AutoResolve tries each flag in order until one succeeds.
// If clientFile is non-empty it resolves that file only; otherwise all pending resolves.
func (c *Client) AutoResolve(clientFile string, flags []string) error {
	var lastErr error
	for _, flag := range flags {
		args := []string{"resolve", flag}
		if clientFile != "" {
			args = append(args, clientFile)
		}
		if _, err := c.run(args...); err != nil {
			lastErr = err
			continue
		}
		return nil
	}
	return lastErr
}

// ResolveList returns files needing resolve. If path is non-empty, only that
// file or folder (use "//depot/path/...") is checked.
func (c *Client) ResolveList(path string) ([]ConflictFile, error) {
	args := []string{"resolve", "-n"}
	if path != "" {
		args = append(args, path)
	}
	out, err := c.run(args...)
	if err != nil {
		// p4 resolve -n exits non-zero when nothing to resolve on some servers
		if strings.Contains(err.Error(), "No file(s) to resolve") {
			return nil, nil
		}
		return nil, err
	}
	// p4 resolve -n formats:
	//   //depot/file - merging //depot/other#1,2
	//   //depot/file - branch resolve from //depot/other#1,2
	//   //depot/file - delete from //depot/other#1
	//   //depot/file - vs //depot/other#1
	//   //depot/file - action resolve from //depot/other#1
	separators := []string{" - merging ", " - branch resolve from ", " - resolving branch from ", " - delete from ", " - vs ", " - action resolve from "}
	var conflicts []ConflictFile
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		matched := false
		for _, sep := range separators {
			if parts := strings.SplitN(line, sep, 2); len(parts) == 2 {
				conflicts = append(conflicts, ConflictFile{
					ClientFile: parts[0],
					FromFile:   parts[1],
				})
				matched = true
				break
			}
		}
		// Unrecognised line — surface it raw so the user sees it.
		if !matched {
			conflicts = append(conflicts, ConflictFile{ClientFile: line})
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
		Root:       r["clientRoot"],
	}, nil
}

// DepotFromStream extracts the depot wildcard path from a stream path, e.g. "//depot/main" -> "//depot/...".
func DepotFromStream(streamPath string) string {
	trimmed := strings.TrimPrefix(streamPath, "//")
	if idx := strings.Index(trimmed, "/"); idx >= 0 {
		return "//" + trimmed[:idx] + "/..."
	}
	return streamPath
}

// Streams returns all streams under the depot inferred from the current workspace stream.
// depotPath should be e.g. "//testdepot/..." (use "*" for depot wildcard).
func (c *Client) Streams(depotPath string) ([]StreamInfo, error) {
	out, err := c.runZtag("streams", depotPath)
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	result := make([]StreamInfo, 0, len(records))
	for _, r := range records {
		path := r["Stream"]
		if path == "" {
			continue
		}
		result = append(result, StreamInfo{
			Path:   path,
			Parent: r["Parent"],
			Type:   r["Type"],
			Name:   r["Name"],
		})
	}
	return result, nil
}

// ShelvedCLs returns changelists with shelved files for the current workspace.
func (c *Client) ShelvedCLs() ([]ShelvedCL, error) {
	args := []string{"changes", "-s", "shelved"}
	if c.Workspace != "" {
		args = append(args, "-c", c.Workspace)
	}
	out, err := c.runZtag(args...)
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	var cls []ShelvedCL
	for _, r := range records {
		clID := r["change"]
		if clID == "" {
			continue
		}
		files, _ := c.shelvedFiles(clID)
		cls = append(cls, ShelvedCL{
			ID:          clID,
			Description: strings.TrimSpace(r["desc"]),
			User:        r["user"],
			Files:       files,
		})
	}
	return cls, nil
}

func (c *Client) shelvedFiles(clID string) ([]ShelvedFile, error) {
	out, err := c.runZtag("describe", "-s", "-S", clID)
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	if len(records) == 0 {
		return nil, nil
	}
	r := records[0]
	var files []ShelvedFile
	for i := 0; ; i++ {
		path := r[fmt.Sprintf("depotFile%d", i)]
		if path == "" {
			break
		}
		files = append(files, ShelvedFile{
			DepotFile: path,
			Action:    Action(r[fmt.Sprintf("action%d", i)]),
		})
	}
	return files, nil
}

// UnshelveAndDelete unshelves files from a CL back into the workspace and deletes the shelf.
// First tries a plain unshelve; if no files are unshelved (cross-stream case) and c.Stream is
// set, retries with -S <stream> to remap files through the stream graph.
// If files were unshelved but p4 also reports an error (e.g. needs resolve), the shelf is
// preserved and the error is returned so the user can resolve before re-trying.
func (c *Client) UnshelveAndDelete(clID string) error {
	tryUnshelve := func(extraArgs ...string) (out string, runErr error) {
		args := append(c.globalFlags(), "unshelve", "-s", clID)
		args = append(args, extraArgs...)
		cmd := exec.Command("p4", args...)
		var stdout, stderr bytes.Buffer
		cmd.Stdout = &stdout
		cmd.Stderr = &stderr
		runErr = cmd.Run()
		out = stdout.String() + stderr.String()
		return
	}

	out, runErr := tryUnshelve()
	unshelved := strings.Contains(out, " - unshelved")

	// If nothing was unshelved and we have a stream, retry with -S for cross-stream remapping.
	if !unshelved && c.Stream != "" {
		out2, runErr2 := tryUnshelve("-S", c.Stream)
		if strings.Contains(out2, " - unshelved") {
			out, runErr = out2, runErr2
			unshelved = true
		}
	}

	unshelved = strings.Contains(out, " - unshelved")
	needsResolve := strings.Contains(out, "needs resolve")

	if runErr != nil && !unshelved {
		return fmt.Errorf("unshelve: %s: %s", runErr, strings.TrimSpace(out))
	}
	if !unshelved {
		return fmt.Errorf("no files unshelved — shelf was preserved")
	}
	if needsResolve {
		// Files are open but require resolve before the shelf can be deleted.
		return fmt.Errorf("files unshelved with conflicts — shelf kept, delete manually after resolving")
	}
	if _, err := c.run("shelve", "-d", "-c", clID); err != nil {
		if strings.Contains(err.Error(), "needs resolve") || strings.Contains(err.Error(), "Shelve aborted") {
			return fmt.Errorf("files unshelved with conflicts — shelf kept, delete manually after resolving")
		}
		return fmt.Errorf("delete shelf: %w", err)
	}
	return nil
}

// DeleteShelf deletes the shelved files from a CL without unshelving.
func (c *Client) DeleteShelf(clID string) error {
	_, err := c.run("shelve", "-d", "-c", clID)
	return err
}

// ForceSyncPath runs p4 sync -f on the given depot path (file or wildcard).
func (c *Client) ForceSyncPath(depotPath string) (string, error) {
	return c.run("sync", "-f", depotPath)
}

// WhereLocal converts a depot path to a local filesystem path using p4 where.
func (c *Client) WhereLocal(depotPath string) (string, error) {
	out, err := c.run("where", depotPath)
	if err != nil {
		return "", err
	}
	// Output: //depot/path //client/path /local/path
	fields := strings.Fields(strings.TrimSpace(out))
	if len(fields) < 3 {
		return "", fmt.Errorf("unexpected where output: %s", out)
	}
	return fields[2], nil
}

// Reconcile runs p4 reconcile on the given path to detect offline changes
// (edit, add, delete). If reconcile finds nothing to open, falls back to
// p4 edit so the user can check out an unchanged depot file for editing.
func (c *Client) Reconcile(path string) (string, error) {
	out, err := c.run("reconcile", path)
	if err != nil {
		return out, err
	}
	if strings.TrimSpace(out) == "" {
		return c.run("edit", path)
	}
	return out, nil
}

// BrowserDirs lists immediate subdirectories at the given wildcard path (e.g. "//depot/stream/*").
func (c *Client) BrowserDirs(wildcard string) ([]string, error) {
	out, err := c.run("dirs", wildcard)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such file") || strings.Contains(msg, "no files") {
			return nil, nil
		}
		return nil, err
	}
	var dirs []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line != "" {
			dirs = append(dirs, line)
		}
	}
	return dirs, nil
}

// BrowserHaveFiles lists files synced in the workspace at the given wildcard (non-recursive).
func (c *Client) BrowserHaveFiles(wildcard string) ([]string, error) {
	out, err := c.run("have", wildcard)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such file") || strings.Contains(msg, "not on client") {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if idx := strings.Index(line, "#"); idx > 0 {
			files = append(files, line[:idx])
		}
	}
	return files, nil
}

// BrowserDepotFiles lists all non-deleted depot files at the given wildcard (non-recursive).
func (c *Client) BrowserDepotFiles(wildcard string) ([]string, error) {
	out, err := c.run("files", "-e", wildcard)
	if err != nil {
		if strings.Contains(err.Error(), "no such file") {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line = strings.TrimSpace(line); line == "" {
			continue
		}
		if idx := strings.Index(line, "#"); idx > 0 {
			files = append(files, line[:idx])
		}
	}
	return files, nil
}

// WorkspaceEntry represents a file visible in the local workspace (tracked or untracked).
type WorkspaceEntry struct {
	DepotPath string // depot path (computed for untracked files)
	LocalPath string // absolute local path
	Tracked   bool   // true if synced via p4 have
}

// BrowserWorkspaceFast returns directories and files visible in the local workspace
// from a filesystem scan only — no p4 server calls. All files are returned with
// Tracked=false; call BrowserWorkspaceStatus to overlay p4 data.
func (c *Client) BrowserWorkspaceFast(depotPath string) (dirs []string, files []WorkspaceEntry, err error) {
	if c.Root == "" || c.Stream == "" {
		return nil, nil, nil
	}
	rel := strings.TrimPrefix(depotPath, c.Stream)
	localDir := c.Root + rel
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return nil, nil, err
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".") {
			continue
		}
		computedDepot := c.Stream + rel + "/" + e.Name()
		if e.IsDir() {
			dirs = append(dirs, computedDepot)
		} else {
			files = append(files, WorkspaceEntry{
				DepotPath: computedDepot,
				LocalPath: localDir + "/" + e.Name(),
				Tracked:   false,
			})
		}
	}
	return dirs, files, nil
}

// BrowserWorkspaceStatus fetches p4-side data for a workspace directory:
// tracked file paths, depot subdirectory paths, and any tracked files missing
// from the local filesystem (e.g. deleted on disk).
func (c *Client) BrowserWorkspaceStatus(depotPath string) (haveFiles, depotDirs []string, missingFiles []WorkspaceEntry, err error) {
	if c.Root == "" || c.Stream == "" {
		return nil, nil, nil, nil
	}
	rel := strings.TrimPrefix(depotPath, c.Stream)
	localDir := c.Root + rel
	wildcard := depotPath + "/*"
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); haveFiles, err = c.BrowserHaveFiles(wildcard) }()
	go func() { defer wg.Done(); depotDirs, _ = c.BrowserDirs(wildcard) }()
	wg.Wait()

	// Find tracked files missing from local filesystem.
	entries, fsErr := os.ReadDir(localDir)
	localNames := map[string]bool{}
	if fsErr == nil {
		for _, e := range entries {
			localNames[e.Name()] = true
		}
	}
	for _, f := range haveFiles {
		name := f[strings.LastIndex(f, "/")+1:]
		if !localNames[name] {
			missingFiles = append(missingFiles, WorkspaceEntry{
				DepotPath: f,
				LocalPath: localDir + "/" + name,
				Tracked:   true,
			})
		}
	}
	return haveFiles, depotDirs, missingFiles, err
}

// AddFile opens a local file for add in the default changelist.
// OpenedByOthers returns the set of depot paths opened by users other than the current user
// at the given wildcard (e.g. "//depot/stream/*"). Uses p4 opened -a which is reliable
// regardless of client view mapping.
func (c *Client) OpenedByOthers(wildcard string) (map[string]bool, error) {
	out, err := c.run("opened", "-a", wildcard)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such file") || strings.Contains(msg, "no files") ||
			strings.Contains(msg, "not opened") || strings.Contains(msg, "not open") {
			return nil, nil
		}
		return nil, err
	}
	result := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// //depot/stream/file.txt#1 - edit default change (text) by user@workspace
		depotPath := line
		if idx := strings.Index(line, "#"); idx > 0 {
			depotPath = line[:idx]
		}
		if c.User == "" || !strings.Contains(line, " by "+c.User+"@") {
			result[depotPath] = true
		}
	}
	return result, nil
}

// HasChanges returns true if the file opened for edit differs from the have revision.
// Do NOT use -f here — that compares against depot HEAD, not the synced revision.
func (c *Client) HasChanges(clientFile string) (bool, error) {
	out, err := c.run("diff", "-du", clientFile)
	if err != nil {
		return false, err
	}
	return strings.Contains(out, "@@"), nil
}

// FilesDiffStatus returns the set of depot paths for open-for-edit files that
// differ from the have revision. p4 diff exits non-zero when any file differs,
// so stdout is captured regardless of exit code.
func (c *Client) FilesDiffStatus() map[string]bool {
	args := append(c.globalFlags(), "diff", "-du")
	cmd := exec.Command("p4", args...)
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Run() // exit code ignored — non-zero is normal when any file has changes

	changed := map[string]bool{}
	var currentDepot string
	for _, line := range strings.Split(out.String(), "\n") {
		if strings.HasPrefix(line, "--- //") {
			// format: --- //depot/path\tdate
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				currentDepot = parts[1] // //depot/path, no #rev suffix
			}
		} else if strings.HasPrefix(line, "@@") && currentDepot != "" {
			changed[currentDepot] = true
		}
	}
	return changed
}

// DeletePath marks a file or path (e.g. "//depot/stream/dir/...") for delete.
func (c *Client) DeletePath(path string) error {
	_, err := c.run("delete", path)
	return err
}

// MergeStream promotes changes from the current (child) stream up to its parent.
// Run from a child workspace: p4 copy (no -S) copies current stream → parent.
func (c *Client) MergeStream(_ string) (string, error) {
	return c.run("copy")
}

// CopyStream brings changes from the parent stream down into targetStream (child).
// p4 merge -S child merges from child's parent into the child stream.
func (c *Client) CopyStream(targetStream string) (string, error) {
	return c.run("merge", "-S", targetStream)
}

// IntegrateClassic integrates files from source to target using classic (non-stream) depot paths.
func (c *Client) IntegrateClassic(source, target string) (string, error) {
	return c.run("integrate", source, target)
}

// SyncDryRun returns the number of changelists the workspace is behind head.
func (c *Client) SyncDryRun() (int, error) {
	// Get the have CL as a number first to avoid per-file revision ambiguity.
	haveCL := c.CurrentCL()
	if haveCL == "" {
		return 0, nil
	}
	out, err := c.run("changes", "-s", "submitted", fmt.Sprintf("//...@%s,#head", haveCL))
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
	// subtract 1 to exclude the have CL itself
	if count > 0 {
		count--
	}
	return count, nil
}

// SwitchToStream switches the current workspace to the given stream using
// `p4 switch`.  When no workspace for that stream exists the server creates
// one based on the current client spec (requires p4 2021.1+).
// p4 switch validates that CWD is under the client root, so we must run it
// from there; if the root doesn't exist yet we create it first.
func (c *Client) SwitchToStream(streamPath string) error {
	dir := c.Root
	if dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			dir = "" // fall back to inherited CWD
		}
	}
	cmd := exec.Command("p4", append(c.globalFlags(), "switch", streamPath)...)
	if dir != "" {
		cmd.Dir = dir
	}
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return nil
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
