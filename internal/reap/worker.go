package reap

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// Run unlinks everything in a staging directory, one item at a time so the log
// shows real progress. Sizes come from the manifest, so no tree is walked twice.
//
// The stop sentinel and the context are both checked between items, never
// during one: a delete that is abandoned part-way through a tree would leave
// rubble that is neither restorable nor accounted for.
func Run(ctx context.Context, dir string, log *Log) error {
	if _, err := os.Stat(dir); err != nil {
		return nil
	}

	entries := ReadManifest(dir)
	start := time.Now()
	log.Printf("delete started: %d item%s — %s", len(entries), plural(len(entries)), dir)

	var freed int64
	removed := 0
	for _, e := range entries {
		if Stopped(dir) {
			log.Printf("delete stopped after %d of %d item%s — %s remain recoverable",
				removed, len(entries), plural(len(entries)), shorten(dir))
			return nil
		}
		select {
		case <-ctx.Done():
			log.Printf("delete interrupted after %d of %d item%s", removed, len(entries), plural(len(entries)))
			return nil
		default:
		}

		target := filepath.Join(dir, e.Slot)
		if _, err := os.Stat(target); err != nil {
			continue
		}
		t0 := time.Now()
		if err := os.RemoveAll(target); err != nil {
			log.Printf("  failed: %s — %v", shorten(e.Origin), err)
			continue
		}
		freed += e.Size
		removed++
		log.Printf("  [%d/%d] %8s  %s  (%s)", removed, len(entries),
			formatSize(e.Size), shorten(e.Origin), time.Since(t0).Round(time.Second))
	}

	os.Remove(filepath.Join(dir, ManifestName))
	os.Remove(filepath.Join(dir, StopFile))
	// Anything staged without a manifest row still has to go.
	if err := os.RemoveAll(dir); err != nil {
		return fmt.Errorf("remove staging directory: %w", err)
	}

	log.Printf("delete finished: %s in %d item%s, %s",
		formatSize(freed), removed, plural(removed), time.Since(start).Round(time.Second))
	return nil
}

// PruneShards removes the two-character shard directories once their build
// directories are gone.
//
// Finder leaves a .DS_Store in any directory it displays, so a shard whose
// build directory was reaped still holds a hidden file and a plain remove
// refuses it — the shard then looks empty but lingers forever. The metadata is
// dropped only when nothing real remains, and the remove that follows still
// refuses if a concurrent build just recreated something.
func PruneShards(buildRoot string) int {
	shards, err := os.ReadDir(buildRoot)
	if err != nil {
		return 0
	}

	pruned := 0
	for _, shard := range shards {
		if !shard.IsDir() || strings.HasPrefix(shard.Name(), ".") {
			continue
		}
		dir := filepath.Join(buildRoot, shard.Name())
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}

		onlyMetadata := true
		for _, e := range entries {
			if !isFinderMetadata(e.Name()) {
				onlyMetadata = false
				break
			}
		}
		if !onlyMetadata {
			continue
		}
		for _, e := range entries {
			os.Remove(filepath.Join(dir, e.Name()))
		}
		if os.Remove(dir) == nil {
			pruned++
		}
	}
	return pruned
}

func isFinderMetadata(name string) bool {
	return name == ".DS_Store" || strings.HasPrefix(name, "._")
}

// Worker is a delete running in a process of its own.
type Worker struct {
	proc *os.Process
}

// Spawn starts a detached worker for dir. globalFlags are forwarded so the
// worker resolves the same configuration as its parent; without them it would
// log to, and prune shards under, the default paths instead.
//
// Setsid puts the child in its own session so it survives the terminal that
// launched it, which is the whole point of staging: the caller need not stay.
// A caller that does stay to Wait is still only watching. An interrupt is
// delivered to the terminal's foreground process group, which the worker has
// left, so stopping the watch never stops the delete.
func Spawn(dir string, globalFlags ...string) (*Worker, error) {
	self, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate mater binary: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(self, append([]string{"reap", dir}, globalFlags...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start background delete: %w", err)
	}
	return &Worker{proc: cmd.Process}, nil
}

// Wait blocks until the worker exits and reports whether it ran to a clean
// end. A worker that restore stops between items has ended cleanly too; only
// the log tells the two apart.
func (w *Worker) Wait() error {
	state, err := w.proc.Wait()
	if err != nil {
		return fmt.Errorf("wait for background delete: %w", err)
	}
	if !state.Success() {
		return fmt.Errorf("background delete ended with %s", state)
	}
	return nil
}

// Release lets the worker run on unobserved. It is never waited on, and needs
// no waiting: once this process exits, init adopts the worker and reaps it.
func (w *Worker) Release() error {
	return w.proc.Release()
}

func plural(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}

// formatSize is duplicated from the display layer so the worker, which runs
// with no terminal attached, does not depend on it.
func formatSize(b int64) string {
	const (
		kb = 1 << 10
		mb = 1 << 20
		gb = 1 << 30
		tb = 1 << 40
	)
	switch {
	case b >= tb:
		return fmt.Sprintf("%.1fT", float64(b)/tb)
	case b >= gb:
		return fmt.Sprintf("%.1fG", float64(b)/gb)
	case b >= mb:
		return fmt.Sprintf("%.0fM", float64(b)/mb)
	case b >= kb:
		return fmt.Sprintf("%.0fK", float64(b)/kb)
	default:
		return fmt.Sprintf("%dB", b)
	}
}
