package p4

// Action represents the type of operation on a file in a changelist.
type Action string

const (
	ActionEdit   Action = "edit"
	ActionAdd    Action = "add"
	ActionDelete Action = "delete"
	ActionBranch Action = "branch"
	ActionInteg  Action = "integrate"
	ActionMove   Action = "move/add"
	ActionMoveD  Action = "move/delete"
)

// OpenedFile is a file currently open in a changelist.
type OpenedFile struct {
	DepotFile    string
	ClientFile   string
	Action       Action
	Type         string
	Change       string // changelist number, or "default"
	Revision     int
	NeedsResolve bool // true when p4 opened reports "unresolved" flag
	HasChanges   bool // true when local content differs from the have revision (edit only)
}

// Changelist groups opened files under a single CL.
type Changelist struct {
	ID          string // "default" or numeric string
	Description string
	Files       []OpenedFile
}

// FilelogEntry is one revision entry from p4 filelog.
type FilelogEntry struct {
	DepotFile   string
	Rev         int
	Change      string
	Action      Action
	Date        string // formatted as "2006-01-02 15:04"
	Author      string
	Client      string // workspace name
	Description string
}

// ConflictFile represents a file that needs resolving.
type ConflictFile struct {
	ClientFile string
	FromFile   string
}

// ShelvedFile is a file stored in a shelved changelist.
type ShelvedFile struct {
	DepotFile string
	Action    Action
}

// ShelvedCL is a changelist with shelved files.
type ShelvedCL struct {
	ID          string
	Description string
	User        string
	Files       []ShelvedFile
}

// StreamInfo represents a single stream from p4 streams.
type StreamInfo struct {
	Path   string // e.g. //depot/main
	Parent string // e.g. //depot/main or "none"
	Type   string // mainline, development, release, virtual, task
	Name   string // short name
}

// WorkspaceInfo contains basic info from p4 info.
type WorkspaceInfo struct {
	Client     string // workspace/client name
	User       string // p4 user name
	Stream     string // client stream path (e.g. //depot/main), empty if not stream client
	ServerAddr string
	Root       string // local root path of the workspace
}
