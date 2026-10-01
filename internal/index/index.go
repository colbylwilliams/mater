// Package index records which workspace produced each build directory.
//
// Nothing inside a build directory names the workspace it belongs to, so the
// link is observed while the workspace still exists and written down, or
// recovered afterwards by proving which deleted path Cargo maps to it. That
// record is what lets a build directory later be called an orphan: only a
// directory the index attributes to a path that no longer exists qualifies, so
// output that cannot be attributed is never collectable.
package index

import (
	"bufio"
	"bytes"
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

	// changed holds every build directory set or forgotten since the index was
	// loaded or last saved. Only these are laid over the file on save: for the
	// rest, the file is at least as current as what was loaded.
	changed map[string]struct{}
}

// New returns an empty index bound to path.
func New(path string) *Index {
	return &Index{path: path, byDir: map[string]string{}, changed: map[string]struct{}{}}
}

// Load reads the index file. A missing file yields an empty index, which is the
// correct state on a first run.
func Load(path string) (*Index, error) {
	data, err := read(path)
	if err != nil {
		return nil, err
	}
	ix := New(path)
	return ix, parse(data, ix.byDir)
}

// read returns the index file's content, which is nothing before a first save.
func read(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read index %s: %w", path, err)
	}
	return data, nil
}

// parse adds every well-formed line of an index file to byDir.
func parse(data []byte, byDir map[string]string) error {
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
	for sc.Scan() {
		dir, ws, ok := strings.Cut(sc.Text(), "\t")
		if !ok || dir == "" || ws == "" {
			continue
		}
		byDir[dir] = ws
	}
	return sc.Err()
}

// Workspace returns the workspace recorded for a build directory.
func (ix *Index) Workspace(buildDir string) (string, bool) {
	ws, ok := ix.byDir[buildDir]
	return ws, ok
}

// Set records a mapping.
func (ix *Index) Set(buildDir, workspace string) {
	ix.byDir[buildDir] = workspace
	ix.changed[buildDir] = struct{}{}
}

// Forget drops a mapping.
func (ix *Index) Forget(buildDir string) {
	delete(ix.byDir, buildDir)
	ix.changed[buildDir] = struct{}{}
}

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

// Save merges the index into whatever the file holds now and writes the result
// atomically, keeping only build directories that still exist. The existence
// check is repeated here because a run that staged its victims has already
// moved them by the time it persists.
//
// Every command records what its survey learned, and runs overlap: a prune can
// sit at its prompt while another run records a workspace built meanwhile.
// Replacing the file wholesale would erase that mapping, so only what this run
// set or forgot is laid over the file as it stands now. A save that would
// leave the file as it is writes nothing, so a read never creates the build
// root on a machine that has never built.
func (ix *Index) Save() error {
	if ix.path == "" {
		return nil
	}

	current, err := read(ix.path)
	if err != nil {
		return err
	}
	merged := map[string]string{}
	if err := parse(current, merged); err != nil {
		return fmt.Errorf("read index %s: %w", ix.path, err)
	}
	for d := range ix.changed {
		if w, ok := ix.byDir[d]; ok {
			merged[d] = w
		} else {
			delete(merged, d)
		}
	}
	for d := range merged {
		if _, err := os.Stat(d); err != nil {
			delete(merged, d)
		}
	}
	ix.byDir = merged

	var buf bytes.Buffer
	for _, e := range ix.Entries() {
		fmt.Fprintf(&buf, "%s\t%s\n", e.BuildDir, e.Workspace)
	}
	if bytes.Equal(buf.Bytes(), current) {
		clear(ix.changed)
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

	if _, err := tmp.Write(buf.Bytes()); err != nil {
		tmp.Close()
		return fmt.Errorf("write index: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("write index: %w", err)
	}
	if err := os.Rename(tmp.Name(), ix.path); err != nil {
		return fmt.Errorf("replace index: %w", err)
	}
	clear(ix.changed)
	return nil
}
