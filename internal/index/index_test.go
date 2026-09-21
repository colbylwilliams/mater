package index

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadMissingFileIsEmpty(t *testing.T) {
	ix, err := Load(filepath.Join(t.TempDir(), "nope", ".index"))
	if err != nil {
		t.Fatalf("Load returned %v, want nil for a missing file", err)
	}
	if ix.Len() != 0 {
		t.Errorf("Len = %d, want 0", ix.Len())
	}
}

func TestRoundTrip(t *testing.T) {
	dir := t.TempDir()
	build := filepath.Join(dir, "ab", "cdef")
	ws := filepath.Join(dir, "workspace")
	mkdirs(t, build, ws)

	path := filepath.Join(dir, ".index")
	ix := New(path)
	ix.Set(build, ws)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	reloaded, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	got, ok := reloaded.Workspace(build)
	if !ok || got != ws {
		t.Errorf("Workspace(%q) = %q, %v; want %q, true", build, got, ok, ws)
	}
}

// A row for a build directory that has been deleted is worse than useless: a
// future directory landing on the same hash would inherit a stale workspace.
func TestSaveDropsDeletedBuildDirs(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "ab", "alive")
	ws := filepath.Join(dir, "workspace")
	mkdirs(t, alive, ws)

	path := filepath.Join(dir, ".index")
	ix := New(path)
	ix.Set(alive, ws)
	ix.Set(filepath.Join(dir, "cd", "gone"), ws)

	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	reloaded, _ := Load(path)
	if reloaded.Len() != 1 {
		t.Errorf("Len = %d, want 1", reloaded.Len())
	}
}

func TestCompact(t *testing.T) {
	dir := t.TempDir()
	alive := filepath.Join(dir, "ab", "alive")
	mkdirs(t, alive)

	ix := New(filepath.Join(dir, ".index"))
	ix.Set(alive, dir)
	ix.Set(filepath.Join(dir, "cd", "gone"), dir)
	ix.Compact()

	if ix.Len() != 1 {
		t.Errorf("Len after Compact = %d, want 1", ix.Len())
	}
}

// Save writes through a temporary file, so an interrupted run must never leave
// a half-written index in place of a good one.
func TestSaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	build := filepath.Join(dir, "ab", "cdef")
	mkdirs(t, build)

	path := filepath.Join(dir, ".index")
	ix := New(path)
	ix.Set(build, dir)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := ix.Save(); err != nil {
		t.Fatalf("second Save: %v", err)
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Name() != ".index" && e.Name() != "ab" {
			t.Errorf("Save left %q behind", e.Name())
		}
	}
}

func TestMalformedLinesAreIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".index")
	content := "no-tab-here\n\n\tmissing-key\nkey\t\n/a/b\t/c/d\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	ix, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if ix.Len() != 1 {
		t.Errorf("Len = %d, want 1", ix.Len())
	}
}

func mkdirs(t *testing.T, paths ...string) {
	t.Helper()
	for _, p := range paths {
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
}
