package cargo

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
)

// probeMarker identifies a manifest this package wrote. Nothing is ever removed
// unless the marker is still present, so a worktree that reappears mid-probe is
// left untouched.
const probeMarker = "mater-probe"

const probeManifest = `[package]
name = "` + probeMarker + `"
version = "0.0.0"
edition = "2021"

[lib]
path = "lib.rs"
`

// Probe reports the build directory a now-deleted workspace owned.
//
// The build-dir hash is a pure function of the workspace path, but Cargo
// canonicalises that path before hashing, so the only way to ask about a path
// is for a manifest to exist there. A stub is materialised, queried, and taken
// back. Probe never writes to a path that already exists, never removes a file
// it did not write, and gives back any parent directories it had to create.
func Probe(ctx context.Context, dir string) (string, bool) {
	if dir == "" || !filepath.IsAbs(dir) {
		return "", false
	}
	if _, err := os.Lstat(dir); err == nil {
		return "", false // a live path is never probed
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", false
	}

	// A removed worktree root can leave the whole chain missing, so every
	// parent created here is remembered and returned in reverse order.
	var created []string
	p := dir
	for {
		parent := filepath.Dir(p)
		if parent == p || parent == "/" || parent == home || parent == "." {
			return "", false
		}
		if _, err := os.Stat(parent); err == nil {
			break
		}
		p = parent
		created = append([]string{p}, created...)
	}

	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", false
	}

	manifest := filepath.Join(dir, "Cargo.toml")
	lib := filepath.Join(dir, "lib.rs")
	defer func() {
		// Only reclaim the stub while it is still ours.
		if data, err := os.ReadFile(manifest); err == nil && strings.Contains(string(data), probeMarker) {
			os.Remove(manifest)
			os.Remove(lib)
			os.Remove(filepath.Join(dir, "Cargo.lock"))
		}
		// Remove rather than RemoveAll: a directory that gained real content
		// mid-probe stays, along with every parent above it.
		if os.Remove(dir) != nil {
			return
		}
		for i := len(created) - 1; i >= 0; i-- {
			if os.Remove(created[i]) != nil {
				return
			}
		}
	}()

	if err := os.WriteFile(manifest, []byte(probeManifest), 0o644); err != nil {
		return "", false
	}
	if err := os.WriteFile(lib, nil, 0o644); err != nil {
		return "", false
	}

	// A stub inside a live workspace whose members cover it is answered for
	// that workspace, and the build directory reported is the live one's. Only
	// an answer for the stub's own workspace says anything about dir.
	m, ok := readMetadata(ctx, dir)
	if !ok || !sameDir(m.WorkspaceRoot, dir) {
		return "", false
	}
	return m.buildDir()
}

// sameDir compares two directories as Cargo sees them, which is with symlinks
// resolved.
func sameDir(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	resolve := func(p string) string {
		if r, err := filepath.EvalSymlinks(p); err == nil {
			return r
		}
		return filepath.Clean(p)
	}
	return resolve(a) == resolve(b)
}

// ProbeAll probes dirs concurrently and reports what each one resolved to.
//
// Probes that need the same missing directory recreated run one after another.
// Run together, the first to finish cannot remove a parent another is still
// inside, and the other found it already there and never owned it, so it would
// be left behind. Probes in unrelated trees still run at once.
func ProbeAll(ctx context.Context, dirs []string) []Mapping {
	if len(dirs) == 0 {
		return nil
	}

	groups := map[string][]string{}
	var order []string
	for _, d := range dirs {
		top := topMissing(d)
		if _, ok := groups[top]; !ok {
			order = append(order, top)
		}
		groups[top] = append(groups[top], d)
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []Mapping
		ch  = make(chan []string)
	)

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for group := range ch {
				for _, d := range group {
					if ctx.Err() != nil {
						break
					}
					bd, ok := Probe(ctx, d)
					if !ok {
						continue
					}
					mu.Lock()
					out = append(out, Mapping{Workspace: d, BuildDir: bd})
					mu.Unlock()
				}
			}
		}()
	}
	for _, top := range order {
		select {
		case ch <- groups[top]:
		case <-ctx.Done():
		}
	}
	close(ch)
	wg.Wait()
	return out
}

// topMissing is the highest directory a probe of dir would have to create, or
// dir itself when its parent exists. Two probes can only collide when this is
// the same for both.
func topMissing(dir string) string {
	for {
		parent := filepath.Dir(dir)
		if parent == dir || exists(parent) {
			return dir
		}
		dir = parent
	}
}
