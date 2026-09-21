package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/cargo"
	"github.com/colbylwilliams/mater/internal/config"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/ui"
)

func newIndexCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "index",
		GroupID: "setup",
		Short:   "Inspect and maintain the build-directory index",
		Long: `The index records which workspace produced each build directory.

Nothing inside a build directory names its workspace, so the link has to be
observed while the workspace still exists. That record is the only thing that
lets a directory later be identified as an orphan, which is why 'prune' can
never touch output it has not seen before.`,
	}
	cmd.AddCommand(newIndexShowCmd(), newIndexRefreshCmd(), newIndexBootstrapCmd())
	return cmd
}

func newIndexShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "List recorded build directory to workspace mappings",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{})
			if err != nil {
				return err
			}

			width := u.Width()/2 - 6
			if width < 24 {
				width = 24
			}

			rows := make([][]string, 0, len(sv.Items))
			unattributed := 0
			for _, it := range sv.Items {
				if it.Kind != mater.KindBuildDir {
					continue
				}
				if it.Workspace == "" {
					unattributed++
					rows = append(rows, []string{
						u.Path.Render(ui.Elide(mater.ShortPath(it.Path), width)),
						u.Badge("unknown", "unattributed"),
						u.Muted.Render("—"),
					})
					continue
				}
				rows = append(rows, []string{
					u.Path.Render(ui.Elide(mater.ShortPath(it.Path), width)),
					u.Badge(it.State.String(), it.State.String()),
					ui.Truncate(it.Title(), width),
				})
			}

			if len(rows) == 0 {
				u.Note("no build directories under %s", mater.ShortPath(cfg.BuildRoot))
				return nil
			}

			u.Table([]string{"BUILD DIR", "STATE", "WORKSPACE"}, rows, nil)
			u.Printf("\n%d mapping%s recorded in %s\n",
				sv.Index.Len(), mater.Plural(sv.Index.Len()), mater.ShortPath(cfg.IndexFile()))
			if unattributed > 0 {
				u.Note("  %d unattributed — try 'mater index bootstrap'", unattributed)
			}
			return nil
		},
	}
}

func newIndexRefreshCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "refresh [workspace...]",
		Short: "Record the build directory of each live workspace",
		Long: `Ask Cargo which build directory each workspace maps to, and record the answer.

This runs automatically before every prune and clean. Run it by hand to add a
workspace outside the auto-discovered worktree roots; a path given here is
resolved even though it would not normally be scanned.`,
		Example: `  mater index refresh
  mater index refresh ~/code/some-other-rust-repo`,
		RunE: func(cmd *cobra.Command, args []string) error {
			u, cfg := shared.ui, shared.cfg

			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{Refresh: true})
			if err != nil {
				return err
			}

			home, _ := os.UserHomeDir()
			var extra []string
			for _, a := range args {
				p, err := filepath.Abs(config.Expand(a, home))
				if err != nil {
					continue
				}
				extra = append(extra, p)
			}
			for _, m := range cargo.ResolveAll(ctx(cmd), extra) {
				sv.Index.Set(m.BuildDir, m.Workspace)
				u.Success("%s → %s", mater.ShortPath(m.Workspace), mater.ShortPath(m.BuildDir))
			}

			if err := sv.Index.Save(); err != nil {
				return err
			}
			u.Success("%d mapping%s recorded across %d checkout%s",
				sv.Index.Len(), mater.Plural(sv.Index.Len()),
				len(sv.Checkouts), mater.Plural(len(sv.Checkouts)))
			return nil
		},
	}
}

func newIndexBootstrapCmd() *cobra.Command {
	var (
		since  string
		dryRun bool
	)

	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Attribute build directories that predate the index",
		Long: `Recover ownership of build directories the index never saw.

A directory with no recorded workspace can never be recognised as an orphan, so
it accumulates untouched. Two records survive a deleted worktree: Copilot
session state, and git's worktree registry. A deleted path can still be hashed,
so those rows are recoverable after the fact.

The hash is a pure function of the workspace path, but Cargo canonicalises that
path before hashing, so asking about a path requires a manifest to exist there.
A stub is written, queried, and taken back. A path that already exists is never
probed, and nothing is removed that was not written here.

The probe window is derived from the oldest surviving unattributed directory,
which keeps the number of paths briefly occupied to a handful rather than every
worktree ever deleted.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{Refresh: true})
			if err != nil {
				return err
			}

			var unknown []mater.Item
			for _, it := range sv.Items {
				if it.Kind == mater.KindBuildDir && it.Workspace == "" {
					unknown = append(unknown, it)
				}
			}
			if len(unknown) == 0 {
				u.Success("every build directory is already attributed")
				return sv.Index.Save()
			}

			cutoff, err := probeCutoff(since, unknown)
			if err != nil {
				return err
			}
			candidates := probeCandidates(sv, cfg, cutoff)

			u.Printf("%d build director%s unattributed; %d deleted worktree%s active since %s\n",
				len(unknown), map[bool]string{true: "y", false: "ies"}[len(unknown) == 1],
				len(candidates), mater.Plural(len(candidates)), cutoff.Format("2006-01-02"))

			if dryRun {
				u.Note("\nwould probe most-recent-first, stopping once all are matched:")
				for i, p := range candidates {
					if i == 5 {
						u.Note("  … and %d more", len(candidates)-5)
						break
					}
					u.Note("  %s", filepath.Base(p))
				}
				return nil
			}
			if len(candidates) == 0 {
				u.Warning("no deleted worktrees to probe — these directories belong to repos outside the scanned roots")
				return nil
			}

			remaining := map[string]struct{}{}
			for _, it := range unknown {
				remaining[it.Path] = struct{}{}
			}

			u.Section("Probing")
			matched, probed := 0, 0
			for len(candidates) > 0 && len(remaining) > 0 {
				n := min(24, len(candidates))
				batch := candidates[:n]
				candidates = candidates[n:]
				probed += n

				for _, m := range cargo.ProbeAll(ctx(cmd), batch) {
					if _, want := remaining[m.BuildDir]; !want {
						continue
					}
					sv.Index.Set(m.BuildDir, m.Workspace)
					delete(remaining, m.BuildDir)
					matched++
					label := sv.Sessions.Label(m.Workspace)
					u.Success("%s  ←  %s", mater.ShortPath(m.BuildDir), label)
				}
			}

			if err := sv.Index.Save(); err != nil {
				return err
			}
			u.Printf("\nmatched %d of %d after probing %d path%s\n",
				matched, len(unknown), probed, mater.Plural(probed))
			if len(remaining) > 0 {
				u.Note("  %d still unattributed — these belong to repos outside the scanned roots",
					len(remaining))
			}
			return nil
		},
	}

	cmd.Flags().StringVar(&since, "since", "", "widen or narrow the probe window: 2w, 30d")
	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what would be probed")
	return cmd
}

// probeCutoff bounds how far back to look for deleted worktrees. A build
// directory can only belong to a worktree whose session was active around the
// time it was last built, so the window is derived from the oldest surviving
// unattributed directory unless the caller overrides it.
func probeCutoff(since string, unknown []mater.Item) (time.Time, error) {
	if since != "" {
		d, err := mater.ParseAge(since)
		if err != nil {
			return time.Time{}, err
		}
		return time.Now().Add(-d), nil
	}

	var oldest time.Time
	for _, it := range unknown {
		if it.LastBuilt.IsZero() {
			continue
		}
		if oldest.IsZero() || it.LastBuilt.Before(oldest) {
			oldest = it.LastBuilt
		}
	}
	if oldest.IsZero() {
		return time.Now().AddDate(0, 0, -30), nil
	}
	return oldest.AddDate(0, 0, -7), nil
}

// probeCandidates lists worktree paths that no longer exist, newest first.
func probeCandidates(sv *mater.Survey, cfg *config.Config, cutoff time.Time) []string {
	seen := map[string]struct{}{}
	var out []string

	add := func(p string) {
		if p == "" || !strings.Contains(p, "copilot-worktrees") {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		if _, err := os.Lstat(p); err == nil {
			return // never probe a live path
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	// Sessions are already newest first, and a session timestamp is what the
	// window filters on.
	iso := cutoff.UTC().Format(time.RFC3339)
	for _, s := range sv.Sessions.Recent() {
		if s.UpdatedAt != "" && s.UpdatedAt < iso {
			continue
		}
		add(s.Root)
	}
	// Git's registry carries no timestamp, so those entries bypass the window.
	for _, p := range prunableWorktrees(sv.Checkouts) {
		add(p)
	}
	return out
}

// prunableWorktrees asks git for worktrees it still has a record of but whose
// directory is gone. A worktree parent looks like <base>/copilot-worktrees/<repo>,
// so the main checkout that owns the registry is <base>/<repo>.
func prunableWorktrees(checkouts []string) []string {
	repos := map[string]struct{}{}
	for _, c := range checkouts {
		parent := filepath.Dir(c)
		if filepath.Base(filepath.Dir(parent)) != "copilot-worktrees" {
			continue
		}
		main := filepath.Join(filepath.Dir(filepath.Dir(parent)), filepath.Base(parent))
		if exists(filepath.Join(main, ".git")) {
			repos[main] = struct{}{}
		}
	}

	var out []string
	for repo := range repos {
		cmd := exec.Command("git", "-C", repo, "worktree", "list", "--porcelain")
		data, err := cmd.Output()
		if err != nil {
			continue
		}
		var current string
		for _, line := range strings.Split(string(data), "\n") {
			switch {
			case strings.HasPrefix(line, "worktree "):
				current = strings.TrimPrefix(line, "worktree ")
			case strings.HasPrefix(line, "prunable"):
				out = append(out, current)
			}
		}
	}
	return out
}
