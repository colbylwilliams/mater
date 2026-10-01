// Package cargo asks Cargo which build directory a workspace path maps to.
// The hash is a pure function of the canonicalised workspace path, and Cargo
// is the only thing that computes it, so every mapping here comes from Cargo
// itself rather than from a reimplementation that could drift.
package cargo

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"
)

type metadata struct {
	BuildDirectory  string `json:"build_directory"`
	TargetDirectory string `json:"target_directory"`
	WorkspaceRoot   string `json:"workspace_root"`
}

// Resolve reports the build directory Cargo would use for the workspace at
// dir. `--no-deps --offline` answers without building or touching Cargo.lock.
func Resolve(ctx context.Context, dir string) (string, bool) {
	m, ok := readMetadata(ctx, dir)
	if !ok {
		return "", false
	}
	return m.buildDir()
}

func readMetadata(ctx context.Context, dir string) (metadata, bool) {
	if _, err := os.Stat(filepath.Join(dir, "Cargo.toml")); err != nil {
		return metadata{}, false
	}

	cmd := exec.CommandContext(ctx, "cargo", "metadata", "--no-deps", "--format-version", "1", "--offline")
	cmd.Dir = dir
	// Every survey asks this of each Rust checkout, and rustup would otherwise
	// download any toolchain a checkout pins but this machine lacks, silently,
	// before Cargo answered. Such a checkout goes unanswered instead, as if it
	// were not a Rust checkout at all.
	cmd.Env = append(os.Environ(), "RUSTUP_AUTO_INSTALL=0")
	cmd.Stderr = nil
	out, err := cmd.Output()
	if err != nil {
		return metadata{}, false
	}

	var m metadata
	if err := json.Unmarshal(out, &m); err != nil {
		return metadata{}, false
	}
	return m, true
}

// buildDir prefers build_directory, the split-output layout this tool depends
// on; target_directory is the fallback for a Cargo that predates it.
func (m metadata) buildDir() (string, bool) {
	if m.BuildDirectory != "" {
		return m.BuildDirectory, true
	}
	if m.TargetDirectory != "" {
		return m.TargetDirectory, true
	}
	return "", false
}

// Mapping is one workspace and the build directory it owns.
type Mapping struct {
	Workspace string
	BuildDir  string
}

// ResolveAll resolves dirs concurrently. Cargo spends its time on I/O and
// parsing, so running several at once is several times faster than a loop.
func ResolveAll(ctx context.Context, dirs []string) []Mapping {
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
				bd, ok := Resolve(ctx, d)
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
