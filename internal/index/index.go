// Package index records which workspace produced each build directory.
//
// Nothing inside a build directory names the workspace it belongs to, so the
// link has to be observed while the workspace still exists and written down.
// That record is what lets a build directory later be called an orphan: only a
// directory the index attributes to a path that no longer exists qualifies, so
// output from an unscanned repo is never collectable.
package index

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Index is build directory -> workspace root.
type Index struct {
	path  string
	byDir map[string]string
}

// New returns an empty index bound to path.
func New(path string) *Index {
	return &Index{path: path, byDir: map[string]string{}}
}

// Load reads the index file. A missing file yields an empty index, which is the
// correct state on a first run.
func Load(path string) (*Index, error) {
	ix := New(path)

	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return ix, nil
		}
		return nil, fmt.Errorf("read index %s: %w", path, err)
	}
	defer f.Close()

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
	for sc.Scan() {
		dir, ws, ok := strings.Cut(sc.Text(), "\t")
		if !ok || dir == "" || ws == "" {
			continue
		}
		ix.byDir[dir] = ws
	}
	return ix, sc.Err()
}

// Workspace returns the workspace recorded for a build directory.
func (ix *Index) Workspace(buildDir string) (string, bool) {
	ws, ok := ix.byDir[buildDir]
	return ws, ok
}

// Set records a mapping.
func (ix *Index) Set(buildDir, workspace string) { ix.byDir[buildDir] = workspace }

// Forget drops a mapping.
func (ix *Index) Forget(buildDir string) { delete(ix.byDir, buildDir) }

// Len is the number of recorded mappings.
func (ix *Index) Len() int { return len(ix.byDir) }

// Entries lists every mapping, ordered by build directory.
func (ix *Index) Entries() []Entry {
	out := make([]Entry, 0, len(ix.byDir))
	for d, w := range ix.byDir {
		out = append(out, Entry{BuildDir: d, Workspace: w})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].BuildDir < out[j].BuildDir })
	return out
}

// Entry is one recorded mapping.
type Entry struct {
	BuildDir  string
	Workspace string
}

// Compact drops mappings whose build directory is gone. A directory that has
// been deleted carries no information worth keeping, and leaving the row would
// make a future directory at the same hash inherit a stale workspace.
func (ix *Index) Compact() {
	for d := range ix.byDir {
		if _, err := os.Stat(d); err != nil {
			delete(ix.byDir, d)
		}
	}
}

// Save writes the index atomically, skipping build directories that no longer
// exist. The existence check is repeated here because a run that staged its
// victims has already moved them by the time it persists.
func (ix *Index) Save() error {
	if ix.path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(ix.path), 0o755); err != nil {
		return fmt.Errorf("create index directory: %w", err)
	}

	tmp, err := os.CreateTemp(filepath.Dir(ix.path), ".index-*")
	if err != nil {
		return fmt.Errorf("stage index: %w", err)
	}
	defer os.Remove(tmp.Name())

	w := bufio.NewWriter(tmp)
	for _, e := range ix.Entries() {
		if _, err := os.Stat(e.BuildDir); err != nil {
			continue
		}
		if _, err := fmt.Fprintf(w, "%s\t%s\n", e.BuildDir, e.Workspace); err != nil {
			tmp.Close()
			return fmt.Errorf("write index: %w", err)
		}
	}
	if err := w.Flush(); err != nil {
		tmp.Close()
		return fmt.Errorf("write index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	if err := os.Rename(tmp.Name(), ix.path); err != nil {
		return fmt.Errorf("replace index: %w", err)
	}
	return nil
}
