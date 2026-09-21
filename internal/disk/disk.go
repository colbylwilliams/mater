// Package disk measures how much space a directory tree actually occupies.
package disk

import (
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"syscall"
	"time"
)

// Usage is the result of walking one tree.
type Usage struct {
	Bytes   int64     // allocated blocks, matching what `du` reports
	Files   int64     //
	Newest  time.Time // most recent modification anywhere in the tree
	Partial bool      // some entries were unreadable or vanished mid-walk
}

// Size walks root and reports its on-disk usage. Allocated blocks are counted
// rather than apparent size, and a file reached through more than one link is
// counted once, so the number agrees with `du`.
func Size(root string) Usage {
	var (
		u    Usage
		seen = map[[2]uint64]struct{}{}
	)

	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			u.Partial = true
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		info, err := d.Info()
		if err != nil {
			u.Partial = true
			return nil
		}
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok {
			u.Bytes += info.Size()
			return nil
		}
		if st.Nlink > 1 && !d.IsDir() {
			key := [2]uint64{uint64(st.Dev), st.Ino}
			if _, dup := seen[key]; dup {
				return nil
			}
			seen[key] = struct{}{}
		}
		u.Bytes += st.Blocks * 512
		if !d.IsDir() {
			u.Files++
		}
		if m := info.ModTime(); m.After(u.Newest) {
			u.Newest = m
		}
		return nil
	})
	if err != nil {
		u.Partial = true
	}
	return u
}

// SizeAll measures roots concurrently. The work is I/O bound per tree, so
// overlapping several trees is far faster than walking them one at a time.
// progress, when non-nil, is called once per completed tree.
func SizeAll(roots []string, progress func(done, total int)) map[string]Usage {
	out := make(map[string]Usage, len(roots))
	if len(roots) == 0 {
		return out
	}

	var (
		mu   sync.Mutex
		wg   sync.WaitGroup
		ch   = make(chan string)
		done int
	)

	workers := runtime.NumCPU() * 2
	if workers > len(roots) {
		workers = len(roots)
	}
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for r := range ch {
				u := Size(r)
				mu.Lock()
				out[r] = u
				done++
				if progress != nil {
					progress(done, len(roots))
				}
				mu.Unlock()
			}
		}()
	}
	for _, r := range roots {
		ch <- r
	}
	close(ch)
	wg.Wait()
	return out
}

// LastBuilt reports when a Cargo build directory was last written to.
//
// A full walk would be far too slow to run across every directory just to sort
// by age, so only the handful of paths Cargo touches on every build are
// stat'ed: the rustc probe file, and each profile's fingerprint and incremental
// directories.
func LastBuilt(buildDir string) time.Time {
	var newest time.Time

	consider := func(p string) {
		if fi, err := os.Stat(p); err == nil && fi.ModTime().After(newest) {
			newest = fi.ModTime()
		}
	}

	consider(filepath.Join(buildDir, ".rustc_info.json"))

	entries, err := os.ReadDir(buildDir)
	if err != nil {
		return newest
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		profile := filepath.Join(buildDir, e.Name())
		consider(filepath.Join(profile, ".fingerprint"))
		consider(filepath.Join(profile, "incremental"))
	}

	// Nothing recognisable inside means the directory's own timestamp is the
	// best evidence available.
	if newest.IsZero() {
		consider(buildDir)
	}
	return newest
}
