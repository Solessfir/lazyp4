package main

import (
	"fmt"
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"golang.org/x/term"

	"github.com/solessfir/lazyp4/internal/config"
	"github.com/solessfir/lazyp4/internal/p4"
	"github.com/solessfir/lazyp4/internal/ui"
)

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "config error: %v\n", err)
		os.Exit(1)
	}

	client := &p4.Client{
		Port:          cfg.P4.Port,
		User:          cfg.P4.User,
		Workspace:     cfg.P4.Client,
		StorePassword: cfg.Auth.StorePassword,
	}

	// Verify the server and resolve native identity before authentication.
	if err := client.EnsureLoggedIn(terminalPrompt); err != nil {
		fmt.Fprintf(os.Stderr, "auth error: %v\n", err)
		os.Exit(1)
	}

	var fetchInterval time.Duration
	if cfg.UI.FetchInterval != "" {
		fetchInterval, err = time.ParseDuration(cfg.UI.FetchInterval)
		if err != nil {
			fmt.Fprintf(os.Stderr, "warning: invalid fetch_interval %q, disabling auto-fetch\n", cfg.UI.FetchInterval)
			fetchInterval = 0
		}
	}

	app := ui.New(client, fetchInterval, cfg.Linux.FileManager, cfg.UI.PendingTreeView)
	prog := tea.NewProgram(app, tea.WithAltScreen(), tea.WithMouseCellMotion())
	if _, err := prog.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "tui error: %v\n", err)
		os.Exit(1)
	}
}

// terminalPrompt reads a password from the terminal with hidden input.
func terminalPrompt(prompt string) (string, error) {
	fmt.Print(prompt)
	pw, err := term.ReadPassword(int(os.Stdin.Fd()))
	fmt.Println()
	return string(pw), err
}
