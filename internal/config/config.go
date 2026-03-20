package config

import (
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Config holds all lazyp4 configuration.
type Config struct {
	P4   P4Config   `toml:"p4"`
	Auth AuthConfig `toml:"auth"`
	UI   UIConfig   `toml:"ui"`
}

// P4Config holds Perforce connection settings.
type P4Config struct {
	Port      string `toml:"port"`
	Client    string `toml:"client"`
	User      string `toml:"user"`
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

// Load reads ~/.lazyp4.toml and merges environment variables.
// Env vars P4PORT, P4CLIENT, P4USER take precedence over the file.
func Load() (*Config, error) {
	cfg := &Config{
		Auth: AuthConfig{StorePassword: true},
		UI:   UIConfig{Theme: "dark", FetchInterval: "10m"},
	}

	path := filepath.Join(os.Getenv("HOME"), ".lazyp4.toml")
	if _, err := os.Stat(path); err == nil {
		if _, err := toml.DecodeFile(path, cfg); err != nil {
			return nil, err
		}
	}

	// Env vars override file values.
	if v := os.Getenv("P4PORT"); v != "" {
		cfg.P4.Port = v
	}
	if v := os.Getenv("P4CLIENT"); v != "" {
		cfg.P4.Client = v
	}
	if v := os.Getenv("P4USER"); v != "" {
		cfg.P4.User = v
	}

	return cfg, nil
}
