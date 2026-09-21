package sessions

import (
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, dir, id, content string) {
	t.Helper()
	d := filepath.Join(dir, id)
	if err := os.MkdirAll(d, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(d, "workspace.yaml"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestLoadPrefersGitRootAndName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "one", `id: one
cwd: /work/tree/sub
git_root: /work/tree
repository: example/my-repo
branch: colby/feature
name: Real name
summary: Fallback summary
updated_at: 2026-05-01T02:54:39.687Z
`)

	set := Load(dir)
	s, ok := set.Lookup("/work/tree")
	if !ok {
		t.Fatal("git_root was not used as the key")
	}
	if s.Label != "Real name" {
		t.Errorf("Label = %q, want the name field", s.Label)
	}
	if s.Repo != "example/my-repo" || s.Branch != "colby/feature" {
		t.Errorf("Repo/Branch = %q/%q", s.Repo, s.Branch)
	}
}

func TestLoadFallsBackToCwdAndSummary(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "one", `id: one
cwd: /work/tree
summary: Only a summary
updated_at: 2026-05-01T00:00:00.000Z
`)

	set := Load(dir)
	if got := set.Label("/work/tree"); got != "Only a summary" {
		t.Errorf("Label = %q, want the summary", got)
	}
}

// A worktree can be reused across sessions, so the most recently updated one
// has to win; otherwise a directory is labelled with stale work.
func TestNewestSessionWinsForAReusedWorktree(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "older", `cwd: /work/tree
name: Older
updated_at: 2026-01-01T00:00:00.000Z
`)
	write(t, dir, "newer", `cwd: /work/tree
name: Newer
updated_at: 2026-06-01T00:00:00.000Z
`)

	set := Load(dir)
	if got := set.Label("/work/tree"); got != "Newer" {
		t.Errorf("Label = %q, want Newer", got)
	}
	if set.Len() != 1 {
		t.Errorf("Len = %d, want 1 distinct root", set.Len())
	}
	if recent := set.Recent(); len(recent) != 1 || recent[0].Label != "Newer" {
		t.Errorf("Recent = %+v, want one entry labelled Newer", recent)
	}
}

func TestRecentIsNewestFirst(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "a", "cwd: /a\nname: A\nupdated_at: 2026-01-01T00:00:00.000Z\n")
	write(t, dir, "b", "cwd: /b\nname: B\nupdated_at: 2026-09-01T00:00:00.000Z\n")
	write(t, dir, "c", "cwd: /c\nname: C\nupdated_at: 2026-05-01T00:00:00.000Z\n")

	got := Load(dir).Recent()
	want := []string{"B", "C", "A"}
	if len(got) != len(want) {
		t.Fatalf("Recent has %d entries, want %d", len(got), len(want))
	}
	for i := range want {
		if got[i].Label != want[i] {
			t.Errorf("Recent[%d] = %q, want %q", i, got[i].Label, want[i])
		}
	}
}

// Nested blocks and unparseable files must not derail a load of thousands of
// documents.
func TestLoadSurvivesJunk(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "good", "cwd: /good\nname: Good\nupdated_at: 2026-01-01T00:00:00.000Z\n")
	write(t, dir, "nested", `cwd: /nested
name: Outer
nested:
  cwd: /should-be-ignored
  name: Inner
`)
	write(t, dir, "rootless", "name: No root here\n")
	if err := os.MkdirAll(filepath.Join(dir, "empty"), 0o755); err != nil {
		t.Fatal(err)
	}

	set := Load(dir)
	if set.Len() != 2 {
		t.Errorf("Len = %d, want 2", set.Len())
	}
	if _, ok := set.Lookup("/should-be-ignored"); ok {
		t.Error("an indented key was read as a top-level value")
	}
	if got := set.Label("/nested"); got != "Outer" {
		t.Errorf("Label = %q, want Outer", got)
	}
}

func TestLabelFallsBackToDirectoryName(t *testing.T) {
	set := Load(t.TempDir())
	if got := set.Label("/some/path/my-worktree"); got != "my-worktree" {
		t.Errorf("Label = %q, want my-worktree", got)
	}
}

func TestLoadMissingDirectory(t *testing.T) {
	set := Load(filepath.Join(t.TempDir(), "nope"))
	if set.Len() != 0 {
		t.Errorf("Len = %d, want 0", set.Len())
	}
}
