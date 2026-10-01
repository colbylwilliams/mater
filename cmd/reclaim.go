package cmd

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/inuse"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
	"github.com/colbylwilliams/mater/internal/ui"
)

// reclaimOpts are the flags shared by every command that deletes something.
type reclaimOpts struct {
	scope          mater.Scope
	stale          string
	skipOrphans    bool
	dryRun         bool
	yes            bool
	includeRunning bool
	force          bool
	watch          bool
}

// addReclaimFlags registers the flags common to prune and nuke.
func addReclaimFlags(cmd *cobra.Command, o *reclaimOpts) {
	f := cmd.Flags()
	f.BoolVarP(&o.dryRun, "dry-run", "n", false, "report what would be removed without removing it")
	f.BoolVarP(&o.yes, "yes", "y", false, "skip the confirmation prompt")
	f.BoolVar(&o.includeRunning, "include-running", false, "include output a live process is using")
	f.BoolVar(&o.force, "force", false, "proceed even while a build is running")
	f.BoolVarP(&o.watch, "watch", "w", false, "show the background delete's progress and return when it finishes")
}

// runReclaim is the whole destructive path: survey, select, price, confirm,
// stage, and hand off to a detached worker, optionally staying to watch it.
func runReclaim(cmd *cobra.Command, o *reclaimOpts) error {
	u, cfg := shared.ui, shared.cfg

	var age time.Duration
	if o.scope == mater.ScopeStale || o.scope == mater.ScopeStaleOnly {
		parsed, err := staleAge(o.stale, cfg.StaleAge)
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
			return fmt.Errorf("a full nuke would corrupt the build that is currently running; " +
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
		reportFree(u, volumes(cfg.BuildRoot, sel.Items))

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

	// A watch reads the log from where it ends before the worker exists, so
	// the worker's first line cannot land unseen.
	var progress *logReader
	if o.watch {
		progress = openProgress(u, cfg.LogFile)
	}
	defer progress.Close()

	worker, err := reap.Spawn(staging.Dir, globalFlags()...)
	if err != nil {
		return err
	}

	u.Blank()
	u.Success("cleared %s from your tree; deleting in the background",
		u.Size.Render(mater.FormatSize(staging.Total())))
	if adopted > 0 {
		u.Note("  adopted %d item%s left staged by a previous run", adopted, mater.Plural(adopted))
	}

	if !o.watch {
		u.Fields([][2]string{
			{"log", mater.ShortPath(cfg.LogFile)},
			{"watch", "mater logs --follow"},
			{"undo", "mater restore"},
		})
		return worker.Release()
	}

	u.Fields([][2]string{
		{"log", mater.ShortPath(cfg.LogFile)},
		{"undo", "mater restore"},
		{"detach", "ctrl+c — the delete carries on"},
	})
	u.Blank()
	return watch(ctx(cmd), u, progress, worker.Wait)
}

// openProgress opens the log a watch reads from, positioned where it ends now.
// It warns rather than fails: by the time a watch begins the items are staged
// and need their worker, so a log that cannot be read costs only the progress
// lines.
func openProgress(u *ui.UI, path string) *logReader {
	if path == "" {
		u.Warning("no log_file is configured, so the watch has no progress to show")
		return nil
	}
	r, err := openLogEnd(path)
	if err != nil {
		u.Warning("cannot read the log, so the watch has no progress to show: %v", err)
		return nil
	}
	return r
}

// watch prints a worker's progress until wait returns, then reports how the
// worker ended. An interrupt ends only the watch: the worker sits outside the
// terminal's process group, so the delete carries on without it.
func watch(c context.Context, u *ui.UI, progress *logReader, wait func() error) error {
	stop, cancel := context.WithCancel(c)
	defer cancel()

	exited := make(chan error, 1)
	go func() {
		exited <- wait()
		cancel()
	}()

	if progress != nil {
		if err := tail(progress, stop.Done(), u.Println); err != nil {
			u.Warning("cannot read the log any further, so no more progress will be shown: %v", err)
		}
	}
	<-stop.Done()

	select {
	case err := <-exited:
		if err != nil {
			// Rendered as a single sentence: the error presenter capitalises
			// the first word and adds the full stop.
			return fmt.Errorf("%w; whatever it had not reached stays staged for the next "+
				"prune or nuke to finish, and 'mater logs' shows what happened", err)
		}
		return nil
	default:
		// An interrupt from the keyboard leaves ^C echoed mid-line.
		u.Blank()
		u.Note("stopped watching; the delete carries on in the background — 'mater logs -f' follows it")
		return nil
	}
}

// parseStale validates a value supplied for --stale. A blank value asked to
// supply an age and supplied none, which is what an unset variable in a script
// looks like, so it fails rather than being read as the bare form.
func parseStale(v string) (time.Duration, error) {
	if strings.TrimSpace(v) == "" {
		// Rendered as a single sentence: the error presenter capitalises the
		// first word, so the message must not start with a flag name.
		return 0, fmt.Errorf("no age given: --stale was supplied an empty value; pass an age " +
			"like 8h, or give --stale on its own to use the threshold from config")
	}
	return mater.ParseAge(v)
}

// staleAge resolves the threshold a stale run measures against. Given bare,
// --stale carries the sentinel and the threshold comes from config; anything
// else is a value the caller supplied, and is held to the same standard
// wherever it arrived from.
func staleAge(flag, configured string) (time.Duration, error) {
	if flag == staleFromConfig {
		return mater.ParseAge(configured)
	}
	return parseStale(flag)
}

// staleFromConfig is the value pflag substitutes when --stale is given with no
// value, marking the threshold as coming from config. It is a NUL byte for two
// reasons: pflag reads an empty NoOptDefVal as the flag having no
// optional-value form at all, and a NUL cannot appear inside an argv entry, so
// no command line can forge the bare form and slip past the checks in staleAge.
const staleFromConfig = "\x00"

// staleArgs rescues the value of an optional-value flag. --stale carries a
// NoOptDefVal so that it can be given bare, and pflag never consumes the
// following argument for such a flag, so `--stale 8h` arrives as a bare --stale
// plus a stray positional. That positional is the flag's value, not a command.
func staleArgs(o *reclaimOpts) cobra.PositionalArgs {
	return func(cmd *cobra.Command, args []string) error {
		if len(args) == 1 && o.stale == staleFromConfig {
			if _, err := parseStale(args[0]); err != nil {
				return err
			}
			o.stale = args[0]
			return nil
		}
		return cobra.NoArgs(cmd, args)
	}
}

// pruneScope turns the scope flags into a selection scope. Orphans are taken by
// default because nothing will ever use them again; --skip-orphans opts out,
// which only leaves work to do once --stale has widened the run.
func pruneScope(stale, skipOrphans bool) (mater.Scope, error) {
	switch {
	case stale && skipOrphans:
		return mater.ScopeStaleOnly, nil
	case stale:
		return mater.ScopeStale, nil
	case skipOrphans:
		// Rendered as a single sentence: the error presenter capitalises the
		// first word, so the message must not start with a flag name.
		return 0, fmt.Errorf("nothing left in scope: --skip-orphans rules out every orphan, " +
			"so add --stale to take idle output instead, or drop --skip-orphans")
	default:
		return mater.ScopeOrphans, nil
	}
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
which is what keeps output mater cannot attribute out of reach of a bare prune.

--stale widens the selection to include output that has sat idle past a
threshold, attributed or not: age is measured from the output itself, so
unattributed directories are claimed too. Given bare it uses the configured
stale_age; given a value, that value instead.

--skip-orphans narrows the other way, leaving abandoned output in place so that
a run takes only what --stale matched.

The delete itself runs in the background, so prune returns as soon as the
output is out of your tree. --watch stays to print the delete's progress and
returns when it finishes; ctrl+c then stops the watching, never the delete.`,
		Example: `  # Remove output from deleted worktrees
  mater prune

  # Preview first
  mater prune --dry-run

  # Stay until the delete has finished
  mater prune --watch

  # Also take anything untouched for a week
  mater prune --stale 1w

  # Use the threshold from config
  mater prune --stale

  # Take only idle output, leaving deleted worktrees alone
  mater prune --stale 8h --skip-orphans`,
		Args: staleArgs(o),
		RunE: func(cmd *cobra.Command, _ []string) error {
			scope, err := pruneScope(cmd.Flags().Changed("stale"), o.skipOrphans)
			if err != nil {
				return err
			}
			o.scope = scope
			return runReclaim(cmd, o)
		},
	}

	addReclaimFlags(cmd, o)
	f := cmd.Flags()
	f.StringVarP(&o.stale, "stale", "s", "",
		"also remove output idle this long: 45m, 6h, 2d, 1w (default from config)")
	f.Lookup("stale").NoOptDefVal = staleFromConfig
	f.BoolVarP(&o.skipOrphans, "skip-orphans", "o", false,
		"leave output whose workspace is gone in place")
	return cmd
}

func newNukeCmd() *cobra.Command {
	o := &reclaimOpts{scope: mater.ScopeAll}

	cmd := &cobra.Command{
		Use:     "nuke",
		GroupID: "reclaim",
		Short:   "Remove all build output",
		Long: `Remove every build directory, target/, and node_modules that mater knows about,
regardless of age or ownership.

This is the widest thing mater does: it takes output belonging to workspaces
you are still using, so the next build in every one of them starts cold. Reach
for 'mater prune' first, which takes only what is abandoned or idle.

This refuses while any compiler is running, because output taken out from under
an in-flight build corrupts it. Output that a live process is executing from or
sitting inside is held back and reported.

The delete itself runs in the background, so nuke returns as soon as the
output is out of your tree. --watch stays to print the delete's progress and
returns when it finishes; ctrl+c then stops the watching, never the delete.`,
		Example: `  # Preview a full nuke
  mater nuke --dry-run

  # Free everything without confirming
  mater nuke --yes

  # Free everything and stay until the delete has finished
  mater nuke --yes --watch`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			return runReclaim(cmd, o)
		},
	}

	addReclaimFlags(cmd, o)
	return cmd
}
