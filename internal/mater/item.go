package mater

import (
	"path/filepath"
	"time"
)

// Kind distinguishes the sorts of build output mater reclaims.
type Kind int

const (
	// KindBuildDir is Cargo intermediate output under the build root. This is
	// the bulk of the bytes and the reason the build root exists as a single
	// stable path that can be excluded from real-time scanning.
	KindBuildDir Kind = iota
	// KindTarget is a target/ directory still sitting inside a checkout.
	KindTarget
	// KindNodeModules is an installed npm tree.
	KindNodeModules
)

func (k Kind) String() string {
	switch k {
	case KindBuildDir:
		return "build"
	case KindTarget:
		return "target"
	case KindNodeModules:
		return "node_modules"
	default:
		return "unknown"
	}
}

// State is what is known about the workspace behind an item.
type State int

const (
	// StateLive means the workspace that produced the output still exists.
	StateLive State = iota
	// StateOrphan means the index attributes the output to a workspace that has
	// since been deleted, so nothing will ever use it again.
	StateOrphan
	// StateUnknown means no workspace was ever recorded for the output. An
	// unknown item is never treated as an orphan, which is what keeps output
	// from an unscanned repo out of reach of `prune`.
	StateUnknown
)

func (s State) String() string {
	switch s {
	case StateLive:
		return "live"
	case StateOrphan:
		return "orphan"
	default:
		return "unknown"
	}
}

// Item is one reclaimable directory together with everything known about it.
type Item struct {
	Path      string
	Kind      Kind
	State     State
	Workspace string // owning checkout, empty when unattributed
	Session   string // human label for the workspace
	Repo      string
	Branch    string
	LastBuilt time.Time
	InUse     bool
	Size      int64
	Sized     bool
}

// Title is what a person recognises the item by: the work that produced it,
// falling back to the directory name when no session claims it.
func (i Item) Title() string {
	switch {
	case i.Session != "":
		return i.Session
	case i.Workspace != "":
		return filepath.Base(i.Workspace)
	case i.Kind == KindBuildDir:
		return "(unattributed)"
	default:
		return filepath.Base(filepath.Dir(i.Path))
	}
}

// Detail is the second line of a two-line rendering: enough path to act on.
func (i Item) Detail() string {
	if i.Kind == KindBuildDir {
		// The owning workspace is already named in the adjacent column, and the
		// hash path is what identifies the directory on disk.
		return ShortPath(i.Path)
	}
	return filepath.Base(filepath.Dir(i.Path)) + "/" + filepath.Base(i.Path)
}

// Age is how long the item has been idle.
func (i Item) Age() time.Duration {
	if i.LastBuilt.IsZero() {
		return -1
	}
	return time.Since(i.LastBuilt)
}

// Reason explains in a few words why the item was selected.
func (i Item) Reason() string {
	if i.State == StateOrphan {
		if i.Workspace != "" {
			return "orphan — " + filepath.Base(i.Workspace) + " deleted"
		}
		return "orphan"
	}
	if age := i.Age(); age >= 0 {
		return "idle " + FormatAge(age)
	}
	return ""
}
