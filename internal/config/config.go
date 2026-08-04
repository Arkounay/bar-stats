// Package config stores the application's settings and locates the game's
// replay folder.
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// appDir is the folder name used under the OS config and cache roots.
const appDir = "BeyondAllReplays"

// Config is the persisted user configuration.
type Config struct {
	// DemosPath is the folder holding .sdfz replay files.
	DemosPath string `json:"demosPath"`
	// WatchMode selects how new replays are noticed: "events" (default),
	// "poll", or "off". Empty means the default.
	WatchMode string `json:"watchMode,omitempty"`
	// PlayerName is the user's in-game name. Setting it lets the app mark
	// which matches they won and aggregate their record; leaving it empty
	// simply hides those features.
	PlayerName string `json:"playerName,omitempty"`
	// HighlightSelf emphasises the user's own series as soon as a replay is
	// opened, rather than waiting for them to press the toggle.
	HighlightSelf bool `json:"highlightSelf,omitempty"`
}

// Configured reports whether enough is set to start indexing.
func (c *Config) Configured() bool { return c.DemosPath != "" }

// Store reads and writes the configuration file.
type Store struct {
	path string
}

// NewStore locates the configuration under the OS-standard per-user config
// directory, creating it if needed.
func NewStore() (*Store, error) {
	root, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("locate config dir: %w", err)
	}
	dir := filepath.Join(root, appDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("create config dir: %w", err)
	}
	return &Store{path: filepath.Join(dir, "config.json")}, nil
}

// Path is the location of the configuration file, shown in the UI so the user
// can find or delete it.
func (s *Store) Path() string { return s.path }

// Load reads the configuration. A missing file is not an error: it yields the
// zero config, which drives the first-run setup flow.
func (s *Store) Load() (*Config, error) {
	data, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return &Config{}, nil
	}
	if err != nil {
		return nil, err
	}
	var c Config
	if err := json.Unmarshal(data, &c); err != nil {
		// A corrupt config should not permanently wedge the app; fall back to
		// the setup flow rather than refusing to start.
		return &Config{}, nil
	}
	return &c, nil
}

// Save writes the configuration atomically, so an interrupted write cannot
// leave a truncated file behind.
func (s *Store) Save(c *Config) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// CacheDir returns the per-user cache directory for the index, creating it.
func CacheDir() (string, error) {
	root, err := os.UserCacheDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, appDir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}
