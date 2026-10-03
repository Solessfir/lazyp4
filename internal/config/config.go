package config

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"

	"github.com/BurntSushi/toml"
)

// Config holds all lazyp4 configuration.
type Config struct {
	P4    P4Config    `toml:"p4"`
	Auth  AuthConfig  `toml:"auth"`
	UI    UIConfig    `toml:"ui"`
	Linux LinuxConfig `toml:"linux"`
}

// LinuxConfig holds Linux-specific settings.
type LinuxConfig struct {
	FileManager string `toml:"file_manager"` // e.g. "nemo", "nautilus". Auto-detected if empty.
}

// P4Config holds Perforce connection settings.
type P4Config struct {
	Port        string `toml:"port"`
	Client      string `toml:"client"`
	User        string `toml:"user"`
	EnvOverTOML bool   `toml:"env_over_toml"` // Native settings override TOML; P4CONFIG always wins.
}

// AuthConfig controls authentication behaviour.
type AuthConfig struct {
	StorePassword bool `toml:"store_password"`
}

// UIConfig holds display preferences.
type UIConfig struct {
	FetchInterval   string `toml:"fetch_interval"`    // e.g. "10m", "30s". Empty = disabled.
	PendingTreeView bool   `toml:"pending_tree_view"` // Start pending pane in tree view (default: true).
}

// Load combines TOML preferences with effective native Perforce settings.
func Load() (*Config, error) {
	cfg := &Config{
		P4:   P4Config{EnvOverTOML: true},
		Auth: AuthConfig{StorePassword: false},
		UI:   UIConfig{FetchInterval: "10m", PendingTreeView: true},
	}

	// Read TOML first so its preferences control native fallbacks.
	if path, err := configPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, err
			}
		}
	}

	settings, err := nativeSettings()
	if err != nil {
		return nil, err
	}
	applyNativeSettings(&cfg.P4, settings)

	return cfg, nil
}

// configPath returns the platform-appropriate path for lazyp4.toml.
//
//	Linux  : ~/.config/lazyp4/lazyp4.toml
//	macOS  : ~/Library/Application Support/lazyp4/lazyp4.toml
//	Windows: <dir of lazyp4.exe>/lazyp4.toml
func configPath() (string, error) {
	if runtime.GOOS == "windows" {
		exe, err := os.Executable()
		if err != nil {
			return "", err
		}
		return filepath.Join(filepath.Dir(exe), "lazyp4.toml"), nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "lazyp4", "lazyp4.toml"), nil
}

type nativeSetting struct {
	value  string
	config bool
}

var nativeSourceSuffix = regexp.MustCompile(` \((config(?: '.*')?|enviro|set(?: -s)?)\)$`)

func nativeSettings() (map[string]nativeSetting, error) {
	// Let p4 handle ancestor inheritance, P4ENVIRO and platform registry settings.
	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	cmd := exec.Command("p4", "-d", cwd, "set", "P4PORT", "P4USER", "P4CLIENT")
	hideWindow(cmd)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("cannot read native Perforce settings (install p4 in PATH): %w: %s", err, strings.TrimSpace(stderr.String()))
	}
	return parseNativeSettings(&stdout), nil
}

func parseNativeSettings(r io.Reader) map[string]nativeSetting {
	settings := make(map[string]nativeSetting)
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		key, value, ok := strings.Cut(strings.TrimRight(scanner.Text(), "\r"), "=")
		if !ok || (key != "P4PORT" && key != "P4USER" && key != "P4CLIENT") {
			continue
		}
		setting := nativeSetting{value: value}
		if suffix := nativeSourceSuffix.FindStringSubmatchIndex(value); suffix != nil {
			source := value[suffix[2]:suffix[3]]
			setting.value = value[:suffix[0]]
			setting.config = source == "config" || strings.HasPrefix(source, "config '")
		}
		settings[key] = setting
	}
	return settings
}

func applyNativeSettings(cfg *P4Config, settings map[string]nativeSetting) {
	for key, target := range map[string]*string{"P4PORT": &cfg.Port, "P4USER": &cfg.User, "P4CLIENT": &cfg.Client} {
		setting, ok := settings[key]
		if ok && (setting.value != "" || setting.config) && (setting.config || cfg.EnvOverTOML || *target == "") {
			*target = setting.value
		}
	}
}
