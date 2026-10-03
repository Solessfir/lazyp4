package config

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSettingsPrecedence(t *testing.T) {
	settings := parseNativeSettings(strings.NewReader("P4PORT=ssl:parent:1666 (config '/workspace (copy)/.p4config')\r\nP4USER=registered-user (set)\r\nP4CLIENT=workspace with spaces (enviro)\r\n"))
	for _, envWins := range []bool{true, false} {
		cfg := P4Config{Port: "toml:1666", User: "toml-user", EnvOverTOML: envWins}
		applyNativeSettings(&cfg, settings)
		if cfg.Port != "ssl:parent:1666" || cfg.Client != "workspace with spaces" {
			t.Fatalf("config override or native fallback failed: %+v", cfg)
		}
		wantUser := "toml-user"
		if envWins {
			wantUser = "registered-user"
		}
		if cfg.User != wantUser {
			t.Fatalf("env_over_toml=%v: got user %q, want %q", envWins, cfg.User, wantUser)
		}
	}
}

func TestNativeConfigInheritanceAndDuplicates(t *testing.T) {
	if _, err := exec.LookPath("p4"); err != nil {
		t.Skip("native configuration fixture requires p4 in PATH")
	}
	root := t.TempDir()
	child := filepath.Join(root, "child")
	if err := os.Mkdir(child, 0700); err != nil {
		t.Fatal(err)
	}
	for path, contents := range map[string]string{
		filepath.Join(root, ".p4config"):       "P4PORT=first:1666\nP4PORT=last:1666\n",
		filepath.Join(child, ".p4config"):      "P4CLIENT=child-client\n",
		filepath.Join(root, "settings.enviro"): "P4USER=enviro-user\n",
	} {
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("P4CONFIG", ".p4config")
	t.Setenv("P4ENVIRO", filepath.Join(root, "settings.enviro"))
	t.Setenv("P4PORT", "env:1666")
	t.Setenv("P4USER", "")
	if err := os.Unsetenv("P4USER"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("P4CLIENT", "")
	t.Chdir(child)
	settings, err := nativeSettings()
	if err != nil {
		t.Fatal(err)
	}
	if settings["P4PORT"].value != "first:1666" || !settings["P4PORT"].config || settings["P4CLIENT"].value != "child-client" || settings["P4USER"].value != "enviro-user" {
		t.Fatalf("effective settings differ from native ancestor/duplicate/enviro semantics: %#v", settings)
	}
	// Explicit TOML values remain below P4CONFIG, including inherited settings.
	cfg := P4Config{Port: "toml:1666", User: "toml-user", Client: "toml-client", EnvOverTOML: false}
	applyNativeSettings(&cfg, settings)
	if cfg.Port != "first:1666" || cfg.Client != "child-client" || cfg.User != "toml-user" {
		t.Fatalf("TOML precedence failed: %+v", cfg)
	}
}
