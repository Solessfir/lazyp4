package config

import (
	"bufio"
	"os"
	"path/filepath"
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
	Port       string `toml:"port"`
	Client     string `toml:"client"`
	User       string `toml:"user"`
	EnvOverTOML bool   `toml:"env_over_toml"` // if true, env vars take precedence over toml values
}

// AuthConfig controls authentication behaviour.
type AuthConfig struct {
	StorePassword bool `toml:"store_password"`
}

// UIConfig holds display preferences.
type UIConfig struct {
	Theme         string `toml:"theme"`
	FetchInterval string `toml:"fetch_interval"` // e.g. "10m", "30s". Empty = disabled.
}

// Load reads the platform config file, then overrides with env vars, then
// overrides with a P4CONFIG file found by walking up from CWD.
//
// Precedence (highest → lowest):
//
//	P4CONFIG file in CWD (or ancestor) > env vars > lazyp4.toml
func Load() (*Config, error) {
	cfg := &Config{
		P4:   P4Config{EnvOverTOML: true},
		Auth: AuthConfig{StorePassword: false},
		UI:   UIConfig{Theme: "dark", FetchInterval: "10m"},
	}

	// 1. lazyp4.toml — read first to get EnvOverTOML flag and base P4 values.
	if path, err := configPath(); err == nil {
		if _, err := os.Stat(path); err == nil {
			if _, err := toml.DecodeFile(path, cfg); err != nil {
				return nil, err
			}
		}
	}

	// 2. Merge env vars according to precedence.
	//    env_over_toml=true (default): env wins when both are set.
	//    env_over_toml=false: toml wins; env only fills fields toml left empty.
	envPort := os.Getenv("P4PORT")
	envClient := os.Getenv("P4CLIENT")
	envUser := os.Getenv("P4USER")
	if cfg.P4.EnvOverTOML {
		if envPort != "" {
			cfg.P4.Port = envPort
		}
		if envClient != "" {
			cfg.P4.Client = envClient
		}
		if envUser != "" {
			cfg.P4.User = envUser
		}
	} else {
		if cfg.P4.Port == "" {
			cfg.P4.Port = envPort
		}
		if cfg.P4.Client == "" {
			cfg.P4.Client = envClient
		}
		if cfg.P4.User == "" {
			cfg.P4.User = envUser
		}
	}

	// 3. P4CONFIG file always wins (highest priority).
	// Only searched when $P4CONFIG names a file, matching standard p4 behaviour.
	if p4cfg := loadP4Config(); p4cfg != nil {
		if v, ok := p4cfg["P4PORT"]; ok {
			cfg.P4.Port = v
		}
		if v, ok := p4cfg["P4CLIENT"]; ok {
			cfg.P4.Client = v
		}
		if v, ok := p4cfg["P4USER"]; ok {
			cfg.P4.User = v
		}
	}

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

// loadP4Config searches CWD and its ancestors for the file named by $P4CONFIG.
// Returns nil if $P4CONFIG is unset or no file is found.
func loadP4Config() map[string]string {
	name := os.Getenv("P4CONFIG")
	if name == "" {
		return nil
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil
	}

	for dir := cwd; ; dir = filepath.Dir(dir) {
		path := filepath.Join(dir, name)
		if f, err := os.Open(path); err == nil {
			result := parseKeyValue(f)
			f.Close()
			return result
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
	}
	return nil
}

// parseKeyValue reads simple KEY=VALUE lines, ignoring comments and blanks.
func parseKeyValue(f *os.File) map[string]string {
	m := make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			m[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return m
}
