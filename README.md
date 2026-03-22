# lazyp4

A lazygit-inspired terminal UI for Perforce (p4).

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)

## Features

- Browse open changelists and files in tree or flat view
- Colorized unified diff viewer
- File revision history
- Mark individual files for partial submit
- Shelve and unshelve changelists (including cross-stream via `-S`)
- Stream depot support — stream browser, stream switching, merge/copy integration
- Fetch (dry-run sync) — see how many files are pending before committing to a sync
- Reconcile offline changes (`p4 reconcile` + `p4 edit` fallback)
- Conflict resolution — external merge tool, auto-resolve (accept theirs/yours/safe)
- `[?]` indicator on files needing resolve
- Mouse support — click to focus panes and select files
- Keyboard-driven with lazygit-style numbered pane shortcuts
- Context-sensitive hotkey bar

## Requirements

- Go 1.21+
- `p4` CLI installed and in `$PATH`
- A configured Perforce workspace (`P4PORT`, `P4USER`, `P4CLIENT` or `~/.lazyp4.toml`)

## Installation

Download the latest binary from [releases](https://github.com/Solessfir/lazyp4/releases).

Or build from source:

```bash
git clone https://github.com/solessfir/lazyp4
cd lazyp4
go build -o lazyp4 ./cmd/main.go
```

## Configuration

lazyp4 reads `~/.lazyp4.toml`:

```toml
# Perforce server address. Use "ssl:" prefix for SSL connections.
port      = "ssl:your-server:1666"

# Perforce username.
user      = "youruser"

# Workspace (client) name.
workspace = "your-workspace"
```

All fields are optional — environment variables `P4PORT`, `P4USER`, and `P4CLIENT` take precedence, and lazyp4 will also pick up whatever is already set in your `p4` environment (e.g. from `p4 set` or `~/.p4enviro`).

## Keybindings

Press `?` inside the app for context-sensitive help.

### Global

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up / down |
| `tab` / `←` / `→` | Cycle pane focus |
| `1` – `6` | Jump to pane by number |
| `esc` | Back to browser pane |
| `g` | Toggle history / diff pane |
| `f` | Fetch — dry-run sync, shows pending file count |
| `S` | Sync workspace |
| `r` | Refresh |
| `c` | Cancel current operation |
| `v` | Visual mode (disables mouse for text selection) |
| `?` | Keybindings help |
| `q` | Quit |

### Browser pane

| Key | Action |
|-----|--------|
| `enter` / `l` | Expand directory |
| `h` | Collapse directory |
| `b` | Toggle Workspace / Depot Browser |
| `t` | Toggle tree / flat view |
| `/` | Filter / search |
| `space` | Reconcile file or folder (edit / add / delete) |
| `D` | Mark for delete |
| `F` | Force sync selected file or folder |

### Pending pane

| Key | Action |
|-----|--------|
| `space` | Mark / unmark file for partial submit |
| `enter` | Open file |
| `l` / `h` | Expand / collapse folder |
| `s` | Submit — opens description prompt; uses marked files if any |
| `e` / `E` | Shelve (with revert) / shelve only |
| `m` | Move file(s) to a different CL |
| `d` | Discard (revert) — uses marked files if any |
| `R` | Show conflicts (scoped to file or folder) |
| `t` | Toggle tree / flat view |
| `/` | Filter / search |

### Streams pane

| Key | Action |
|-----|--------|
| `enter` / `l` | Switch workspace to selected stream |
| `i` | Integrate — pull from parent (`m`) or push to parent (`c`) |

### Shelved pane

| Key | Action |
|-----|--------|
| `u` | Unshelve + delete shelf |
| `d` | Delete shelf |

### Conflicts pane

| Key | Action |
|-----|--------|
| `enter` | Open merge tool (`$P4MERGE` or `$EDITOR`) |
| `a` | Auto-resolve (accept theirs, then branch) |
| `y` | Auto-resolve (accept yours) |
| `s` | Auto-resolve (safe) |
| `esc` | Close conflicts pane |

### History / Log pane

| Key | Action |
|-----|--------|
| `space` / `enter` | Checkout workspace to selected CL |

## Disclaimer

This project was built with agentic AI coding (Claude). I have a life.
