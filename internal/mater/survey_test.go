package mater

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/colbylwilliams/mater/internal/config"
)

// A workspace created since the last run has never been recorded, and the only
// way to learn its build directory is to ask Cargo while the workspace exists.
func TestScanAsksCargoAboutLiveWorkspacesByDefault(t *testing.T) {
	fakeCargo(t)
	root := t.TempDir()
	build := filepath.Join(root, "build", "ab", "0123456789abcd")
	ws := filepath.Join(root, "checkouts", "brand-new")
	rustWorkspace(t, ws, build)
	cfg := scanConfig(root, ws)

	// The process snapshot runs alongside the Cargo queries, so it is taken
	// here too, where the race detector can see both.
	sv, err := Scan(context.Background(), cfg, SurveyOptions{DetectInUse: true})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, sv, build); it.State != StateLive || it.Workspace != ws {
		t.Errorf("default scan: %s is %v owned by %q, want live owned by %q", build, it.State, it.Workspace, ws)
	}
	if sv.Process == nil {
		t.Error("default scan asked to detect use took no process snapshot")
	}

	sv, err = Scan(context.Background(), cfg, SurveyOptions{SkipRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, sv, build); it.State != StateUnknown {
		t.Errorf("scan skipping the refresh: %s is %v, want it left as recorded: unknown", build, it.State)
	}
}

// The index can name a workspace that has since gone while one that still
// exists builds into the same directory: a member crate recorded in place of
// its workspace root, say. Asking Cargo again is what tells output still in use
// apart from an orphan.
func TestScanRechecksAnApparentOrphan(t *testing.T) {
	fakeCargo(t)
	root := t.TempDir()
	build := filepath.Join(root, "build", "ab", "0123456789abcd")
	ws := filepath.Join(root, "checkouts", "live")
	rustWorkspace(t, ws, build)
	cfg := scanConfig(root, ws)

	gone := filepath.Join(ws, "crates", "removed")
	if err := os.WriteFile(cfg.IndexFile(), []byte(build+"\t"+gone+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	sv, err := Scan(context.Background(), cfg, SurveyOptions{SkipRefresh: true})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, sv, build); it.State != StateOrphan {
		t.Fatalf("as recorded, %s is %v, want orphan", build, it.State)
	}

	sv, err = Scan(context.Background(), cfg, SurveyOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if it := findItem(t, sv, build); it.State != StateLive || it.Workspace != ws {
		t.Errorf("after asking Cargo, %s is %v owned by %q, want live owned by %q", build, it.State, it.Workspace, ws)
	}
}

// fakeCargo puts a cargo on PATH that answers from a file in the directory it
// runs in, so each test decides which build directory a workspace maps to.
func fakeCargo(t *testing.T) {
	t.Helper()
	bin := t.TempDir()
	script := "#!/bin/sh\nexec cat cargo-metadata.json\n"
	if err := os.WriteFile(filepath.Join(bin, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

// rustWorkspace creates a checkout that Cargo maps to buildDir, along with
// buildDir itself.
func rustWorkspace(t *testing.T, dir, buildDir string) {
	t.Helper()
	for _, d := range []string{dir, buildDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(dir, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	answer, err := json.Marshal(map[string]string{"build_directory": buildDir})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "cargo-metadata.json"), answer, 0o644); err != nil {
		t.Fatal(err)
	}
}

// scanConfig confines a survey to root, scanning only the checkouts given.
func scanConfig(root string, checkouts ...string) *config.Config {
	off := false
	return &config.Config{
		BuildRoot:       filepath.Join(root, "build"),
		Roots:           checkouts,
		SessionState:    filepath.Join(root, "no-session-state"),
		ScanNodeModules: &off,
	}
}

func findItem(t *testing.T, sv *Survey, path string) Item {
	t.Helper()
	for _, it := range sv.Items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no item for %s among %d", path, len(sv.Items))
	return Item{}
}
