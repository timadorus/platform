// Package cliapp implements timadorusctl: a cobra-based CLI that issues command and query
// HTTP calls against this platform's command-api/query-api, printing responses as JSON to
// stdout. See docs/PLAN.md §14 for the full command-naming and id-defaulting conventions
// every command in this package follows.
package cliapp

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is persisted as JSON at configPath (see configFilePath) and holds everything
// timadorusctl needs across invocations: where the two APIs live, the bearer token, and the
// tracked context (plan §14) — the authenticated user (informational only; see docs/PLAN.md
// §14's "narrow" id-defaulting decision — it never auto-fills a command argument) plus the
// current universe/campaign, which DO auto-fill the *parent* id of create/add/delete/list
// commands when not overridden by --universe/--campaign.
type Config struct {
	CommandAPIURL     string `json:"commandApiUrl"`
	QueryAPIURL       string `json:"queryApiUrl"`
	Token             string `json:"token,omitempty"`
	CurrentUserID     string `json:"currentUserId,omitempty"`
	CurrentUniverseID string `json:"currentUniverseId,omitempty"`
	CurrentCampaignID string `json:"currentCampaignId,omitempty"`
}

const (
	defaultCommandAPIURL = "http://localhost:8081"
	defaultQueryAPIURL   = "http://localhost:8082"
)

func defaultConfig() Config {
	return Config{CommandAPIURL: defaultCommandAPIURL, QueryAPIURL: defaultQueryAPIURL}
}

// configFilePath returns ~/.config/timadorusctl/config.json, honoring $XDG_CONFIG_HOME, or
// $TIMADORUSCTL_CONFIG if explicitly set (mainly for tests/CI).
func configFilePath() (string, error) {
	if p := os.Getenv("TIMADORUSCTL_CONFIG"); p != "" {
		return p, nil
	}
	dir, err := os.UserConfigDir()
	if err != nil {
		return "", fmt.Errorf("cliapp: resolve config dir: %w", err)
	}
	return filepath.Join(dir, "timadorusctl", "config.json"), nil
}

// LoadConfig reads the config file, returning defaultConfig() (not an error) if it doesn't
// exist yet — a brand new install has no config until the first `auth`/`config set-*` call.
func LoadConfig() (Config, error) {
	path, err := configFilePath()
	if err != nil {
		return Config{}, err
	}

	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return defaultConfig(), nil
	}
	if err != nil {
		return Config{}, fmt.Errorf("cliapp: read config: %w", err)
	}

	cfg := defaultConfig()
	if err := json.Unmarshal(data, &cfg); err != nil {
		return Config{}, fmt.Errorf("cliapp: parse config at %s: %w", path, err)
	}
	return cfg, nil
}

// Save writes cfg to the config file, creating its parent directory if needed.
func (c Config) Save() error {
	path, err := configFilePath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("cliapp: create config dir: %w", err)
	}

	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("cliapp: marshal config: %w", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("cliapp: write config: %w", err)
	}
	return nil
}

// Redacted returns a copy of c safe to print (the token is masked, never shown in full once
// set).
func (c Config) Redacted() Config {
	if c.Token != "" {
		c.Token = "********"
	}
	return c
}
