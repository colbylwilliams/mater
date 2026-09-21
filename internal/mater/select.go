package mater

import "time"

// Scope is how much of the survey a destructive command claims.
type Scope int

const (
	// ScopeOrphans takes only output whose workspace has been deleted. Nothing
	// will ever use it again, so it is safe at any age.
	ScopeOrphans Scope = iota
	// ScopeStale takes orphans plus output that has sat idle past a threshold.
	ScopeStale
	// ScopeAll takes every piece of build output that was found.
	ScopeAll
)

// Selection is the outcome of applying a scope to a survey.
type Selection struct {
	Items   []Item
	Skipped []Item // held back because a process is using them
	Orphans int
	Scope   Scope
	Age     time.Duration
}

// Empty reports whether there is nothing to do.
func (s Selection) Empty() bool { return len(s.Items) == 0 }

// Select applies a scope to the survey.
//
// includeRunning overrides the liveness guard. Without it, output a process is
// executing from or sitting inside is held back and reported rather than taken.
func (sv *Survey) Select(scope Scope, age time.Duration, includeRunning bool) Selection {
	sel := Selection{Scope: scope, Age: age}

	for _, it := range sv.Items {
		if !claims(it, scope, age) {
			continue
		}
		if it.InUse && !includeRunning {
			sel.Skipped = append(sel.Skipped, it)
			continue
		}
		if it.State == StateOrphan {
			sel.Orphans++
		}
		sel.Items = append(sel.Items, it)
	}
	return sel
}

func claims(it Item, scope Scope, age time.Duration) bool {
	switch scope {
	case ScopeAll:
		return true

	case ScopeOrphans:
		// A deleted worktree takes its own target/ and node_modules with it, so
		// only build output living outside the checkout can be orphaned.
		return it.Kind == KindBuildDir && it.State == StateOrphan

	case ScopeStale:
		if it.Kind == KindBuildDir && it.State == StateOrphan {
			return true
		}
		if it.LastBuilt.IsZero() {
			// No usable timestamp is not evidence of idleness.
			return false
		}
		return time.Since(it.LastBuilt) >= age

	default:
		return false
	}
}
