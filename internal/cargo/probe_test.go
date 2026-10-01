package cargo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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

// Siblings under a deleted parent all need that parent recreated. Probed at
// once, whichever finishes first cannot remove the parent while another is
// still inside it, and a probe that found the parent already there never owned
// it, so the batch as a whole has to give it back.
func TestProbeAllLeavesNoTraceWhenProbesShareAParent(t *testing.T) {
	dir := t.TempDir()
	var dirs []string
	for i := range 16 {
		dirs = append(dirs, filepath.Join(dir, "gone", "parent", fmt.Sprintf("worktree-%d", i)))
	}
	// Nested probes share their missing ancestors too.
	dirs = append(dirs, filepath.Join(dir, "gone"), filepath.Join(dir, "gone", "parent"))

	ProbeAll(context.Background(), dirs)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("ProbeAll left %d entr(ies) behind in the sandbox: %v", len(entries), entries)
	}
}

// A stub inside a live workspace whose members glob covers it is answered for
// that workspace, so the build directory Cargo reports is the live one's.
// Accepting it would record live output against a path about to vanish, and
// the next prune would take it as an orphan.
func TestProbeRejectsAnAnswerForAnEnclosingWorkspace(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo is not installed")
	}
	live := t.TempDir()
	if err := os.MkdirAll(filepath.Join(live, "crates"), 0o755); err != nil {
		t.Fatal(err)
	}
	manifest := "[workspace]\nmembers = [\"crates/*\"]\nresolver = \"2\"\n"
	if err := os.WriteFile(filepath.Join(live, "Cargo.toml"), []byte(manifest), 0o644); err != nil {
		t.Fatal(err)
	}

	if bd, ok := Probe(context.Background(), filepath.Join(live, "crates", "gone")); ok {
		t.Errorf("Probe accepted the enclosing workspace's build directory %s", bd)
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
