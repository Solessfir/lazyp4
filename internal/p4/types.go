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
	DepotFile  string
	ClientFile string
	Action     Action
	Type       string
	Change     string // changelist number, or "default"
	Revision   int
}

// Changelist groups opened files under a single CL.
type Changelist struct {
	ID          string // "default" or numeric string
	Description string
	Files       []OpenedFile
}

// DepotFile is a file in the depot (from p4 files / p4 fstat).
type DepotFile struct {
	DepotFile  string
	ClientFile string
	HeadRev    int
	HeadAction Action
	HeadType   string
}

// FilelogEntry is one revision entry from p4 filelog.
type FilelogEntry struct {
	DepotFile   string
	Rev         int
	Change      string
	Action      Action
	Date        string
	Author      string
	Description string
}

// ConflictFile represents a file that needs resolving.
type ConflictFile struct {
	ClientFile string
	FromFile   string
	StartRev   int
	EndRev     int
}

// WorkspaceInfo contains basic info from p4 info.
type WorkspaceInfo struct {
	Client     string // workspace/client name
	Stream     string // client stream path (e.g. //depot/main), empty if not stream client
	ServerAddr string
}
