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

// Spawn starts a detached worker for dir. globalFlags are forwarded so the
// worker resolves the same configuration as its parent; without them it would
// log to, and prune shards under, the default paths instead.
//
// Setsid puts the child in its own session so it survives the terminal that
// launched it, which is the whole point of staging: the caller returns
// immediately.
func Spawn(dir string, globalFlags ...string) error {
	self, err := os.Executable()
	if err != nil {
		return fmt.Errorf("locate mater binary: %w", err)
	}

	devNull, err := os.OpenFile(os.DevNull, os.O_RDWR, 0)
	if err != nil {
		return fmt.Errorf("open %s: %w", os.DevNull, err)
	}
	defer devNull.Close()

	cmd := exec.Command(self, append([]string{"reap", dir}, globalFlags...)...)
	cmd.Stdin, cmd.Stdout, cmd.Stderr = devNull, devNull, devNull
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start background delete: %w", err)
	}
	// The child is deliberately not waited on; releasing it hands the process
	// to init rather than leaving a zombie when this process exits.
	return cmd.Process.Release()
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
	)
	switch {
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
