// Package inuse detects build output that a live process is relying on.
//
// Age alone is not proof that output is idle: an app may have been built days
// before it was launched, and it still executes from its own target directory
// and serves from its own node_modules. Two independent signals are taken from
// a single snapshot of the process table — a running binary names its own path
// in argv, and a test harness instead parks its working directory inside the
// tree.
package inuse

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// Snapshot is one view of what every process is doing.
type Snapshot struct {
	args string
	cwds map[string]struct{}
}

// Take collects process arguments and working directories concurrently. Either
// probe may be unavailable; a missing signal narrows detection rather than
// failing the run.
func Take(ctx context.Context) *Snapshot {
	s := &Snapshot{cwds: map[string]struct{}{}}

	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		out, err := exec.CommandContext(ctx, "ps", "-Ao", "args=").Output()
		if err != nil {
			return
		}
		s.args = string(out)
	}()

	go func() {
		defer wg.Done()
		// lsof exits non-zero when any single file cannot be examined, which is
		// routine on a multi-user machine, so its output is used regardless.
		out, _ := exec.CommandContext(ctx, "lsof", "-a", "-d", "cwd", "-F", "n").Output()
		sc := bufio.NewScanner(bytes.NewReader(out))
		sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
		for sc.Scan() {
			line := sc.Text()
			if strings.HasPrefix(line, "n/") {
				s.cwds[line[1:]] = struct{}{}
			}
		}
	}()

	wg.Wait()
	return s
}

// Check reports whether any process is executing from, or sitting inside, dir.
func (s *Snapshot) Check(dir string) bool {
	if s == nil || dir == "" {
		return false
	}

	candidates := []string{dir}
	// lsof reports physical paths, so the resolved form is compared too: /tmp
	// is a symlink to /private/tmp, and a repo root may itself be a symlink.
	if resolved, err := filepath.EvalSymlinks(dir); err == nil && resolved != dir {
		candidates = append(candidates, resolved)
	}

	for _, c := range candidates {
		// The trailing separator keeps /foo/target from matching
		// /foo/target-other.
		if s.args != "" && strings.Contains(s.args, c+string(filepath.Separator)) {
			return true
		}
		if _, ok := s.cwds[c]; ok {
			return true
		}
		prefix := c + string(filepath.Separator)
		for cwd := range s.cwds {
			if strings.HasPrefix(cwd, prefix) {
				return true
			}
		}
	}
	return false
}

// RustcRunning reports whether a compiler is live anywhere on the machine.
//
// Per-directory detection cannot see a rustc that starts moments from now, so a
// full nuke additionally refuses while any build is in flight. Operations that
// only read, or that only take directories whose workspace is already gone, are
// unaffected.
func RustcRunning(ctx context.Context) bool {
	if err := exec.CommandContext(ctx, "pgrep", "-qx", "rustc").Run(); err == nil {
		return true
	}
	return false
}
