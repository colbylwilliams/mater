package cmd

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/inuse"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
)

// reclaimOpts are the flags shared by every command that deletes something.
type reclaimOpts struct {
	scope          mater.Scope
	stale          string
	dryRun         bool
	yes            bool
	includeRunning bool
	force          bool
}

// addReclaimFlags registers the flags common to prune and clean.
func addReclaimFlags(cmd *cobra.Command, o *reclaimOpts) {
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "report what would be removed without removing it")
	f.BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	f.BoolVar(&o.includeRunning, "include-running", false, "include output a live process is using")
	f.BoolVar(&o.force, "force", false, "proceed even while a build is running")
}

// runReclaim is the whole destructive path: survey, select, price, confirm,
// stage, and hand off to a detached worker.
func runReclaim(cmd *cobra.Command, o *reclaimOpts) error {
	u, cfg := shared.ui, shared.cfg

	var age time.Duration
	if o.scope == mater.ScopeStale {
		// --stale may be given with no value, in which case pflag substitutes
		// the no-option default and the threshold comes from config.
		v := strings.TrimSpace(o.stale)
		if v == "" {
			v = cfg.StaleAge
		}
		parsed, err := mater.ParseAge(v)
		if err != nil {
			return err
		}
		age = parsed
	}

	sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{Refresh: true, DetectInUse: true})
	if err != nil {
		return err
	}

	// Per-directory detection cannot see a compiler that starts moments from
	// now, so taking everything additionally refuses while any build is live.
	// Narrower scopes only take output that is already idle or already orphaned.
	if o.scope == mater.ScopeAll && !o.force && inuse.RustcRunning(ctx(cmd)) {
		if !o.dryRun {
			// Rendered as a single sentence: the error presenter reflows and
			// capitalises whatever it is given.
			return fmt.Errorf("a full clean would corrupt the build that is currently running; " +
				"wait for rustc to finish, re-run with --force, or use 'mater prune --stale' to take only idle output")
		}
		u.Warning("rustc is running — a real run would refuse (use --force, or 'mater prune --stale')")
		u.Blank()
	}

	sel := sv.Select(o.scope, age, o.includeRunning)
	leftovers := reap.Leftovers(cfg.BuildRoot)

	reportSkipped(u, sel.Skipped)

	if sel.Empty() && len(leftovers) == 0 {
		// Even a run that removes nothing has learned where today's build
		// directories live, and that record is what detects tomorrow's orphan.
		if err := sv.Index.Save(); err != nil {
			return err
		}
		u.Note("nothing to reclaim")
		return nil
	}

	// A dry run, and a run that has to ask, both price the work first. Only
	// --yes is allowed to stage before sizing.
	if o.dryRun || !o.yes {
		sel.Items = measure(u, sel.Items, "sizing")
		itemTable(u, sel.Items, o.scope != mater.ScopeOrphans)

		for _, l := range leftovers {
			pending := reap.Pending(l)
			u.Note("  plus staging left by a previous run: %s (%d item%s)",
				mater.ShortPath(l), pending, mater.Plural(pending))
		}

		prefix := "reclaimable:"
		if o.dryRun {
			prefix = "dry run:"
		}
		summarise(u, sel.Items, sel.Orphans, prefix)

		if o.dryRun {
			return nil
		}

		ok, err := u.Confirm("Delete these?")
		if err != nil {
			return err
		}
		if !ok {
			u.Note("aborted — nothing removed")
			return nil
		}
	}

	entries := make([]reap.Entry, len(sel.Items))
	for i, it := range sel.Items {
		entries[i] = reap.Entry{Source: it.Path, Size: it.Size, Origin: it.Path}
	}

	staging, err := reap.Stage(cfg.BuildRoot, entries)
	if err != nil {
		return err
	}
	adopted := staging.Adopt(cfg.BuildRoot)
	reap.PruneShards(cfg.BuildRoot)

	if len(staging.Entries) == 0 {
		staging.Discard()
		if err := sv.Index.Save(); err != nil {
			return err
		}
		u.Note("nothing to reclaim")
		return nil
	}

	// On the --yes path nothing has been priced yet: the tree is already free,
	// so the staged copies are what gets walked, and the report follows.
	if o.yes {
		staging.Measure()
		sized := make(map[string]int64, len(staging.Entries))
		for _, e := range staging.Entries {
			sized[e.Origin] = e.Size
		}
		for i := range sel.Items {
			if size, ok := sized[sel.Items[i].Path]; ok {
				sel.Items[i].Size = size
				sel.Items[i].Sized = true
			}
		}
		itemTable(u, sel.Items, o.scope != mater.ScopeOrphans)
	}

	if err := staging.WriteManifest(); err != nil {
		return err
	}

	log, err := reap.OpenLog(cfg.LogFile)
	if err != nil {
		return err
	}
	log.Printf("%s: staged %s in %d item%s", cmd.Name(),
		mater.FormatSize(staging.Total()), len(staging.Entries), mater.Plural(len(staging.Entries)))
	if sel.Orphans > 0 {
		log.Printf("  %d orphan%s whose workspace no longer exists", sel.Orphans, mater.Plural(sel.Orphans))
	}
	if len(sel.Skipped) > 0 {
		log.Printf("  skipped %d item%s in use", len(sel.Skipped), mater.Plural(len(sel.Skipped)))
	}
	log.Close()
	reap.Rotate(cfg.LogFile)

	if err := sv.Index.Save(); err != nil {
		return err
	}
	if err := reap.Spawn(staging.Dir, globalFlags()...); err != nil {
		return err
	}

	u.Blank()
	u.Success("cleared %s from your tree; deleting in the background",
		u.Size.Render(mater.FormatSize(staging.Total())))
	if adopted > 0 {
		u.Note("  adopted %d item%s left staged by a previous run", adopted, mater.Plural(adopted))
	}
	u.Fields([][2]string{
		{"log", mater.ShortPath(cfg.LogFile)},
		{"watch", "mater logs --follow"},
		{"undo", "mater restore"},
	})
	return nil
}

func newPruneCmd() *cobra.Command {
	o := &reclaimOpts{scope: mater.ScopeOrphans}

	cmd := &cobra.Command{
		Use:     "prune",
		GroupID: "reclaim",
		Short:   "Remove build output whose workspace is gone",
		Long: `Remove orphaned build output.

An orphan is a build directory whose workspace has been deleted. Nothing will
ever use it again, so it is removed regardless of age.

Nothing inside a build directory names the workspace that produced it, so the
link is recorded in the index as each workspace is seen. A directory is only
ever called an orphan when the index holds a path for it that no longer exists,
which is what keeps output from an unscanned repo out of reach.

--stale widens the selection to include output that is still attached to a live
workspace but has sat idle past a threshold.`,
		Example: `  # Remove output from deleted worktrees
  mater prune

  # Preview first
  mater prune --dry-run

  # Also take anything untouched for a week
  mater prune --stale 1w`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if cmd.Flags().Changed("stale") {
				o.scope = mater.ScopeStale
			}
			return runReclaim(cmd, o)
		},
	}

	addReclaimFlags(cmd, o)
	cmd.Flags().StringVar(&o.stale, "stale", "",
		"also remove output idle this long: 45m, 6h, 2d, 1w (default from config)")
	cmd.Flags().Lookup("stale").NoOptDefVal = " "
	return cmd
}

func newCleanCmd() *cobra.Command {
	o := &reclaimOpts{scope: mater.ScopeAll}

	cmd := &cobra.Command{
		Use:     "clean",
		GroupID: "reclaim",
		Short:   "Remove all build output",
		Long: `Remove every build directory, target/, and node_modules that mater knows about,
regardless of age or ownership.

This refuses while any compiler is running, because output taken out from under
an in-flight build corrupts it. Output that a live process is executing from or
sitting inside is held back and reported.`,
		Example: `  # Preview a full clean
  mater clean --dry-run

  # Free everything without confirming
  mater clean --yes`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReclaim(cmd, o)
		},
	}

	addReclaimFlags(cmd, o)
	return cmd
}
