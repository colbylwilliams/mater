// Package config resolves mater's paths and user settings.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config is the user-editable settings file, merged over built-in defaults.
type Config struct {
	// BuildRoot holds every Rust build directory. It is the single stable path
	// that has to be excluded from Defender's real-time scanning.
	BuildRoot string `yaml:"build_root"`

	// Roots are extra checkouts to scan for target/ and node_modules. Copilot
	// worktrees are discovered automatically and do not belong here.
	Roots []string `yaml:"roots"`

	// WorktreeGlobs override worktree discovery. When empty, worktree parents
	// are derived from Copilot session state.
	WorktreeGlobs []string `yaml:"worktree_globs"`

	// SessionState is the Copilot session directory used to attribute build
	// directories to the work that produced them.
	SessionState string `yaml:"session_state"`

	// StaleAge is the default idle threshold for `mater prune --stale`.
	StaleAge string `yaml:"stale_age"`

	// ScanNodeModules includes node_modules alongside Rust output.
	ScanNodeModules *bool `yaml:"scan_node_modules"`

	// LogFile records background delete progress.
	LogFile string `yaml:"log_file"`

	path string
}

// Path is the config file this Config was loaded from, whether or not it exists.
func (c *Config) Path() string { return c.path }

// IndexFile maps build directories to the workspaces that produced them.
func (c *Config) IndexFile() string { return filepath.Join(c.BuildRoot, ".index") }

// NodeModules reports whether node_modules is in scope.
func (c *Config) NodeModules() bool { return c.ScanNodeModules == nil || *c.ScanNodeModules }

// DefaultPath is the config file location, honouring XDG_CONFIG_HOME.
func DefaultPath() string {
	if dir := os.Getenv("XDG_CONFIG_HOME"); dir != "" {
		return filepath.Join(dir, "mater", "config.yaml")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".config", "mater", "config.yaml")
}

func defaults(home string) *Config {
	return &Config{
		BuildRoot:    filepath.Join(home, ".rust-build"),
		SessionState: filepath.Join(home, ".copilot", "session-state"),
		StaleAge:     "5d",
		LogFile:      filepath.Join(home, "Library", "Logs", "mater.log"),
	}
}

// Load reads path, falling back to DefaultPath when empty. A missing file is
// not an error: the defaults alone are a working configuration.
func Load(path string) (*Config, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil, fmt.Errorf("resolve home directory: %w", err)
	}
	if path == "" {
		path = DefaultPath()
	}

	cfg := defaults(home)
	cfg.path = path

	data, err := os.ReadFile(path)
	switch {
	case errors.Is(err, os.ErrNotExist):
		cfg.expand(home)
		return cfg, nil
	case err != nil:
		return nil, fmt.Errorf("read %s: %w", path, err)
	}

	// Decode over the defaults so an omitted key keeps its default rather than
	// becoming a zero value.
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	cfg.expand(home)
	return cfg, nil
}

// expand turns ~ and $VARS into absolute paths so every consumer can treat the
// config as literal filesystem locations.
func (c *Config) expand(home string) {
	c.BuildRoot = Expand(c.BuildRoot, home)
	c.SessionState = Expand(c.SessionState, home)
	c.LogFile = Expand(c.LogFile, home)
	for i, r := range c.Roots {
		c.Roots[i] = Expand(r, home)
	}
	for i, g := range c.WorktreeGlobs {
		c.WorktreeGlobs[i] = Expand(g, home)
	}
}

// Expand resolves a leading ~ and any environment variables in p.
func Expand(p, home string) string {
	if p == "" {
		return ""
	}
	p = os.ExpandEnv(p)
	if p == "~" {
		return home
	}
	if strings.HasPrefix(p, "~/") {
		return filepath.Join(home, p[2:])
	}
	return p
}

// Shorten is Expand's inverse, for display only.
func Shorten(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if strings.HasPrefix(p, home+string(filepath.Separator)) {
		return "~" + p[len(home):]
	}
	return p
}

// Template is written by `mater config init`; every key is commented out so the
// file documents the defaults without pinning them.
const Template = `# mater configuration
# Every value below is the built-in default. Uncomment to override.

# Where Cargo writes build intermediates. This is the one stable path to
# exclude from Defender real-time scanning:
#
#   mdatp exclusion folder add --path ~/.rust-build
#
# It must match build.build-dir in ~/.cargo/config.toml.
# build_root: ~/.rust-build

# Extra checkouts to scan for target/ and node_modules. Copilot worktrees are
# discovered automatically, so list only repos outside them.
# roots:
#   - ~/GitHub/example/my-repo
#   - ~/GitHub/example/other-repo

# Override worktree discovery. Leave unset to derive worktree parents from
# Copilot session state, which adapts as new worktree roots appear.
# worktree_globs:
#   - ~/GitHub/copilot-worktrees/*/*

# Copilot session state, used to name build directories after the work that
# produced them.
# session_state: ~/.copilot/session-state

# Default idle threshold for ` + "`mater prune --stale`" + `.
# stale_age: 5d

# Include node_modules alongside Rust build output.
# scan_node_modules: true

# Background delete progress log.
# log_file: ~/Library/Logs/mater.log
`
