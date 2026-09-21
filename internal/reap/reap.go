// Package reap removes staged build output.
//
// Deleting a large tree in place takes minutes and holds the terminal for all
// of them. Instead each victim is renamed into a staging directory on the same
// volume, which is an O(1) operation, and a detached worker unlinks it
// afterwards. The tree is free the moment staging completes.
//
// Staging deliberately lives inside the build root: that path is already
// excluded from real-time scanning, so the unlink churn of a mass delete is not
// scanned either.
//
// Staging is also what makes a delete reversible. Until the worker reaches an
// item it is still whole, and the manifest records where it came from, so a
// run can be stopped and put back. A worker can be killed outright, so each run
// additionally adopts staging left behind by previous runs.
package reap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/colbylwilliams/mater/internal/disk"
)

const (
	// ManifestName records what was staged and where each item came from. The
	// worker reads it so its log names real directories rather than opaque
	// slots, restore reads it to put things back, and neither has to walk a
	// tree that was already measured.
	ManifestName = ".manifest"

	// StopFile asks a running worker to stop before its next item. Signalling
	// through the filesystem rather than the process table means a stop is
	// race-free: the worker only ever checks between whole items, so it is
	// never interrupted part-way through one.
	StopFile = ".stop"

	// prefix marks a directory as mater staging.
	prefix = ".staged-"
)

// Entry is one staged item.
type Entry struct {
	Slot   string // name inside the staging directory
	Size   int64
	Origin string // absolute path it was moved from; empty when unknown
	Source string // path to move; not persisted
}

// Staging is a directory of items waiting to be unlinked.
type Staging struct {
	Dir     string
	Entries []Entry

	next int // next free slot number
}

// Total is the number of bytes staged.
func (s *Staging) Total() int64 {
	var n int64
	for _, e := range s.Entries {
		n += e.Size
	}
	return n
}

// Stage moves each entry's Source into a fresh staging directory under
// buildRoot. Anything that cannot be moved is left in place and dropped from
// the result. Sizes are not read here, so staging stays O(1) per item.
func Stage(buildRoot string, entries []Entry) (*Staging, error) {
	if err := os.MkdirAll(buildRoot, 0o755); err != nil {
		return nil, fmt.Errorf("create build root: %w", err)
	}
	// A unique directory per run, rather than one named after the process and
	// clock: two runs in the same second would otherwise share staging, and the
	// second would adopt the first's items out from under its worker.
	dir, err := os.MkdirTemp(buildRoot, prefix+"*")
	if err != nil {
		return nil, fmt.Errorf("create staging directory: %w", err)
	}

	st := &Staging{Dir: dir}
	for _, e := range entries {
		e.Slot = strconv.Itoa(st.next)
		if err := os.Rename(e.Source, filepath.Join(dir, e.Slot)); err != nil {
			continue
		}
		if e.Origin == "" {
			e.Origin = e.Source
		}
		st.Entries = append(st.Entries, e)
		st.next++
	}
	return st, nil
}

// Adopt takes over staging left behind by a run whose worker was killed, so
// those bytes are finished rather than accumulating forever.
//
// Items are merged one at a time rather than nested wholesale, which keeps each
// one's recorded origin intact. A nested directory would lose that, and with it
// any chance of restoring what it held.
func (s *Staging) Adopt(buildRoot string) int {
	adopted := 0

	for _, old := range Leftovers(buildRoot) {
		if old == s.Dir {
			continue
		}

		origins := map[string]Entry{}
		for _, e := range ReadManifest(old) {
			origins[e.Slot] = e
		}

		entries, err := os.ReadDir(old)
		if err != nil {
			continue
		}
		for _, de := range entries {
			if de.Name() == ManifestName || de.Name() == StopFile {
				continue
			}
			slot := strconv.Itoa(s.next)
			if err := os.Rename(filepath.Join(old, de.Name()), filepath.Join(s.Dir, slot)); err != nil {
				continue
			}
			prev := origins[de.Name()]
			s.Entries = append(s.Entries, Entry{Slot: slot, Size: prev.Size, Origin: prev.Origin})
			s.next++
			adopted++
		}

		os.Remove(filepath.Join(old, ManifestName))
		os.Remove(filepath.Join(old, StopFile))
		os.Remove(old)
	}
	return adopted
}

// Measure sizes the staged copies. Used on the --yes path, where staging runs
// first so the tree is freed before anything is walked.
func (s *Staging) Measure() {
	paths := make([]string, len(s.Entries))
	for i, e := range s.Entries {
		paths[i] = filepath.Join(s.Dir, e.Slot)
	}
	usage := disk.SizeAll(paths, nil)
	for i, e := range s.Entries {
		if u, ok := usage[filepath.Join(s.Dir, e.Slot)]; ok {
			s.Entries[i].Size = u.Bytes
		}
	}
}

// Discard removes an empty staging directory, so a run that moved nothing
// leaves no husk behind. It refuses once anything has been staged.
func (s *Staging) Discard() {
	if len(s.Entries) == 0 {
		os.Remove(s.Dir)
	}
}

// WriteManifest records what was staged, which is what drives the worker and
// what makes the run reversible.
func (s *Staging) WriteManifest() error {
	f, err := os.Create(filepath.Join(s.Dir, ManifestName))
	if err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	defer f.Close()

	w := bufio.NewWriter(f)
	for _, e := range s.Entries {
		fmt.Fprintf(w, "%s\t%d\t%s\n", e.Slot, e.Size, e.Origin)
	}
	if err := w.Flush(); err != nil {
		return fmt.Errorf("write manifest: %w", err)
	}
	return nil
}

// Leftovers lists staging directories present under buildRoot.
func Leftovers(buildRoot string) []string {
	entries, err := os.ReadDir(buildRoot)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		if e.IsDir() && strings.HasPrefix(e.Name(), prefix) {
			out = append(out, filepath.Join(buildRoot, e.Name()))
		}
	}
	sort.Strings(out)
	return out
}

// Pending counts the items still waiting inside a staging directory.
func Pending(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if e.Name() != ManifestName && e.Name() != StopFile {
			n++
		}
	}
	return n
}

// ReadManifest loads what WriteManifest recorded.
func ReadManifest(dir string) []Entry {
	f, err := os.Open(filepath.Join(dir, ManifestName))
	if err != nil {
		return nil
	}
	defer f.Close()

	var out []Entry
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
	for sc.Scan() {
		parts := strings.SplitN(sc.Text(), "\t", 3)
		if len(parts) != 3 {
			continue
		}
		size, _ := strconv.ParseInt(parts[1], 10, 64)
		out = append(out, Entry{Slot: parts[0], Size: size, Origin: parts[2]})
	}
	return out
}
