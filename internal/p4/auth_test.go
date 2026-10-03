package p4

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// Reuse the test executable as a portable CLI fixture without a second build.
func TestMain(m *testing.M) {
	if os.Getenv("LAZYP4_AUTH_HELPER") != "1" {
		os.Exit(m.Run())
	}
	args := os.Args[1:]
	if file, err := os.OpenFile(os.Getenv("LAZYP4_AUTH_LOG"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600); err == nil {
		fmt.Fprintln(file, strings.Join(args, " "))
		file.Close()
	}
	for len(args) > 0 && strings.HasPrefix(args[0], "-") {
		switch args[0] {
		case "-p", "-u", "-c", "-d":
			args = args[2:]
		default:
			args = args[1:]
		}
	}
	if len(args) == 0 {
		os.Exit(2)
	}
	switch args[0] {
	case "info":
		if os.Getenv("LAZYP4_AUTH_INFO_FAIL") == "1" {
			fmt.Fprintln(os.Stderr, "The authenticity of this server's SSL fingerprint is not established. Use p4 trust.")
			os.Exit(1)
		}
		_, loggedIn := os.Stat(os.Getenv("LAZYP4_AUTH_LOG") + ".logged-in")
		if os.Getenv("LAZYP4_AUTH_INFO_AUTH") == "1" && loggedIn != nil {
			fmt.Fprintln(os.Stderr, "Perforce password (P4PASSWD) invalid or unset.")
			os.Exit(1)
		}
		info := map[string]string{"clientName": "native-client", "userName": "native-user", "serverAddress": "native-server:1666", "clientRoot": os.Getenv("LAZYP4_AUTH_ROOT"), "clientStream": "//streams/main"}
		if os.Getenv("LAZYP4_AUTH_INFO_HIDDEN") == "1" && loggedIn != nil {
			info["clientRoot"], info["clientName"], info["serverAddress"] = "*unknown*", "*unknown*", "*unknown*"
		}
		if strings.Contains(strings.Join(os.Args, " "), "-Mj") {
			json.NewEncoder(os.Stdout).Encode(info)
		} else {
			for key, value := range info {
				fmt.Printf("... %s %s\n", key, value)
			}
		}
	case "login":
		if len(args) > 1 && args[1] == "-s" && os.Getenv("LAZYP4_AUTH_VALID_TICKET") != "1" {
			fmt.Fprintln(os.Stderr, "Your session has expired, please login again.")
			os.Exit(1)
		}
		if len(args) == 1 {
			if err := os.WriteFile(os.Getenv("LAZYP4_AUTH_LOG")+".logged-in", nil, 0600); err != nil {
				os.Exit(2)
			}
		}
	default:
		fmt.Fprintln(os.Stderr, "unexpected authentication command")
		os.Exit(2)
	}
	os.Exit(0)
}

func authCLI(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	name := "p4"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	if err := os.WriteFile(filepath.Join(dir, name), contents, 0700); err != nil {
		t.Fatal(err)
	}
	log := filepath.Join(dir, "commands.log")
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("LAZYP4_AUTH_HELPER", "1")
	t.Setenv("LAZYP4_AUTH_LOG", log)
	t.Setenv("LAZYP4_AUTH_ROOT", dir)
	t.Setenv("LAZYP4_AUTH_INFO_FAIL", "")
	t.Setenv("LAZYP4_AUTH_INFO_AUTH", "")
	t.Setenv("LAZYP4_AUTH_INFO_HIDDEN", "")
	t.Setenv("LAZYP4_AUTH_VALID_TICKET", "")
	return log
}

func mockCredentials(t *testing.T, get func(string, string) (string, error), set func(string, string, string) error) {
	t.Helper()
	oldGet, oldSet := keyringGet, keyringSet
	keyringGet, keyringSet = get, set
	t.Cleanup(func() { keyringGet, keyringSet = oldGet, oldSet })
}

func TestPasswordStorageOptOut(t *testing.T) {
	authCLI(t)
	mockCredentials(t, func(_, _ string) (string, error) {
		t.Fatal("keyring read while password storage is disabled")
		return "", nil
	}, func(_, _, _ string) error {
		t.Fatal("keyring write while password storage is disabled")
		return nil
	})
	client := &Client{}
	prompted := false
	if err := client.EnsureLoggedIn(func(string) (string, error) { prompted = true; return "password", nil }); err != nil {
		t.Fatal(err)
	}
	if !prompted || client.User != "native-user" || client.Port != "native-server:1666" || client.Workspace != "native-client" || client.Root == "" || client.Stream != "//streams/main" {
		t.Fatalf("effective identity or prompt missing: %+v, prompted=%v", client, prompted)
	}
	if err := client.StoreCredential("password"); err != nil {
		t.Fatal(err)
	}
}

func TestStoredCredentialUsesServerAndEffectiveUser(t *testing.T) {
	authCLI(t)
	var readKey, writeKey string
	mockCredentials(t, func(service, identity string) (string, error) {
		if service != keyringService {
			t.Fatalf("unexpected keyring service %q", service)
		}
		readKey = identity
		return "", nil
	}, func(_, identity, password string) error {
		writeKey = identity
		if password != "password" {
			t.Fatal("wrong credential stored")
		}
		return nil
	})
	client := &Client{StorePassword: true}
	if err := client.EnsureLoggedIn(func(string) (string, error) { return "password", nil }); err != nil {
		t.Fatal(err)
	}
	if readKey != `["native-server:1666","native-user"]` || writeKey != readKey {
		t.Fatalf("credential lookup/store used wrong identity: %q / %q", readKey, writeKey)
	}
	other := &Client{Port: "other-server:1666", User: client.User, StorePassword: true}
	key, err := other.credentialKey()
	if err != nil || key == readKey {
		t.Fatalf("different servers shared a credential slot: %q, %v", key, err)
	}
}

func TestUnknownFingerprintStopsBeforeCredentials(t *testing.T) {
	log := authCLI(t)
	t.Setenv("LAZYP4_AUTH_INFO_FAIL", "1")
	mockCredentials(t, func(_, _ string) (string, error) {
		t.Fatal("credentials read before server trust was verified")
		return "", nil
	}, func(_, _, _ string) error { t.Fatal("credentials stored before server trust was verified"); return nil })
	err := (&Client{StorePassword: true}).EnsureLoggedIn(func(string) (string, error) {
		t.Fatal("password requested before server trust was verified")
		return "", errors.New("unexpected prompt")
	})
	if err == nil || !strings.Contains(err.Error(), "verify the server fingerprint with your administrator") {
		t.Fatalf("missing actionable trust failure: %v", err)
	}
	commands, err := os.ReadFile(log)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(commands), "trust") || strings.Contains(string(commands), "login") {
		t.Fatalf("authentication mutated trust or attempted login: %s", commands)
	}
}

func TestRestrictedInfoStillAllowsLogin(t *testing.T) {
	for _, mode := range []string{"LAZYP4_AUTH_INFO_AUTH", "LAZYP4_AUTH_INFO_HIDDEN"} {
		t.Run(mode, func(t *testing.T) {
			authCLI(t)
			t.Setenv(mode, "1")
			var stored bool
			mockCredentials(t, func(_, identity string) (string, error) {
				if identity != `["native-server:1666","native-user"]` {
					t.Fatalf("restricted info changed native identity: %q", identity)
				}
				return "", nil
			}, func(_, _, _ string) error { stored = true; return nil })
			client := &Client{Port: "native-server:1666", User: "native-user", Workspace: "native-client", Root: "existing-root", StorePassword: true}
			if err := client.EnsureLoggedIn(func(string) (string, error) {
				if client.Root != "existing-root" || client.Workspace != "native-client" {
					t.Fatal("hidden workspace info overwrote known identity")
				}
				return "password", nil
			}); err != nil {
				t.Fatal(err)
			}
			if !stored || client.Root != os.Getenv("LAZYP4_AUTH_ROOT") || client.Stream != "//streams/main" {
				t.Fatalf("post-login identity or storage missing: %+v, stored=%v", client, stored)
			}
		})
	}
}
