package reap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// victim builds a directory tree with a file in it and returns its path.
func victim(t *testing.T, root, name string) string {
	t.Helper()
	p := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Join(p, "deep"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(p, "deep", "artifact"), make([]byte, 4096), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestStageMovesAndManifestRoundTrips(t *testing.T) {
	root := t.TempDir()
	a := victim(t, root, "a")
	b := victim(t, root, "b")

	st, err := Stage(root, []Entry{
		{Source: a, Origin: "a"},
		{Source: b, Origin: "b"},
	})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	if len(st.Entries) != 2 {
		t.Fatalf("staged %d entries, want 2", len(st.Entries))
	}
	for _, p := range []string{a, b} {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("%s still in place after staging", p)
		}
	}

	st.Measure()
	if st.Total() == 0 {
		t.Error("Total = 0 after Measure, want the staged bytes")
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatalf("WriteManifest: %v", err)
	}

	got := ReadManifest(st.Dir)
	if len(got) != 2 {
		t.Fatalf("manifest has %d rows, want 2", len(got))
	}
	if got[0].Origin != "a" || got[0].Size == 0 {
		t.Errorf("row 0 = %+v, want origin a with a non-zero size", got[0])
	}
}

func TestRunDeletesEverythingIncludingStagingDir(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a"), Origin: "a"}})
	if err != nil {
		t.Fatalf("Stage: %v", err)
	}
	st.Measure()
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	log, err := OpenLog(filepath.Join(root, "logs", "mater.log"))
	if err != nil {
		t.Fatal(err)
	}
	if err := Run(context.Background(), st.Dir, log); err != nil {
		t.Fatalf("Run: %v", err)
	}
	log.Close()

	if _, err := os.Stat(st.Dir); err == nil {
		t.Error("staging directory survived Run")
	}
	if lines := Tail(filepath.Join(root, "logs", "mater.log"), 10); len(lines) < 2 {
		t.Errorf("log has %d lines, want start and finish records", len(lines))
	}
}

// Content staged without a manifest row still has to be removed, otherwise a
// partially written manifest would strand bytes forever.
func TestRunRemovesUnmanifestedContent(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a"), Origin: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(st.Dir, "stray"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	if err := Run(context.Background(), st.Dir, log); err != nil {
		t.Fatalf("Run: %v", err)
	}
	log.Close()

	if _, err := os.Stat(st.Dir); err == nil {
		t.Error("staging directory survived Run")
	}
}

func TestAdoptTakesOverAbandonedStaging(t *testing.T) {
	root := t.TempDir()

	abandoned, err := Stage(root, []Entry{{Source: victim(t, root, "old"), Origin: "old"}})
	if err != nil {
		t.Fatal(err)
	}
	current, err := Stage(root, []Entry{{Source: victim(t, root, "new"), Origin: "new"}})
	if err != nil {
		t.Fatal(err)
	}

	if n := current.Adopt(root); n != 1 {
		t.Fatalf("adopted %d directories, want 1", n)
	}
	if _, err := os.Stat(abandoned.Dir); err == nil {
		t.Error("abandoned staging still in the build root")
	}
	if len(current.Entries) != 2 {
		t.Errorf("current staging has %d entries, want 2", len(current.Entries))
	}
}

func TestAdoptIgnoresItself(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a"), Origin: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if n := st.Adopt(root); n != 0 {
		t.Errorf("Adopt took over %d directories, want 0 — it adopted itself", n)
	}
}

func TestDiscardOnlyRemovesEmptyStaging(t *testing.T) {
	root := t.TempDir()

	empty, err := Stage(root, nil)
	if err != nil {
		t.Fatal(err)
	}
	empty.Discard()
	if _, err := os.Stat(empty.Dir); err == nil {
		t.Error("empty staging directory survived Discard")
	}

	full, err := Stage(root, []Entry{{Source: victim(t, root, "a"), Origin: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	full.Discard()
	if _, err := os.Stat(full.Dir); err != nil {
		t.Error("Discard removed a staging directory that held content")
	}
}

// A shard whose build directories are gone should disappear, but Finder
// metadata must not be mistaken for real content, and a shard that still holds
// a build directory must survive.
func TestPruneShards(t *testing.T) {
	root := t.TempDir()

	metadataOnly := filepath.Join(root, "ab")
	if err := os.MkdirAll(metadataOnly, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(metadataOnly, ".DS_Store"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	occupied := filepath.Join(root, "cd", "build")
	if err := os.MkdirAll(occupied, 0o755); err != nil {
		t.Fatal(err)
	}

	empty := filepath.Join(root, "ef")
	if err := os.MkdirAll(empty, 0o755); err != nil {
		t.Fatal(err)
	}

	if n := PruneShards(root); n != 2 {
		t.Errorf("pruned %d shards, want 2", n)
	}
	if _, err := os.Stat(metadataOnly); err == nil {
		t.Error("shard holding only Finder metadata survived")
	}
	if _, err := os.Stat(filepath.Join(root, "cd")); err != nil {
		t.Error("shard holding a build directory was pruned")
	}
}

func TestLeftoversAndPending(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a"), Origin: "a"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	left := Leftovers(root)
	if len(left) != 1 || left[0] != st.Dir {
		t.Fatalf("Leftovers = %v, want [%s]", left, st.Dir)
	}
	// The manifest is bookkeeping, not an item waiting to be deleted.
	if n := Pending(st.Dir); n != 1 {
		t.Errorf("Pending = %d, want 1", n)
	}
}

func TestRotateTrimsAnOverlongLog(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "mater.log")

	log, err := OpenLog(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < maxLogLines+50; i++ {
		log.Printf("line %d", i)
	}
	log.Close()

	Rotate(path)
	if got := len(Tail(path, maxLogLines*2)); got > maxLogLines {
		t.Errorf("log still has %d lines after Rotate, want at most %d", got, maxLogLines)
	}
}
