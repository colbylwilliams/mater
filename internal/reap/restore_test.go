package reap

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// The manifest has to carry absolute origins: a restore that only knew a
// display-shortened path could not put anything back.
func TestManifestRecordsAbsoluteOrigins(t *testing.T) {
	root := t.TempDir()
	src := victim(t, root, "a")

	st, err := Stage(root, []Entry{{Source: src}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	rows := ReadManifest(st.Dir)
	if len(rows) != 1 {
		t.Fatalf("manifest has %d rows, want 1", len(rows))
	}
	if rows[0].Origin != src {
		t.Errorf("Origin = %q, want the absolute source %q", rows[0].Origin, src)
	}
}

func TestRecoverableSplitsReadyFromBlocked(t *testing.T) {
	root := t.TempDir()
	gone := victim(t, root, "gone")
	retaken := victim(t, root, "retaken")

	st, err := Stage(root, []Entry{{Source: gone}, {Source: retaken}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	// A build ran after the delete and recreated one of the paths.
	if err := os.MkdirAll(retaken, 0o755); err != nil {
		t.Fatal(err)
	}

	ready, blocked := Recoverable(st.Dir)
	if len(ready) != 1 || ready[0].Origin != gone {
		t.Errorf("ready = %+v, want just %q", ready, gone)
	}
	if len(blocked) != 1 || blocked[0].Origin != retaken {
		t.Errorf("blocked = %+v, want just %q", blocked, retaken)
	}
}

func TestRestorePutsItemsBack(t *testing.T) {
	root := t.TempDir()
	src := victim(t, root, "nested/deeply/a")

	st, err := Stage(root, []Entry{{Source: src}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(src); err == nil {
		t.Fatal("source still present after staging")
	}

	// The parent chain may have been cleaned up, so restore has to recreate it.
	if err := os.RemoveAll(filepath.Join(root, "nested")); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	ready, _ := Recoverable(st.Dir)
	n, err := Restore(ready, log)
	log.Close()
	if err != nil {
		t.Fatalf("Restore: %v", err)
	}
	if n != 1 {
		t.Fatalf("restored %d items, want 1", n)
	}
	if _, err := os.Stat(filepath.Join(src, "deep", "artifact")); err != nil {
		t.Errorf("restored tree is incomplete: %v", err)
	}
}

// Restore must never overwrite output that a build produced after the delete.
func TestRestoreLeavesAReoccupiedPathAlone(t *testing.T) {
	root := t.TempDir()
	src := victim(t, root, "a")

	st, err := Stage(root, []Entry{{Source: src}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	if err := os.MkdirAll(src, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(src, "fresh"), []byte("new"), 0o644); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	ready, blocked := Recoverable(st.Dir)
	if len(ready) != 0 {
		t.Errorf("ready = %+v, want none", ready)
	}
	if len(blocked) != 1 {
		t.Errorf("blocked = %+v, want one", blocked)
	}
	if _, err := Restore(ready, log); err != nil {
		t.Fatal(err)
	}
	log.Close()

	if _, err := os.Stat(filepath.Join(src, "fresh")); err != nil {
		t.Error("Restore clobbered output produced after the delete")
	}
}

func TestStopHaltsTheWorkerBeforeAnyItem(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{
		{Source: victim(t, root, "a")},
		{Source: victim(t, root, "b")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}
	if err := Stop(st.Dir); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	if err := Run(context.Background(), st.Dir, log); err != nil {
		t.Fatalf("Run: %v", err)
	}
	log.Close()

	if !Stopped(st.Dir) {
		t.Error("stop sentinel was cleared by a stopped run")
	}
	if n := Pending(st.Dir); n != 2 {
		t.Errorf("Pending = %d, want 2 — a stopped worker deleted something", n)
	}
	if ready, _ := Recoverable(st.Dir); len(ready) != 2 {
		t.Errorf("recoverable = %d, want 2", len(ready))
	}
}

func TestCancelledContextStopsTheWorker(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	if err := Run(ctx, st.Dir, log); err != nil {
		t.Fatalf("Run: %v", err)
	}
	log.Close()

	if n := Pending(st.Dir); n != 1 {
		t.Errorf("Pending = %d, want 1 — a cancelled worker deleted something", n)
	}
}

// Adopting must preserve each item's origin; nesting a whole directory would
// lose it, and with it any chance of restoring what it held.
func TestAdoptPreservesOrigins(t *testing.T) {
	root := t.TempDir()
	orphaned := victim(t, root, "orphaned")

	abandoned, err := Stage(root, []Entry{{Source: orphaned}})
	if err != nil {
		t.Fatal(err)
	}
	if err := abandoned.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	current, err := Stage(root, []Entry{{Source: victim(t, root, "current")}})
	if err != nil {
		t.Fatal(err)
	}
	if n := current.Adopt(root); n != 1 {
		t.Fatalf("adopted %d items, want 1", n)
	}
	if err := current.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	if _, err := os.Stat(abandoned.Dir); err == nil {
		t.Error("abandoned staging directory survived adoption")
	}

	var found bool
	for _, e := range ReadManifest(current.Dir) {
		if e.Origin == orphaned {
			found = true
		}
	}
	if !found {
		t.Errorf("adopted item lost its origin %q", orphaned)
	}

	ready, _ := Recoverable(current.Dir)
	if len(ready) != 2 {
		t.Errorf("recoverable = %d, want 2 — the adopted item is not restorable", len(ready))
	}
}

func TestCleanupRemovesDrainedStaging(t *testing.T) {
	root := t.TempDir()
	st, err := Stage(root, []Entry{{Source: victim(t, root, "a")}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	ready, _ := Recoverable(st.Dir)
	if _, err := Restore(ready, log); err != nil {
		t.Fatal(err)
	}
	log.Close()

	if err := Cleanup(st.Dir); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(st.Dir); err == nil {
		t.Error("Cleanup left an empty staging directory behind")
	}
}

// Items that could not be restored stay staged, and the manifest has to be
// rewritten to match so the next worker still knows where they came from.
func TestCleanupRewritesManifestWhenItemsRemain(t *testing.T) {
	root := t.TempDir()
	keep := victim(t, root, "keep")
	drop := victim(t, root, "drop")

	st, err := Stage(root, []Entry{{Source: keep}, {Source: drop}})
	if err != nil {
		t.Fatal(err)
	}
	if err := st.WriteManifest(); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}

	log, _ := OpenLog(filepath.Join(root, "mater.log"))
	ready, _ := Recoverable(st.Dir)
	if _, err := Restore(ready, log); err != nil {
		t.Fatal(err)
	}
	log.Close()

	if err := Cleanup(st.Dir); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	rows := ReadManifest(st.Dir)
	if len(rows) != 1 {
		t.Fatalf("manifest has %d rows, want 1", len(rows))
	}
	if rows[0].Origin != keep {
		t.Errorf("remaining row origin = %q, want %q", rows[0].Origin, keep)
	}
}
