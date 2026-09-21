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

	return Resolve(ctx, dir)
}

// ProbeAll probes dirs concurrently and reports what each one resolved to.
func ProbeAll(ctx context.Context, dirs []string) []Mapping {
	if len(dirs) == 0 {
		return nil
	}

	var (
		mu  sync.Mutex
		wg  sync.WaitGroup
		out []Mapping
		ch  = make(chan string)
	)

	workers := runtime.NumCPU()
	if workers > 8 {
		workers = 8
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for d := range ch {
				bd, ok := Probe(ctx, d)
				if !ok {
					continue
				}
				mu.Lock()
				out = append(out, Mapping{Workspace: d, BuildDir: bd})
				mu.Unlock()
			}
		}()
	}
	for _, d := range dirs {
		select {
		case ch <- d:
		case <-ctx.Done():
		}
	}
	close(ch)
	wg.Wait()
	return out
}
