# lazyp4

A lazygit-inspired terminal UI for Perforce (p4).

![Go](https://img.shields.io/badge/Go-1.21+-00ADD8?logo=go)

## Features

- Browse open changelists and files in tree or flat view
- Colorized unified diff viewer
- File revision history
- Mark individual files for partial submit
- Shelve changelists
- Fetch (dry-run sync) — see how many files are pending before committing to a sync
- Conflict resolution via external merge tool
- Mouse support — click to focus panes and select files
- Keyboard-driven with lazygit-style numbered pane shortcuts

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
port      = "ssl:your-server:1666"
user      = "youruser"
workspace = "your-workspace"
```

Environment variables `P4PORT`, `P4USER`, and `P4CLIENT` override the config file.

## Keybindings

| Key | Action |
|-----|--------|
| `j` / `k` | Navigate up / down |
| `h` / `l` or `←` / `→` | Cycle pane focus left / right |
| `1` – `4` | Jump to pane: Files / History / Diff / Log |
| `tab` | Cycle pane focus forward |
| `esc` | Back to Files pane |
| `enter` | Expand / collapse directory |
| `space` | Mark / unmark file (or whole directory) for partial submit |
| `s` | Submit — opens description prompt; uses marked files if any |
| `d` | Discard (revert) — confirmation prompt; uses marked files if any |
| `S` | Sync with progress bar |
| `c` | Cancel current operation (sync or submit) |
| `f` | Fetch — dry-run sync, shows pending file count |
| `r` | Refresh file list |
| `t` | Toggle tree / flat view |
| `L` | File log for selected file |
| `R` | Show conflicts |
| `?` | Keybindings help |
| `q` | Quit |

## Disclaimer

This project was built with agentic AI coding (Claude). I have a life.
