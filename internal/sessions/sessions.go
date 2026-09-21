// Package sessions attributes worktrees to the Copilot sessions that created
// them, so a build directory can be shown as recognisable work rather than as
// an opaque hash.
package sessions

import (
	"bufio"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// Session is one workspace.yaml, reduced to the fields that identify work.
type Session struct {
	Root      string // git_root, or cwd when git_root is absent
	Label     string // name, or summary for an unnamed session
	Repo      string
	Branch    string
	UpdatedAt string // RFC3339; compared lexically, which is valid for this format
}

// Set is worktree root -> most recently updated session for that root.
type Set struct {
	byRoot map[string]Session
	recent []Session // newest first
}

// Lookup returns the session that owns root.
func (s *Set) Lookup(root string) (Session, bool) {
	if s == nil {
		return Session{}, false
	}
	v, ok := s.byRoot[root]
	return v, ok
}

// Label names root for display, falling back to the directory's own name.
func (s *Set) Label(root string) string {
	if sess, ok := s.Lookup(root); ok && sess.Label != "" {
		return sess.Label
	}
	return filepath.Base(root)
}

// Recent lists every known session, newest first. Sessions outlive the
// worktrees they created, which is what makes a deleted path recoverable.
func (s *Set) Recent() []Session {
	if s == nil {
		return nil
	}
	return s.recent
}

// Len is the number of distinct worktree roots known.
func (s *Set) Len() int {
	if s == nil {
		return 0
	}
	return len(s.byRoot)
}

// Load reads every workspace.yaml under stateDir. Only six keys matter, so the
// files are scanned line by line; unmarshalling thousands of documents in full
// costs far more than it returns. A file that cannot be read is skipped rather
// than failing the run.
func Load(stateDir string) *Set {
	set := &Set{byRoot: map[string]Session{}}

	entries, err := os.ReadDir(stateDir)
	if err != nil {
		return set
	}

	paths := make([]string, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() {
			paths = append(paths, filepath.Join(stateDir, e.Name(), "workspace.yaml"))
		}
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out = make([]Session, 0, len(paths))
		ch  = make(chan string)
	)

	workers := runtime.NumCPU() * 4
	if workers > 64 {
		workers = 64
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for p := range ch {
				s, ok := parse(p)
				if !ok {
					continue
				}
				mu.Lock()
				out = append(out, s)
				mu.Unlock()
			}
		}()
	}
	for _, p := range paths {
		ch <- p
	}
	close(ch)
	wg.Wait()

	// Newest first, so the first entry seen for a root is the one that wins and
	// Recent() is already ordered for the bootstrap probe.
	sort.Slice(out, func(i, j int) bool {
		if out[i].UpdatedAt != out[j].UpdatedAt {
			return out[i].UpdatedAt > out[j].UpdatedAt
		}
		return out[i].Root < out[j].Root
	})

	set.recent = make([]Session, 0, len(out))
	for _, s := range out {
		if _, seen := set.byRoot[s.Root]; seen {
			continue
		}
		set.byRoot[s.Root] = s
		set.recent = append(set.recent, s)
	}
	return set
}

func parse(path string) (Session, bool) {
	f, err := os.Open(path)
	if err != nil {
		return Session{}, false
	}
	defer f.Close()

	var (
		s       Session
		cwd     string
		name    string
		summary string
	)

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 256<<10)
	for sc.Scan() {
		line := sc.Text()
		// Only top-level scalars are of interest, so an indented line belongs to
		// a nested block and is skipped.
		if line == "" || line[0] == ' ' || line[0] == '-' || line[0] == '#' {
			continue
		}
		key, val, ok := strings.Cut(line, ": ")
		if !ok {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		if val == "" {
			continue
		}
		switch key {
		case "git_root":
			s.Root = val
		case "cwd":
			cwd = val
		case "name":
			name = val
		case "summary":
			summary = val
		case "repository":
			s.Repo = val
		case "branch":
			s.Branch = val
		case "updated_at":
			s.UpdatedAt = val
		}
	}

	if s.Root == "" {
		s.Root = cwd
	}
	if name != "" {
		s.Label = name
	} else {
		s.Label = summary
	}
	if s.Root == "" {
		return Session{}, false
	}
	return s, true
}
