package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultsWhenNoFileExists(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("Load returned %v, want defaults for a missing file", err)
	}
	home, _ := os.UserHomeDir()
	if cfg.BuildRoot != filepath.Join(home, ".rust-build") {
		t.Errorf("BuildRoot = %q", cfg.BuildRoot)
	}
	if cfg.StaleAge != "5d" {
		t.Errorf("StaleAge = %q, want 5d", cfg.StaleAge)
	}
	if !cfg.NodeModules() {
		t.Error("NodeModules = false, want true by default")
	}
}

// An omitted key has to keep its default rather than becoming a zero value.
func TestPartialFileKeepsDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("stale_age: 2w\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.StaleAge != "2w" {
		t.Errorf("StaleAge = %q, want 2w", cfg.StaleAge)
	}
	home, _ := os.UserHomeDir()
	if cfg.BuildRoot != filepath.Join(home, ".rust-build") {
		t.Errorf("BuildRoot = %q, want the default", cfg.BuildRoot)
	}
	if cfg.LogFile == "" {
		t.Error("LogFile was cleared by a partial file")
	}
}

func TestTildeAndEnvExpansion(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	t.Setenv("MATER_TEST_ROOT", "/opt/scratch")
	content := `build_root: ~/custom-build
roots:
  - ~/code/one
  - $MATER_TEST_ROOT/two
worktree_globs:
  - ~/trees/*/*
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	home, _ := os.UserHomeDir()
	if cfg.BuildRoot != filepath.Join(home, "custom-build") {
		t.Errorf("BuildRoot = %q", cfg.BuildRoot)
	}
	if cfg.Roots[0] != filepath.Join(home, "code", "one") {
		t.Errorf("Roots[0] = %q", cfg.Roots[0])
	}
	if cfg.Roots[1] != "/opt/scratch/two" {
		t.Errorf("Roots[1] = %q", cfg.Roots[1])
	}
	if cfg.WorktreeGlobs[0] != filepath.Join(home, "trees", "*", "*") {
		t.Errorf("WorktreeGlobs[0] = %q", cfg.WorktreeGlobs[0])
	}
}

func TestScanNodeModulesCanBeDisabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("scan_node_modules: false\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.NodeModules() {
		t.Error("NodeModules = true, want false")
	}
}

func TestMalformedYamlIsAnError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("roots: [unclosed\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("Load accepted malformed YAML")
	}
}

func TestIndexFileLivesUnderTheBuildRoot(t *testing.T) {
	cfg, err := Load(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join(cfg.BuildRoot, ".index"); cfg.IndexFile() != want {
		t.Errorf("IndexFile = %q, want %q", cfg.IndexFile(), want)
	}
}

func TestShortenRoundTripsWithExpand(t *testing.T) {
	home, _ := os.UserHomeDir()
	p := filepath.Join(home, "a", "b")
	if got := Shorten(p); got != "~/a/b" {
		t.Errorf("Shorten = %q, want ~/a/b", got)
	}
	if got := Expand("~/a/b", home); got != p {
		t.Errorf("Expand = %q, want %q", got, p)
	}
	if got := Shorten("/elsewhere"); got != "/elsewhere" {
		t.Errorf("Shorten rewrote a path outside home: %q", got)
	}
	if got := Shorten(home); got != "~" {
		t.Errorf("Shorten(home) = %q, want ~", got)
	}
}

// The template is what a user edits, so every documented key has to be one the
// parser actually understands.
func TestTemplateIsValidAndComplete(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(Template), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("the shipped template does not parse: %v", err)
	}

	for _, key := range []string{
		"build_root", "roots", "worktree_globs",
		"session_state", "stale_age", "scan_node_modules", "log_file",
	} {
		if !strings.Contains(Template, "# "+key+":") && !strings.Contains(Template, "# "+key+"\n") {
			t.Errorf("template does not document %q", key)
		}
	}
}
