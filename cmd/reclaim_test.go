package cmd

import (
	"testing"

	"github.com/colbylwilliams/mater/internal/mater"
)

// parsePrune runs the parsing cobra would, and reports what --stale ended up
// holding. The flag's Value writes through to the same string the command
// reads, so a value rescued from the positionals is visible here.
func parsePrune(t *testing.T, args ...string) (string, error) {
	t.Helper()

	cmd := newPruneCmd()
	if err := cmd.Flags().Parse(args); err != nil {
		return "", err
	}
	if err := cmd.ValidateArgs(cmd.Flags().Args()); err != nil {
		return "", err
	}
	return cmd.Flags().Lookup("stale").Value.String(), nil
}

// --stale is an optional-value flag, which in pflag means it never consumes the
// following argument. Every spelling a user would reach for has to arrive at
// the same threshold anyway.
func TestStaleAcceptsValueInEverySpelling(t *testing.T) {
	for _, args := range [][]string{
		{"--stale", "8h"},
		{"-s", "8h"},
		{"--stale=8h"},
		{"-s=8h"},
		{"--stale", "8h", "--dry-run"},
		{"--dry-run", "--stale", "8h"},
		{"-s", "8h", "-o"},
	} {
		got, err := parsePrune(t, args...)
		if err != nil {
			t.Errorf("%v: %v", args, err)
			continue
		}
		if got != "8h" {
			t.Errorf("%v: stale = %q, want %q", args, got, "8h")
		}
	}
}

// Given bare, the flag has to stay distinguishable from being absent so that
// the threshold can fall back to config.
func TestStaleGivenBareDefersToConfig(t *testing.T) {
	got, err := parsePrune(t, "--stale", "--dry-run")
	if err != nil {
		t.Fatal(err)
	}
	if got != staleFromConfig {
		t.Errorf("stale = %q, want the config sentinel %q", got, staleFromConfig)
	}
}

// Rescuing a positional must not turn prune into a command that quietly
// swallows arguments it does not understand.
func TestPruneRejectsArgumentsThatAreNotStaleValues(t *testing.T) {
	for _, args := range [][]string{
		{"8h"},                   // no --stale to claim it
		{"--stale", "8h", "9h"},  // one value, two positionals
		{"--stale=8h", "9h"},     // value already supplied
		{"--stale", "zz"},        // not an age
		{"--dry-run", "orphans"}, // stray word
	} {
		if _, err := parsePrune(t, args...); err == nil {
			t.Errorf("%v: parsed without error, want a rejection", args)
		}
	}
}

func TestPruneScope(t *testing.T) {
	tests := []struct {
		name        string
		stale       bool
		skipOrphans bool
		want        mater.Scope
		wantErr     bool
	}{
		{"bare prune takes orphans", false, false, mater.ScopeOrphans, false},
		{"--stale adds idle output", true, false, mater.ScopeStale, false},
		{"--skip-orphans narrows to idle output", true, true, mater.ScopeStaleOnly, false},
		{"--skip-orphans alone has nothing to take", false, true, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := pruneScope(tt.stale, tt.skipOrphans)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected an error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tt.want {
				t.Errorf("scope = %v, want %v", got, tt.want)
			}
		})
	}
}
