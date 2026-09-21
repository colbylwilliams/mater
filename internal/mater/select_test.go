package mater

import (
	"testing"
	"time"
)

func item(kind Kind, state State, idle time.Duration) Item {
	it := Item{Kind: kind, State: state}
	if idle >= 0 {
		it.LastBuilt = time.Now().Add(-idle)
	}
	return it
}

// prune must never take output it cannot prove is abandoned. Unknown state
// means no workspace was ever recorded, which is exactly the case where the
// directory may belong to a repo mater has never scanned.
func TestScopeOrphansTakesOnlyProvenOrphans(t *testing.T) {
	sv := &Survey{Items: []Item{
		item(KindBuildDir, StateOrphan, time.Hour),
		item(KindBuildDir, StateLive, 400*24*time.Hour),
		item(KindBuildDir, StateUnknown, 400*24*time.Hour),
		item(KindTarget, StateOrphan, time.Hour),
		item(KindNodeModules, StateUnknown, time.Hour),
	}}

	sel := sv.Select(ScopeOrphans, 0, true)
	if len(sel.Items) != 1 {
		t.Fatalf("selected %d items, want 1", len(sel.Items))
	}
	if sel.Items[0].Kind != KindBuildDir || sel.Items[0].State != StateOrphan {
		t.Errorf("selected %v/%v, want build/orphan", sel.Items[0].Kind, sel.Items[0].State)
	}
	if sel.Orphans != 1 {
		t.Errorf("Orphans = %d, want 1", sel.Orphans)
	}
}

func TestScopeStaleTakesOrphansAtAnyAge(t *testing.T) {
	sv := &Survey{Items: []Item{
		item(KindBuildDir, StateOrphan, time.Minute),  // fresh, but abandoned
		item(KindBuildDir, StateLive, 10*time.Minute), // fresh and live
		item(KindBuildDir, StateLive, 10*24*time.Hour),
		item(KindTarget, StateLive, 10*24*time.Hour),
	}}

	sel := sv.Select(ScopeStale, 5*24*time.Hour, true)
	if len(sel.Items) != 3 {
		t.Fatalf("selected %d items, want 3", len(sel.Items))
	}
}

// A directory with no usable timestamp must not be swept up by an age filter:
// absence of evidence is not evidence of idleness.
func TestScopeStaleSkipsUntimestampedOutput(t *testing.T) {
	sv := &Survey{Items: []Item{{Kind: KindBuildDir, State: StateLive}}}

	if sel := sv.Select(ScopeStale, time.Nanosecond, true); len(sel.Items) != 0 {
		t.Fatalf("selected %d items, want 0", len(sel.Items))
	}
}

func TestScopeAllTakesEverything(t *testing.T) {
	sv := &Survey{Items: []Item{
		item(KindBuildDir, StateUnknown, -1),
		item(KindTarget, StateLive, time.Minute),
		item(KindNodeModules, StateOrphan, time.Hour),
	}}

	if sel := sv.Select(ScopeAll, 0, true); len(sel.Items) != 3 {
		t.Fatalf("selected %d items, want 3", len(sel.Items))
	}
}

func TestInUseIsHeldBackUnlessIncluded(t *testing.T) {
	live := item(KindBuildDir, StateOrphan, time.Hour)
	live.InUse = true
	sv := &Survey{Items: []Item{live}}

	sel := sv.Select(ScopeOrphans, 0, false)
	if len(sel.Items) != 0 {
		t.Errorf("selected %d items, want 0", len(sel.Items))
	}
	if len(sel.Skipped) != 1 {
		t.Errorf("skipped %d items, want 1", len(sel.Skipped))
	}
	if sel.Orphans != 0 {
		t.Errorf("Orphans = %d, want 0 — a skipped item was not removed", sel.Orphans)
	}

	if sel := sv.Select(ScopeOrphans, 0, true); len(sel.Items) != 1 {
		t.Errorf("--include-running selected %d items, want 1", len(sel.Items))
	}
}
