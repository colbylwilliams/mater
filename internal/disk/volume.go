package disk

import (
	"os"
	"path/filepath"
	"syscall"
)

// Volume is a mounted filesystem and the room left on it, measured the way `df`
// measures it, just as Usage follows `du`.
type Volume struct {
	Mount string // where the filesystem is mounted
	Free  int64  // bytes an unprivileged process can still write; root's reserve is excluded
	Size  int64  // capacity of the filesystem
}

// Volumes reports each distinct volume holding paths, in the order the paths
// first reach it. A path that does not exist yet is looked up through its
// nearest existing parent, since that is the volume it would be created on. A
// volume that cannot be queried is left out rather than reported as empty.
//
// Volumes are told apart by device number. macOS gives its system and data
// volumes the same one, which is what a reader wants here: they draw on the
// same free space, so they are reported once.
func Volumes(paths ...string) []Volume {
	var (
		out  []Volume
		seen = map[uint64]bool{}
	)
	for _, p := range paths {
		if p == "" {
			continue
		}
		p, dev, ok := nearest(p)
		if !ok || seen[dev] {
			continue
		}
		v, err := statVolume(p)
		if err != nil {
			continue
		}
		seen[dev] = true
		out = append(out, v)
	}
	return out
}

// nearest returns path, or its closest ancestor that exists, along with the
// device holding it.
func nearest(path string) (string, uint64, bool) {
	for {
		if dev, ok := device(path); ok {
			return path, dev, true
		}
		parent := filepath.Dir(path)
		if parent == path {
			return "", 0, false
		}
		path = parent
	}
}

// mountPoint climbs from path to the highest directory still on the same
// device, since the mount boundary is exactly where the device number changes.
// It serves platforms whose statfs does not say where a filesystem is mounted.
// Symlinks are resolved first, so a link into another volume is not mistaken
// for that volume's root.
func mountPoint(path string) string {
	p, err := filepath.Abs(path)
	if err != nil {
		return path
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	dev, ok := device(p)
	if !ok {
		return p
	}
	for {
		parent := filepath.Dir(p)
		if parent == p {
			return p
		}
		if d, ok := device(parent); !ok || d != dev {
			return p
		}
		p = parent
	}
}

// device identifies the filesystem holding path, following symlinks the same
// way statfs does.
func device(path string) (uint64, bool) {
	fi, err := os.Stat(path)
	if err != nil {
		return 0, false
	}
	st, ok := fi.Sys().(*syscall.Stat_t)
	if !ok {
		return 0, false
	}
	return uint64(st.Dev), true
}
