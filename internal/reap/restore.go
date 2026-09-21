package reap

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

// Staged is one item sitting in staging, together with where it came from.
type Staged struct {
	Entry
	Dir  string // the staging directory holding it
	Path string // where the item currently sits
}

// Recoverable sorts what is still in staging into what can be put back and what
// cannot.
//
// An item is recoverable while three things hold: it is still staged, the
// manifest says where it came from, and nothing has since taken that path. The
// last matters because a build that ran after the delete will have recreated
// the directory, and the fresh output is the one to keep.
func Recoverable(dir string) (ready, blocked []Staged) {
	for _, e := range ReadManifest(dir) {
		s := Staged{Entry: e, Dir: dir, Path: filepath.Join(dir, e.Slot)}
		if _, err := os.Stat(s.Path); err != nil {
			continue // the worker already deleted it
		}
		if e.Origin == "" || !filepath.IsAbs(e.Origin) {
			blocked = append(blocked, s)
			continue
		}
		if _, err := os.Lstat(e.Origin); err == nil {
			blocked = append(blocked, s)
			continue
		}
		ready = append(ready, s)
	}
	sort.Slice(ready, func(i, j int) bool { return ready[i].Origin < ready[j].Origin })
	sort.Slice(blocked, func(i, j int) bool { return blocked[i].Slot < blocked[j].Slot })
	return ready, blocked
}

// Stop asks a worker to stop before its next item.
//
// Signalling through the filesystem rather than the process table makes the
// stop race-free: the worker only ever checks between whole items, so it is
// never interrupted part-way through one and never leaves a half-deleted tree.
// The sentinel is left in place, which also stops a worker that has not started
// yet and so closes the window between staging and spawn.
func Stop(dir string) error {
	f, err := os.Create(filepath.Join(dir, StopFile))
	if err != nil {
		return fmt.Errorf("signal the worker to stop: %w", err)
	}
	return f.Close()
}

// Stopped reports whether a stop has been requested for a staging directory.
func Stopped(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, StopFile))
	return err == nil
}

// Restore moves items back where they came from. Each move is guarded
// individually, so one failure does not strand the rest.
func Restore(items []Staged, log *Log) (int, error) {
	restored := 0
	for _, s := range items {
		if err := os.MkdirAll(filepath.Dir(s.Origin), 0o755); err != nil {
			return restored, fmt.Errorf("recreate %s: %w", filepath.Dir(s.Origin), err)
		}
		// Re-checked immediately before the move: a build may have recreated
		// the path while earlier items were being restored.
		if _, err := os.Lstat(s.Origin); err == nil {
			continue
		}
		if err := os.Rename(s.Path, s.Origin); err != nil {
			return restored, fmt.Errorf("restore %s: %w", s.Origin, err)
		}
		log.Printf("  restored %s", shorten(s.Origin))
		restored++
	}
	return restored, nil
}

// Cleanup removes a staging directory once nothing is left in it, and otherwise
// rewrites the manifest to match what actually remains.
func Cleanup(dir string) error {
	if Pending(dir) == 0 {
		os.Remove(filepath.Join(dir, ManifestName))
		os.Remove(filepath.Join(dir, StopFile))
		if err := os.Remove(dir); err != nil {
			return fmt.Errorf("remove staging directory: %w", err)
		}
		return nil
	}

	var remaining []Entry
	for _, e := range ReadManifest(dir) {
		if _, err := os.Stat(filepath.Join(dir, e.Slot)); err == nil {
			remaining = append(remaining, e)
		}
	}
	st := &Staging{Dir: dir, Entries: remaining, next: len(remaining)}
	return st.WriteManifest()
}

// shorten abbreviates a home-relative path for the log. It is kept here rather
// than imported so the worker, which runs with no terminal attached, stays
// independent of the display layer.
func shorten(p string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return p
	}
	if p == home {
		return "~"
	}
	if len(p) > len(home) && p[:len(home)] == home && p[len(home)] == filepath.Separator {
		return "~" + p[len(home):]
	}
	return p
}
