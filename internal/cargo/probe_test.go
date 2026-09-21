package cargo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Probe writes a stub manifest at a path that does not exist. If it ever ran
// against a live worktree it would delete that worktree's Cargo.toml, so the
// guard matters more than the result.
func TestProbeRefusesAnExistingPath(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(live, "Cargo.toml")
	if err := os.WriteFile(manifest, []byte("[package]\nname = \"real\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if _, ok := Probe(context.Background(), live); ok {
		t.Error("Probe reported success against a live path")
	}
	data, err := os.ReadFile(manifest)
	if err != nil {
		t.Fatalf("Probe removed a real manifest: %v", err)
	}
	if string(data) != "[package]\nname = \"real\"\n" {
		t.Error("Probe overwrote a real manifest")
	}
}

func TestProbeRejectsRelativePaths(t *testing.T) {
	if _, ok := Probe(context.Background(), "relative/path"); ok {
		t.Error("Probe accepted a relative path")
	}
	if _, ok := Probe(context.Background(), ""); ok {
		t.Error("Probe accepted an empty path")
	}
}

// Whatever the outcome, a probe must give back every directory it created.
func TestProbeLeavesNoTrace(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "a", "b", "c", "worktree")

	Probe(context.Background(), target)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("Probe left %d entr(ies) behind in the sandbox: %v", len(entries), entries)
	}
}

// A path whose parent chain reaches the home directory is out of scope; the
// probe must not start creating directories at that level.
func TestProbeRefusesToClimbToHome(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if _, ok := Probe(context.Background(), home); ok {
		t.Error("Probe accepted the home directory")
	}
	if _, ok := Probe(context.Background(), "/"); ok {
		t.Error("Probe accepted the filesystem root")
	}
}

func TestResolveIgnoresDirectoriesWithoutAManifest(t *testing.T) {
	if _, ok := Resolve(context.Background(), t.TempDir()); ok {
		t.Error("Resolve reported success for a directory with no Cargo.toml")
	}
}
