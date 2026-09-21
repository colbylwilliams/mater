package disk

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestSizeCountsAllocatedBlocks(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "a", "b"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a", "b", "f"), make([]byte, 8192), 0o644); err != nil {
		t.Fatal(err)
	}

	u := Size(dir)
	if u.Bytes < 8192 {
		t.Errorf("Bytes = %d, want at least the 8192 written", u.Bytes)
	}
	if u.Files != 1 {
		t.Errorf("Files = %d, want 1", u.Files)
	}
	if u.Partial {
		t.Error("Partial = true for a fully readable tree")
	}
}

// du counts a file reached through several links once, and so must this.
func TestSizeCountsHardLinksOnce(t *testing.T) {
	dir := t.TempDir()
	original := filepath.Join(dir, "original")
	if err := os.WriteFile(original, make([]byte, 64*1024), 0o644); err != nil {
		t.Fatal(err)
	}
	single := Size(dir).Bytes

	for i := 0; i < 3; i++ {
		if err := os.Link(original, filepath.Join(dir, "link"+string(rune('a'+i)))); err != nil {
			t.Skipf("hard links unsupported here: %v", err)
		}
	}

	if got := Size(dir).Bytes; got != single {
		t.Errorf("Bytes = %d after hard-linking, want %d", got, single)
	}
}

func TestSizeOfMissingTreeIsPartial(t *testing.T) {
	u := Size(filepath.Join(t.TempDir(), "nope"))
	if !u.Partial {
		t.Error("Partial = false for a missing tree")
	}
	if u.Bytes != 0 {
		t.Errorf("Bytes = %d, want 0", u.Bytes)
	}
}

func TestSizeAllReportsProgressForEveryRoot(t *testing.T) {
	dir := t.TempDir()
	var roots []string
	for _, name := range []string{"a", "b", "c"} {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(p, "f"), make([]byte, 1024), 0o644); err != nil {
			t.Fatal(err)
		}
		roots = append(roots, p)
	}

	seen := 0
	usage := SizeAll(roots, func(done, total int) {
		seen++
		if total != len(roots) {
			t.Errorf("progress total = %d, want %d", total, len(roots))
		}
	})
	if len(usage) != len(roots) {
		t.Errorf("measured %d roots, want %d", len(usage), len(roots))
	}
	if seen != len(roots) {
		t.Errorf("progress fired %d times, want %d", seen, len(roots))
	}
}

// A full walk is far too slow to run just to sort by age, so LastBuilt reads
// only the paths Cargo touches on every build.
func TestLastBuiltReadsCargoMarkers(t *testing.T) {
	dir := t.TempDir()
	profile := filepath.Join(dir, "debug")
	if err := os.MkdirAll(filepath.Join(profile, ".fingerprint"), 0o755); err != nil {
		t.Fatal(err)
	}

	recent := time.Now().Add(-time.Hour)
	if err := os.Chtimes(filepath.Join(profile, ".fingerprint"), recent, recent); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-100 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatal(err)
	}

	got := LastBuilt(dir)
	if got.Sub(recent).Abs() > 2*time.Second {
		t.Errorf("LastBuilt = %v, want the fingerprint time %v", got, recent)
	}
}

func TestLastBuiltFallsBackToTheDirectoryItself(t *testing.T) {
	dir := t.TempDir()
	if got := LastBuilt(dir); got.IsZero() {
		t.Error("LastBuilt is zero for a directory with no Cargo markers")
	}
	if got := LastBuilt(filepath.Join(dir, "nope")); !got.IsZero() {
		t.Errorf("LastBuilt = %v for a missing directory, want zero", got)
	}
}
