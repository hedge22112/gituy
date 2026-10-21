// Package config loads gituy's settings from the user config file and an
// optional per-repository override.
package config

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is gituy's configuration.
type Config struct {
	Fetch          Fetch  `yaml:"fetch"`
	Status         Status `yaml:"status"`
	ReturnAfterRun bool   `yaml:"return_after_run"`
	MaxSuggestions int    `yaml:"max_suggestions"`
}

// Fetch controls background fetching.
type Fetch struct {
	Enabled  bool          `yaml:"enabled"`
	OnStart  bool          `yaml:"on_start"`
	Interval time.Duration `yaml:"interval"` // 0 = only on start
	Remotes  []string      `yaml:"remotes"`  // empty or ["*"] = all
	Exclude  []string      `yaml:"exclude"`
	Prune    bool          `yaml:"prune"`
	Tags     bool          `yaml:"tags"`
	Timeout  time.Duration `yaml:"timeout"`
}

// Status controls background status refreshing.
type Status struct {
	PollInterval      time.Duration `yaml:"poll_interval"`       // 0 = off
	Watch             bool          `yaml:"watch"`               // watch files for changes
	WatchPollInterval time.Duration `yaml:"watch_poll_interval"` // safety-net poll while watching
	MaxWatches        int           `yaml:"max_watches"`         // above this, poll the working tree
}

// Default returns the built-in defaults.
func Default() Config {
	return Config{
		Fetch: Fetch{
			Enabled:  true,
			OnStart:  true,
			Interval: 5 * time.Minute,
			Prune:    true,
			Timeout:  60 * time.Second,
		},
		Status: Status{
			PollInterval:      3 * time.Second,
			Watch:             true,
			WatchPollInterval: 30 * time.Second,
			MaxWatches:        8000,
		},
		ReturnAfterRun: true,
		MaxSuggestions: 6,
	}
}

// UserPath is the user config file, e.g. ~/.config/gituy/config.yaml.
func UserPath() string {
	dir, err := os.UserConfigDir()
	if err != nil {
		return ""
	}
	return filepath.Join(dir, "gituy", "config.yaml")
}

// Load returns defaults overlaid with the user config and then the repo
// config (<gitdir>/gituy.yaml). Missing files are fine.
func Load(gitDir string) (Config, error) {
	cfg := Default()
	paths := []string{UserPath()}
	if gitDir != "" {
		paths = append(paths, filepath.Join(gitDir, "gituy.yaml"))
	}
	for _, p := range paths {
		if p == "" {
			continue
		}
		data, err := os.ReadFile(p)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return cfg, err
		}
		if err := yaml.Unmarshal(data, &cfg); err != nil {
			return cfg, errors.New(p + ": " + err.Error())
		}
	}
	return cfg, nil
}

// FetchRemotes resolves which remotes to fetch. nil means all.
func (f Fetch) FetchRemotes(available []string) []string {
	all := len(f.Remotes) == 0 || (len(f.Remotes) == 1 && f.Remotes[0] == "*")
	if all && len(f.Exclude) == 0 {
		return nil
	}
	want := map[string]bool{}
	for _, r := range f.Remotes {
		want[r] = true
	}
	skip := map[string]bool{}
	for _, r := range f.Exclude {
		skip[r] = true
	}
	out := []string{}
	for _, r := range available {
		if (all || want[r]) && !skip[r] {
			out = append(out, r)
		}
	}
	return out
}
