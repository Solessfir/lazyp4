# lazyp4

A lazygit-inspired terminal UI for Perforce (p4).

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)

## Features

- Browse open changelists and files in tree or flat view
- Color-coded file status — yellow = modified, default = checked out unchanged, green = added, red = deleted
- Colorized unified diff viewer
- File revision history
- Mark individual files for partial submit
- Shelve and unshelve changelists (including cross-stream via `-S`)
- Stream depot support — stream browser, stream switching, merge/copy integration
- Fetch (dry-run sync) — see how many files are pending before committing to a sync
- Reconcile offline changes (`p4 reconcile` + `p4 edit` fallback)
- Discard added files with optional local delete (`dd`)
- Skip discard confirmation for files with no local changes
- Conflict resolution — external merge tool, auto-resolve (accept theirs/yours/safe)
- `[?]` indicator on files needing resolve
- Mouse support — click to focus panes and select files
- Keyboard-driven with lazygit-style numbered pane shortcuts
- Context-sensitive hotkey bar

## Requirements

- Go 1.21+
- `p4` CLI installed and in `$PATH`
- A configured Perforce workspace (`P4PORT`, `P4USER`, `P4CLIENT`, a P4CONFIG file, or the lazyp4 config file)

## Installation

Download the latest binary from [releases](https://github.com/Solessfir/lazyp4/releases).

Or build from source:

```bash
git clone https://github.com/solessfir/lazyp4
cd lazyp4
go build -o lazyp4 ./cmd/main.go
```

## Configuration

Connection settings are resolved in this order (highest priority first):

1. **P4CONFIG file** — if `$P4CONFIG` is set, lazyp4 walks up from the current directory looking for that file (e.g. `.p4config`)
2. **Environment variables** — `P4PORT`, `P4USER`, `P4CLIENT`
3. **lazyp4.toml** — platform config file (lowest priority)

Config file location:

| Platform | Path |
|----------|------|
| Linux    | `~/.config/lazyp4/lazyp4.toml` |
| macOS    | `~/Library/Application Support/lazyp4/lazyp4.toml` |
| Windows  | `<lazyp4.exe directory>/lazyp4.toml` |

All fields are optional:

```toml
[p4]
port         = "ssl:your-server:1666"  # Perforce server address
user         = "youruser"              # Perforce username
client       = "your-workspace"        # Workspace (client) name
env_over_toml = true                   # true: env vars win over toml; false: toml wins

[auth]
store_password = false           # Cache password in system keyring

[ui]
theme          = "dark"          # "dark" or "light"
fetch_interval = "10m"           # Background refresh interval (empty = disabled)
```

## Keybindings

Press `?` inside the app for context-sensitive help.

### Global

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up / down |
| `tab` | Cycle pane focus |
| `1` – `5` | Jump to pane by number |
| `esc` | Back to browser pane |
| `g` | Toggle history / diff pane |
| `f` | Fetch — dry-run sync, shows pending file count |
| `p` | Sync workspace |
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
| `d` | Discard (revert) checked-out file |
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
| `d` | Discard (revert) — skips confirmation if file is unchanged; `dd` to also delete local file for added files |
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
