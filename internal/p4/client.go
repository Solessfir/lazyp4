package p4

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
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
	Port          string
	User          string
	Workspace     string
	Root          string // local root of the current workspace; used as CWD for commands that require it
	Stream        string // current stream path e.g. //depot/main
	StorePassword bool   // opt in to saving credentials in the OS keychain
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
	return c.runContext(context.Background(), args...)
}

func (c *Client) runContext(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "p4", append(c.globalFlags(), args...)...)
	configureP4Command(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stderr.String()))
	}
	return stdout.String(), nil
}

// runZtag executes a p4 command with -ztag as a global flag.
func (c *Client) runZtag(args ...string) (string, error) {
	return c.runZtagContext(context.Background(), args...)
}

func (c *Client) runZtagContext(ctx context.Context, args ...string) (string, error) {
	flags := append([]string{"-ztag", "-Mj"}, c.globalFlags()...)
	cmd := exec.CommandContext(ctx, "p4", append(flags, args...)...)
	configureP4Command(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stdout.String()+stderr.String()))
	}
	return validateStructuredOutput(stdout.String())
}

func validateStructuredOutput(output string) (string, error) {
	decoder := json.NewDecoder(strings.NewReader(output))
	for {
		var record map[string]json.RawMessage
		if err := decoder.Decode(&record); err != nil {
			if err == io.EOF {
				return output, nil
			}
			return "", fmt.Errorf("invalid structured p4 output: %w", err)
		}
		if record == nil {
			return "", fmt.Errorf("invalid structured p4 output: expected an object")
		}
	}
}

// runWithStdin executes a p4 command with data piped to stdin.
func (c *Client) runWithStdin(stdin string, args ...string) (string, error) {
	return c.runWithStdinContext(context.Background(), stdin, args...)
}

func (c *Client) runWithStdinContext(ctx context.Context, stdin string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "p4", append(c.globalFlags(), args...)...)
	configureP4Command(cmd)
	cmd.Stdin = strings.NewReader(stdin)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	if err != nil {
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		return "", fmt.Errorf("%s: %s", err, strings.TrimSpace(stdout.String()+stderr.String()))
	}
	return stdout.String(), nil
}

func (c *Client) whereFiles(paths []string) (string, error) {
	output, err := c.runWithStdin(strings.Join(fileSpecs(paths), "\n")+"\n", "-ztag", "-Mj", "-x", "-", "where")
	if err != nil {
		return "", err
	}
	return validateStructuredOutput(output)
}

// parseZtag reads JSON records after runZtag has validated the complete output.
func parseZtag(output string) []map[string]string {
	var records []map[string]string
	decoder := json.NewDecoder(strings.NewReader(output))
	decoder.UseNumber()
	for decoder.More() {
		var fields map[string]interface{}
		if err := decoder.Decode(&fields); err != nil {
			break
		}
		if _, message := fields["data"]; message {
			continue
		}
		record := make(map[string]string, len(fields))
		for key, value := range fields {
			record[key] = fmt.Sprint(value)
		}
		records = append(records, record)
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
// For Binary files, keeps the --- / +++ headers and replaces the
// "(... files differ ...)" line with a size summary.
func (c *Client) Diff(clientFile string) (string, error) {
	// Force permits offline edits; an explicit have revision avoids comparing against HEAD.
	out, err := c.run("diff", "-du", "-f", fileSpec(clientFile)+"#have")
	// p4 diff exits non-zero when files differ; for binary files the marker is
	// "(... files differ ...)" — treat this as success, not an error.
	if strings.Contains(out, "files differ") {
		return c.annotateBinaryDiff(out, clientFile), nil
	}
	if err != nil {
		return "", err
	}
	if strings.TrimSpace(out) == "" {
		return "(files are identical)", nil
	}
	return out, nil
}

// annotateBinaryDiff keeps the --- / +++ headers and replaces the
// "(... files differ ...)" marker with a size annotation.
func (c *Client) annotateBinaryDiff(raw, clientFile string) string {
	localSize := ""
	if info, err := os.Stat(clientFile); err == nil {
		localSize = formatBytes(info.Size())
	}

	headSize := ""
	if fout, _ := c.run("fstat", "-Ol", fileSpec(clientFile)); fout != "" {
		for _, line := range strings.Split(fout, "\n") {
			if after, ok := strings.CutPrefix(strings.TrimSpace(line), "... headSize "); ok {
				if n, err := strconv.ParseInt(strings.TrimSpace(after), 10, 64); err == nil {
					headSize = formatBytes(n)
				}
				break
			}
		}
	}

	sizeInfo := "Binary - diff not available"
	switch {
	case headSize != "" && localSize != "":
		sizeInfo = fmt.Sprintf("Binary  %s → %s", headSize, localSize)
	case localSize != "":
		sizeInfo = fmt.Sprintf("Binary  %s", localSize)
	}

	lines := strings.Split(raw, "\n")
	for i, line := range lines {
		if strings.Contains(line, "files differ") {
			lines[i] = sizeInfo
		}
	}
	return strings.Join(lines, "\n")
}

func formatBytes(n int64) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KB", float64(n)/1024)
	case n < 1024*1024*1024:
		return fmt.Sprintf("%.1f MB", float64(n)/(1024*1024))
	default:
		return fmt.Sprintf("%.1f GB", float64(n)/(1024*1024*1024))
	}
}

// SyncToCL syncs the workspace to a specific changelist.
// Uses -f (force) so the workspace exactly matches the CL state: files added
// after the target CL are removed, deleted files are restored, modified files
// are overwritten — mirroring git checkout behaviour.
// For stream depots pass the stream path (e.g. "//depot/main"); for classic depots pass "".
func (c *Client) SyncToCL(streamPath, cl string) error {
	var target string
	if streamPath != "" {
		target = streamPath + "/...@" + cl
	} else {
		target = "@" + cl // syncs entire workspace client view to CL
	}
	_, err := c.run("sync", "-f", target)
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
	number, err := strconv.Atoi(clID)
	if err != nil || number <= 0 {
		return "", fmt.Errorf("selected files require a numbered changelist to shelve")
	}
	if len(clientFiles) == 0 {
		return "", fmt.Errorf("select files to shelve")
	}
	args := append([]string{"shelve", "-c", clID}, fileSpecs(clientFiles)...)
	return c.run(args...)
}

// Submit submits a changelist and streams output lines into lines.
func (c *Client) Submit(ctx context.Context, clID, description string, lines chan<- string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if clID == "" || clID == "default" {
		args := c.globalFlags()
		args = append(args, "submit")
		if description != "" {
			args = append(args, "-d", description)
		}
		return c.streamCommand(ctx, lines, args...)
	}
	if description != "" {
		if err := c.updateChangeDescription(ctx, clID, description); err != nil {
			return fmt.Errorf("update description: %w", err)
		}
	}
	args := append(c.globalFlags(), "submit", "-c", clID)
	return c.streamCommand(ctx, lines, args...)
}

// SubmitMarked moves selected files into a new changelist and submits it.
func (c *Client) SubmitMarked(ctx context.Context, files []OpenedFile, description string, lines chan<- string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(files) == 0 {
		return fmt.Errorf("select files to submit")
	}
	clID, err := c.createChange(ctx, description)
	if err != nil {
		return fmt.Errorf("create change: %w", err)
	}
	for _, f := range files {
		if _, err := c.runContext(ctx, "reopen", "-c", clID, fileSpec(f.ClientFile)); err != nil {
			return fmt.Errorf("reopen %s: %w", f.ClientFile, err)
		}
	}
	return c.Submit(ctx, clID, description, lines)
}

// streamCommand runs a p4 command and sends each stdout line to lines.
func (c *Client) streamCommand(ctx context.Context, lines chan<- string, args ...string) error {
	cmd := exec.CommandContext(ctx, "p4", args...)
	configureP4Command(cmd)
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
	err = cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("%s", msg)
		}
		return err
	}
	return nil
}

// RevertFiles discards local changes for the given files in a single p4 revert call.
func (c *Client) RevertFiles(clientFiles []string) (string, error) {
	args := append([]string{"revert"}, fileSpecs(clientFiles)...)
	return c.run(args...)
}

// RevertUnchanged reverts all open files in the given CL that are identical to the depot version.
// Pass "default" for the default CL. Errors are silently ignored (no files to revert is normal).
func (c *Client) RevertUnchanged(clID string) error {
	return c.RevertUnchangedContext(context.Background(), clID)
}

func (c *Client) RevertUnchangedContext(ctx context.Context, clID string) error {
	_, err := c.runContext(ctx, "revert", "-a", "-c", clID, "//...")
	return err
}

// RevertUnchangedPaths reverts the given paths (client files or local paths with wildcards)
// that are identical to the depot version. Returns an error only on p4 failures.
func (c *Client) RevertUnchangedPaths(paths []string) error {
	return c.RevertUnchangedPathsContext(context.Background(), paths)
}

func (c *Client) RevertUnchangedPathsContext(ctx context.Context, paths []string) error {
	args := append([]string{"revert", "-a"}, fileSpecs(paths)...)
	_, err := c.runContext(ctx, args...)
	return err
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
	args := append([]string{"reopen", "-c", clID}, fileSpecs(clientFiles)...)
	return c.run(args...)
}

// RevertCL discards the confirmed files while leaving later additions to the changelist intact.
func (c *Client) RevertCL(clID string, clientFiles []string) (string, error) {
	if clID == "" || len(clientFiles) == 0 {
		return "", fmt.Errorf("a changelist and selected files are required to revert")
	}
	args := append([]string{"revert", "-c", clID}, fileSpecs(clientFiles)...)
	return c.run(args...)
}

type RevertResult struct {
	Files           []string
	AddedLocalFiles []string
}

// RevertForDiscard returns local paths positively confirmed as reverted.
func (c *Client) RevertForDiscard(clID string, clientFiles []string) (RevertResult, error) {
	if len(clientFiles) == 0 {
		return RevertResult{}, fmt.Errorf("selected files are required to revert")
	}
	args := []string{"revert"}
	if clID != "" {
		args = append(args, "-c", clID)
	}
	args = append(args, fileSpecs(clientFiles)...)
	out, err := c.runZtag(args...)
	if err != nil {
		return RevertResult{}, err
	}
	var result RevertResult
	for _, record := range parseZtag(out) {
		if record["clientFile"] == "" || record["oldAction"] == "" || (record["action"] != "reverted" && record["action"] != "abandoned") {
			continue
		}
		result.Files = append(result.Files, record["clientFile"])
		if record["oldAction"] == string(ActionAdd) && record["action"] == "abandoned" {
			result.AddedLocalFiles = append(result.AddedLocalFiles, record["clientFile"])
		}
	}
	return result, nil
}

// Reopen moves a file to a different changelist.
func (c *Client) Reopen(clID, clientFile string) (string, error) {
	return c.run("reopen", "-c", clID, fileSpec(clientFile))
}

// DeleteChange deletes an empty pending changelist.
func (c *Client) DeleteChange(clID string) error {
	_, err := c.run("change", "-d", clID)
	return err
}

// CreateChange creates a new pending changelist and returns its ID.
// UpdateChangeDescription updates the description of an existing numbered CL.
func (c *Client) UpdateChangeDescription(clID, description string) error {
	return c.updateChangeDescription(context.Background(), clID, description)
}

func (c *Client) updateChangeDescription(ctx context.Context, clID, description string) error {
	out, err := c.runContext(ctx, "change", "-o", clID)
	if err != nil {
		return err
	}
	// Replace description field — everything after "Description:\t" until the next field
	lines := strings.Split(out, "\n")
	var result []string
	skip := false
	for _, line := range lines {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "Description:") {
			result = append(result, descriptionSpec(description))
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
	_, err = c.runWithStdinContext(ctx, spec, "change", "-i")
	return err
}

func (c *Client) CreateChange(description string) (string, error) {
	return c.createChange(context.Background(), description)
}

func (c *Client) createChange(ctx context.Context, description string) (string, error) {
	spec := "Change:\tnew\n" + descriptionSpec(description) + "\n"
	out, err := c.runWithStdinContext(ctx, spec, "change", "-i")
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

func descriptionSpec(description string) string {
	description = strings.ReplaceAll(description, "\r\n", "\n")
	return "Description:\n\t" + strings.ReplaceAll(description, "\n", "\n\t")
}

// CurrentCL returns the highest CL currently synced in the workspace (have revision).
func (c *Client) CurrentCL() string {
	scope := "//..."
	if c.Stream != "" {
		scope = c.Stream + "/..."
	}
	out, err := c.run("changes", "-m1", "-s", "submitted", scope+"@"+c.Workspace)
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
	args = append(args, fileSpec(path))
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
	args = append(args, fileSpec(depotFile))
	out, err := c.runZtag(args...)
	if err != nil {
		return nil, err
	}
	records := parseZtag(out)
	entries := make([]FilelogEntry, 0, len(records))
	for _, r := range records {
		for i := 0; r[fmt.Sprintf("change%d", i)] != ""; i++ {
			suffix := strconv.Itoa(i)
			rev, _ := strconv.Atoi(r["rev"+suffix])
			date := r["time"+suffix]
			if ts, err := strconv.ParseInt(date, 10, 64); err == nil {
				date = time.Unix(ts, 0).Format("02 Jan 2006 15:04")
			}
			entries = append(entries, FilelogEntry{
				DepotFile: r["depotFile"], Rev: rev, Change: r["change"+suffix],
				Action: Action(r["action"+suffix]), Date: date, Author: r["user"+suffix],
				Client: r["client"+suffix], Description: strings.TrimSpace(r["desc"+suffix]),
			})
		}
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
			args = append(args, fileSpec(clientFile))
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
		args = append(args, fileSpec(path))
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
		User:       r["userName"],
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
	expected, err := c.shelvedFiles(clID)
	if err != nil {
		return fmt.Errorf("inspect shelf: %w", err)
	}
	if len(expected) == 0 {
		return fmt.Errorf("shelf has no files to unshelve; shelf was preserved")
	}
	tryUnshelve := func(extraArgs ...string) (out string, runErr error) {
		args := append(c.globalFlags(), "unshelve", "-s", clID)
		args = append(args, extraArgs...)
		cmd := exec.Command("p4", args...)
		configureP4Command(cmd)
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

	if runErr != nil {
		return fmt.Errorf("unshelve: %s: %s", runErr, strings.TrimSpace(out))
	}
	if !unshelved {
		return fmt.Errorf("no files unshelved — shelf was preserved")
	}
	if needsResolve {
		// Files are open but require resolve before the shelf can be deleted.
		return fmt.Errorf("files unshelved with conflicts — shelf kept, delete manually after resolving")
	}
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.Contains(line, " - unshelved") {
			count++
		}
	}
	if count != len(expected) {
		return fmt.Errorf("unshelved %d of %d files; shelf was preserved", count, len(expected))
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
func (c *Client) SyncPath(depotPath string) (string, error) {
	return c.run("sync", fileSpec(depotPath))
}

func (c *Client) ForceSyncPath(depotPath string) (string, error) {
	return c.run("sync", "-f", fileSpec(depotPath))
}

// WhereLocal converts a depot path to a local filesystem path using p4 where.
func (c *Client) WhereLocal(depotPath string) (string, error) {
	mapping, err := c.where(depotPath)
	if err != nil {
		return "", err
	}
	return mapping["path"], nil
}

// ResolveCommand lets the CLI prepare merge inputs and record the accepted resolution.
func (c *Client) ResolveCommand(file string) (*exec.Cmd, error) {
	if strings.TrimSpace(file) == "" {
		return nil, fmt.Errorf("select a file to resolve")
	}
	cmd := exec.Command("p4", append(c.globalFlags(), "resolve", fileSpec(file))...)
	if c.Root != "" {
		cmd.Dir = c.Root
	}
	return cmd, nil
}

// WhereClient returns the effective client-view path, including remappings.
func (c *Client) WhereClient(path string) (string, error) {
	mapping, err := c.where(path)
	if err != nil {
		return "", err
	}
	return mapping["clientFile"], nil
}

func (c *Client) where(path string) (map[string]string, error) {
	out, err := c.runZtag("where", fileSpec(path))
	if err != nil {
		return nil, err
	}
	var mapping map[string]string
	for _, record := range parseZtag(out) {
		if _, excluded := record["unmap"]; excluded {
			mapping = nil
			continue
		}
		if record["path"] != "" {
			mapping = record
		}
	}
	if mapping == nil {
		return nil, fmt.Errorf("path is not mapped in workspace %s: %s", c.Workspace, path)
	}
	return mapping, nil
}

// Reconcile runs p4 reconcile on the given local path to detect offline changes
// (edit, add, delete). Uses -m for mtime-based detection and -f for literal special filenames.
func (c *Client) Reconcile(localPath string) (string, error) {
	// -f takes literal filenames, but directory wildcards still use encoded filespecs.
	if strings.HasSuffix(localPath, "/...") || strings.HasSuffix(localPath, `\...`) || strings.HasSuffix(localPath, "/*") || strings.HasSuffix(localPath, `\*`) {
		localPath = fileSpec(localPath)
	}
	return c.run("reconcile", "-m", "-f", localPath)
}

// RestoreReadOnly makes tracked-but-unopened files under localWildcard read-only.
// This corrects permissions after offline file replacement (e.g. copying a folder
// from outside the workspace). A no-op if the workspace uses the allwrite option.
// Returns the count of files made read-only (-1 = allwrite skip, -2 = have error, -3 = opened error).
func (c *Client) RestoreReadOnly(localWildcard string) (int, error) {
	if allwrite, err := c.isAllWrite(); err != nil {
		return -1, fmt.Errorf("isAllWrite: %w", err)
	} else if allwrite {
		return -1, nil
	}
	localPaths, depotPaths, err := c.haveLocalPaths(localWildcard)
	if err != nil {
		return -2, fmt.Errorf("have(%s): %w", localWildcard, err)
	}
	opened, err := c.openedDepotPaths(localWildcard)
	if err != nil {
		return -3, fmt.Errorf("opened(%s): %w", localWildcard, err)
	}
	count := 0
	for i, depot := range depotPaths {
		if opened[depot] {
			continue
		}
		info, err := os.Stat(localPaths[i])
		if err != nil {
			continue
		}
		if err := os.Chmod(localPaths[i], info.Mode()&^0222); err == nil {
			count++
		}
	}
	return count, nil
}

func (c *Client) isAllWrite() (bool, error) {
	if c.Workspace == "" {
		return false, nil
	}
	out, err := c.run("client", "-o", c.Workspace)
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "Options:") {
			for _, opt := range strings.Fields(strings.TrimPrefix(line, "Options:")) {
				if opt == "allwrite" {
					return true, nil
				}
			}
			return false, nil
		}
	}
	return false, nil
}

func (c *Client) haveLocalPaths(localWildcard string) (localPaths, depotPaths []string, err error) {
	out, runErr := c.run("have", fileSpec(localWildcard))
	if runErr != nil {
		msg := runErr.Error()
		if strings.Contains(msg, "not on client") || strings.Contains(msg, "no such file") {
			return nil, nil, nil
		}
		return nil, nil, runErr
	}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// format: //depot/path#rev - /local/path
		idx := strings.Index(line, " - ")
		if idx < 0 {
			continue
		}
		depotRev := line[:idx]
		local := line[idx+3:]
		depot := depotRev
		if h := strings.Index(depotRev, "#"); h > 0 {
			depot = depotRev[:h]
		}
		localPaths = append(localPaths, local)
		depotPaths = append(depotPaths, depot)
	}
	return
}

func (c *Client) openedDepotPaths(localWildcard string) (map[string]bool, error) {
	out, err := c.run("opened", fileSpec(localWildcard))
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "not opened") || strings.Contains(msg, "no file") {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	opened := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSpace(out), "\n") {
		if line == "" {
			continue
		}
		// format: //depot/path#head - action change (type)
		if h := strings.Index(line, "#"); h > 0 {
			opened[line[:h]] = true
		}
	}
	return opened, nil
}

// Edit opens the given local path for edit. Accepts wildcards (e.g. /path/...).
func (c *Client) Edit(localPath string) (string, error) {
	return c.run("edit", fileSpec(localPath))
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
	if strings.TrimSpace(wildcard) == "" {
		return nil, fmt.Errorf("a workspace file pattern is required")
	}
	out, err := c.runZtag("have", wildcard)
	if err != nil {
		msg := err.Error()
		if strings.Contains(msg, "no such file") || strings.Contains(msg, "not on client") {
			return nil, nil
		}
		return nil, err
	}
	var files []string
	for _, record := range parseZtag(out) {
		if path := record["depotFile"]; path != "" {
			files = append(files, path)
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
	ClientPath string // path in the local workspace tree
	DepotPath  string // depot path (computed for untracked files)
	LocalPath  string // absolute local path
	Tracked    bool   // true if synced via p4 have
}

// BrowserWorkspaceFast scans local entries and maps them through the client view.
// Paths use client syntax so remapped and imported directories retain their local hierarchy.
func (c *Client) BrowserWorkspaceFast(depotPath string) (dirs []string, files []WorkspaceEntry, err error) {
	localDir, clientDir, err := c.workspaceDirectory(depotPath)
	if err != nil {
		return nil, nil, err
	}
	entries, err := os.ReadDir(localDir)
	if err != nil {
		return nil, nil, err
	}
	var paths []string
	for _, e := range entries {
		if e.IsDir() {
			dirs = append(dirs, clientDir+"/"+escapeFileSpec(e.Name()))
		} else {
			paths = append(paths, clientDir+"/"+escapeFileSpec(e.Name()))
		}
	}
	if len(paths) == 0 {
		return dirs, nil, nil
	}
	out, err := c.whereFiles(paths)
	if err != nil {
		return nil, nil, err
	}
	mapped := map[string]WorkspaceEntry{}
	for _, r := range parseZtag(out) {
		if _, excluded := r["unmap"]; excluded {
			delete(mapped, r["clientFile"])
			continue
		}
		if r["clientFile"] != "" && r["path"] != "" {
			mapped[r["clientFile"]] = WorkspaceEntry{ClientPath: r["clientFile"], DepotPath: r["depotFile"], LocalPath: r["path"]}
		}
	}
	for _, path := range paths {
		if entry, ok := mapped[path]; ok {
			files = append(files, entry)
		}
	}
	return dirs, files, nil
}

func escapeFileSpec(path string) string {
	return strings.NewReplacer("%", "%25", "@", "%40", "#", "%23", "*", "%2A").Replace(path)
}

// Depot and client paths already use encoded filespecs; local paths contain literal filenames.
func fileSpec(path string) string {
	if strings.HasPrefix(path, "//") {
		return path
	}
	wildcard := ""
	if path == "*" || strings.HasSuffix(path, "/*") || strings.HasSuffix(path, `\*`) {
		path = strings.TrimSuffix(path, "*")
		wildcard = "*"
	}
	return escapeFileSpec(path) + wildcard
}

func fileSpecs(paths []string) []string {
	result := make([]string, len(paths))
	for i, path := range paths {
		result[i] = fileSpec(path)
	}
	return result
}

func (c *Client) workspaceDirectory(path string) (local, client string, err error) {
	if c.Root == "" || c.Workspace == "" {
		return "", "", fmt.Errorf("workspace root and client are required")
	}
	prefix := "//" + c.Workspace
	if path == prefix || strings.HasPrefix(path, prefix+"/") {
		rel := strings.TrimPrefix(strings.TrimPrefix(path, prefix), "/")
		// Client syntax preserves the workspace layout, independently of depot mappings.
		rel = strings.NewReplacer("%40", "@", "%23", "#", "%2A", "*", "%25", "%").Replace(rel)
		local = filepath.Join(c.Root, filepath.FromSlash(rel))
		within, relErr := filepath.Rel(c.Root, local)
		if relErr != nil || within == ".." || strings.HasPrefix(within, ".."+string(filepath.Separator)) {
			return "", "", fmt.Errorf("client path escapes workspace root: %s", path)
		}
		return local, strings.TrimRight(path, "/"), nil
	}
	mapping, err := c.where(strings.TrimRight(path, "/") + "/...")
	if err != nil {
		return "", "", err
	}
	return strings.TrimSuffix(mapping["path"], "..."), strings.TrimRight(strings.TrimSuffix(mapping["clientFile"], "..."), "/"), nil
}

// BrowserWorkspaceStatus fetches p4-side data for a workspace directory:
// tracked file paths, depot subdirectory paths, and any tracked files missing
// from the local filesystem (e.g. deleted on disk).
func (c *Client) BrowserWorkspaceStatus(depotPath string) (haveFiles, depotDirs []string, missingFiles []WorkspaceEntry, err error) {
	_, clientDir, err := c.workspaceDirectory(depotPath)
	if err != nil {
		return nil, nil, nil, err
	}
	wildcard := clientDir + "/*"
	files, err := c.workspaceHaveEntries(wildcard)
	if err != nil {
		return nil, nil, nil, err
	}
	for _, file := range files {
		haveFiles = append(haveFiles, file.DepotPath)
		if _, statErr := os.Stat(file.LocalPath); os.IsNotExist(statErr) {
			missingFiles = append(missingFiles, file)
		} else if statErr != nil {
			return nil, nil, nil, statErr
		}
	}
	dirs, err := c.BrowserDirs(wildcard)
	if err != nil {
		return nil, nil, nil, err
	}
	if len(dirs) > 0 {
		var paths []string
		for _, dir := range dirs {
			paths = append(paths, dir+"/...")
		}
		out, err := c.whereFiles(paths)
		if err != nil {
			return nil, nil, nil, err
		}
		for _, r := range parseZtag(out) {
			if _, excluded := r["unmap"]; !excluded && r["clientFile"] != "" {
				depotDirs = append(depotDirs, strings.TrimSuffix(r["clientFile"], "/..."))
			}
		}
	}
	return haveFiles, depotDirs, missingFiles, nil
}

// BrowserWorkspaceFiles returns synced files in client syntax for tree searches.
func (c *Client) BrowserWorkspaceFiles(wildcard string) ([]string, error) {
	entries, err := c.workspaceHaveEntries(wildcard)
	if err != nil {
		return nil, err
	}
	var paths []string
	for _, entry := range entries {
		paths = append(paths, entry.ClientPath)
	}
	return paths, nil
}

func (c *Client) workspaceHaveEntries(wildcard string) ([]WorkspaceEntry, error) {
	paths, err := c.BrowserHaveFiles(wildcard)
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, nil
	}
	mappings, err := c.whereFiles(paths)
	if err != nil {
		return nil, err
	}
	byDepot := map[string]WorkspaceEntry{}
	for _, r := range parseZtag(mappings) {
		if _, excluded := r["unmap"]; excluded {
			delete(byDepot, r["depotFile"])
			continue
		}
		if r["path"] != "" {
			byDepot[r["depotFile"]] = WorkspaceEntry{ClientPath: r["clientFile"], DepotPath: r["depotFile"], LocalPath: r["path"], Tracked: true}
		}
	}
	var entries []WorkspaceEntry
	for _, path := range paths {
		if entry, ok := byDepot[path]; ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
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
// Works for both text (@@) and binary ("files differ") files.
func (c *Client) HasChanges(clientFile string) (bool, error) {
	return c.HasChangesContext(context.Background(), clientFile)
}

func (c *Client) HasChangesContext(ctx context.Context, clientFile string) (bool, error) {
	out, err := c.runZtagContext(ctx, "diff", "-sa", fileSpec(clientFile))
	if err != nil {
		return false, err
	}
	return len(parseZtag(out)) > 0, nil
}

// FilesDiffStatus returns opened files that differ from their have revisions.
func (c *Client) FilesDiffStatus() (map[string]bool, error) {
	out, err := c.runZtag("diff", "-sa")
	if err != nil {
		return nil, err
	}
	changed := map[string]bool{}
	for _, record := range parseZtag(out) {
		if depot := record["depotFile"]; depot != "" {
			changed[depot] = true
		}
	}
	return changed, nil
}

// DeletePath marks a file or path (e.g. "//depot/stream/dir/...") for delete.
func (c *Client) DeletePath(path string) error {
	_, err := c.run("delete", fileSpec(path))
	return err
}

// MergeStream promotes a child into its parent using the parent's workspace.
func (c *Client) MergeStream(child string) (string, error) {
	if child == "" {
		return "", fmt.Errorf("select the child stream to promote")
	}
	out, err := c.runZtag("stream", "-o", child)
	if err != nil {
		return "", err
	}
	records := parseZtag(out)
	if len(records) == 0 || records[0]["Parent"] == "" || records[0]["Parent"] == "none" {
		return "", fmt.Errorf("stream %s has no parent to promote into", child)
	}
	parent := records[0]["Parent"]
	current, err := c.workspaceStream()
	if err != nil {
		return "", err
	}
	if current != parent {
		return "", fmt.Errorf("promotion from %s requires a workspace mapped to parent %s; current workspace %s uses %s", child, parent, c.Workspace, current)
	}
	return c.run("copy", "-S", child)
}

// CopyStream merges the parent into the selected child workspace.
func (c *Client) CopyStream(targetStream string) (string, error) {
	if targetStream == "" {
		targetStream = c.Stream
	}
	if targetStream == "" {
		return "", fmt.Errorf("select the child stream to merge into")
	}
	current, err := c.workspaceStream()
	if err != nil {
		return "", err
	}
	if current != targetStream {
		return "", fmt.Errorf("merge into %s requires a workspace mapped to that stream; current workspace %s uses %s", targetStream, c.Workspace, current)
	}
	return c.run("merge")
}

func (c *Client) workspaceStream() (string, error) {
	out, err := c.runZtag("client", "-o", c.Workspace)
	if err != nil {
		return "", err
	}
	records := parseZtag(out)
	if len(records) == 0 || records[0]["Stream"] == "" {
		return "", fmt.Errorf("workspace %s is not mapped to a stream", c.Workspace)
	}
	return records[0]["Stream"], nil
}

// IntegrateClassic integrates files from source to target using classic (non-stream) depot paths.
func (c *Client) IntegrateClassic(source, target string) (string, error) {
	return c.run("integrate", fileSpec(source), fileSpec(target))
}

// SyncDryRun returns the number of changelists the workspace is behind head.
func (c *Client) SyncDryRun() (int, error) {
	// Get the have CL as a number first to avoid per-file revision ambiguity.
	haveCL := c.CurrentCL()
	if haveCL == "" {
		return 0, nil
	}
	scope := "//..."
	if c.Stream != "" {
		scope = c.Stream + "/..."
	}
	out, err := c.run("changes", "-s", "submitted", fmt.Sprintf("%s@%s,#head", scope, haveCL))
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
	configureP4Command(cmd)
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
