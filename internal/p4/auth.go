package p4

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os/exec"
	"strings"

	"github.com/zalando/go-keyring"
)

const keyringService = "lazyp4"

var keyringGet = keyring.Get
var keyringSet = keyring.Set

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

func (c *Client) credentialKey() (string, error) {
	if c.Port == "" || c.User == "" || c.Port == "*unknown*" || c.User == "*unknown*" {
		return "", errors.New("effective Perforce server and user are required for password storage")
	}
	identity, err := json.Marshal([]string{c.Port, c.User})
	return string(identity), err
}

func (c *Client) storedCredential() (string, error) {
	if !c.StorePassword {
		return "", nil
	}
	identity, err := c.credentialKey()
	if err != nil {
		return "", err
	}
	pw, err := keyringGet(keyringService, identity)
	if err == nil {
		return pw, nil
	}
	if errors.Is(err, keyring.ErrNotFound) {
		return "", nil
	}
	return "", fmt.Errorf("keyring unavailable: %w", err)
}

// StoreCredential caches a password only when storage is enabled.
func (c *Client) StoreCredential(password string) error {
	if !c.StorePassword {
		return nil
	}
	identity, err := c.credentialKey()
	if err != nil {
		return err
	}
	return keyringSet(keyringService, identity, password)
}

func (c *Client) loadIdentity() error {
	info, err := c.Info()
	if err != nil {
		return err
	}
	known := func(value string) bool { return value != "" && value != "*unknown*" }
	if c.Port == "" && known(info.ServerAddr) {
		c.Port = info.ServerAddr
	}
	if c.User == "" && known(info.User) {
		c.User = info.User
	}
	if c.Workspace == "" && known(info.Client) {
		c.Workspace = info.Client
	}
	if known(info.Root) {
		c.Root = info.Root
	}
	if known(info.Client) && info.Stream != "*unknown*" {
		c.Stream = info.Stream
	}
	return nil
}

// EnsureLoggedIn checks the ticket and attempts auto-login if expired.
// promptFn receives a prompt and returns a password when interactive login is needed.
func (c *Client) EnsureLoggedIn(promptFn func(prompt string) (string, error)) error {
	if _, err := exec.LookPath("p4"); err != nil {
		return fmt.Errorf("p4 executable not found in PATH - please install the Perforce CLI")
	}
	// Read-only info verifies the connection before any password is retrieved or sent.
	if err := c.loadIdentity(); err != nil {
		message := strings.ToLower(err.Error())
		if strings.Contains(message, "fingerprint") || strings.Contains(message, "p4 trust") || strings.Contains(message, "ssl") {
			return fmt.Errorf("cannot verify Perforce server: %w; verify the server fingerprint with your administrator, then run p4 trust for this server before restarting lazyp4", err)
		}
		// A server authentication response establishes trust but may hide workspace info.
		if !IsAuthError(err) {
			return fmt.Errorf("cannot read Perforce identity: %w", err)
		}
	}

	if c.TicketValid() {
		return nil
	}

	// Try keychain first.
	pw, keyringErr := c.storedCredential()
	if keyringErr != nil {
		log.Printf("warning: keyring unavailable (%v), falling back to prompt", keyringErr)
	}

	if pw != "" {
		if err := c.Login(pw); err == nil {
			_ = c.loadIdentity()
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
	if err := c.loadIdentity(); err != nil {
		log.Printf("warning: could not refresh Perforce identity after login: %v", err)
	}

	// Password storage is best effort after successful authentication.
	if c.StorePassword {
		if storeErr := c.StoreCredential(password); storeErr != nil {
			log.Printf("warning: could not store password in keyring: %v", storeErr)
		}
	}
	return nil
}
