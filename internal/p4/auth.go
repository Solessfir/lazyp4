package p4

import (
	"errors"
	"fmt"
	"log"
	"os"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "lazyp4"

// TicketValid returns true if the current p4 ticket is still valid.
func (c *Client) TicketValid() bool {
	_, err := c.run("login", "-s")
	return err == nil
}

// Login attempts to log in using the provided password.
func (c *Client) Login(password string) error {
	_, err := c.runWithStdin(password+"\n", "login")
	return err
}

// KeyringGet retrieves the stored password for p4user.
// Returns ("", nil) if not found, ("", err) if keychain is unavailable.
func KeyringGet(p4user string) (string, error) {
	pw, err := keyring.Get(keyringService, p4user)
	if err == nil {
		return pw, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return "", fmt.Errorf("keyring unavailable: %w", err)
}

// KeyringSet stores the password in the OS keychain.
func KeyringSet(p4user, password string) error {
	return keyring.Set(keyringService, p4user, password)
}

// EnsureLoggedIn checks the ticket and attempts auto-login if expired.
// promptFn is called when a password must be entered interactively; it
// receives a prompt string and returns the entered password (or an error).
// Returns an error only when login ultimately fails.
func (c *Client) EnsureLoggedIn(promptFn func(prompt string) (string, error)) error {
	if _, err := exec.LookPath("p4"); err != nil {
		return fmt.Errorf("p4 executable not found in PATH - please install the Perforce CLI")
	}

	if c.TicketValid() {
		return nil
	}

	p4user := c.User
	if p4user == "" {
		p4user = os.Getenv("P4USER")
	}

	// Try keychain first.
	pw, keyringErr := KeyringGet(p4user)
	if keyringErr != nil {
		log.Printf("warning: keyring unavailable (%v), falling back to prompt", keyringErr)
	}

	if pw != "" {
		if err := c.Login(pw); err == nil {
			return nil
		}
		// Stored password no longer valid - fall through to prompt.
		log.Printf("stored password rejected, prompting user")
	}

	// Interactive prompt.
	if promptFn == nil {
		return errors.New("ticket expired and no password available")
	}
	password, err := promptFn("Enter Perforce password: ")
	if err != nil {
		return fmt.Errorf("password prompt cancelled: %w", err)
	}
	if err := c.Login(password); err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	// Store on success if keychain is available.
	if keyringErr == nil {
		if storeErr := KeyringSet(p4user, password); storeErr != nil {
			log.Printf("warning: could not store password in keyring: %v", storeErr)
		}
	}
	return nil
}

// Trust accepts the server fingerprint non-interactively.
func (c *Client) Trust() error {
	out, err := c.run("trust", "-y")
	if err != nil {
		return err
	}
	if strings.Contains(out, "already") || strings.Contains(out, "Added") {
		return nil
	}
	return nil
}
