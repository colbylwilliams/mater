package cmd

import (
	"time"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
	"github.com/colbylwilliams/mater/internal/ui"
)

func newRestoreCmd() *cobra.Command {
	var dryRun bool

	cmd := &cobra.Command{
		Use:     "restore",
		GroupID: "reclaim",
		Short:   "Put back build output that has not been deleted yet",
		Long: `Undo a delete that is still in progress.

Staging is what makes this possible. A delete moves each directory aside and
unlinks it afterwards, so until the worker reaches an item it is still whole,
and the manifest records where it came from.

Any running worker is asked to stop first. It only ever checks between whole
items, so it is never interrupted part-way through one, and whatever it has not
reached is put straight back.

An item is left alone when something has since taken its old path: a build that
ran after the delete will have recreated the directory, and the fresh output is
the one to keep.`,
		Example: `  # See what could still be recovered
  mater restore --dry-run

  # Stop the delete and put everything back
  mater restore`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			staging := reap.Leftovers(cfg.BuildRoot)
			if len(staging) == 0 {
				u.Note("nothing staged — there is no delete to undo")
				return nil
			}

			// Stop first, so the worker cannot consume an item between the
			// survey below and the moves that follow.
			if !dryRun {
				for _, dir := range staging {
					if err := reap.Stop(dir); err != nil {
						return err
					}
				}
				waitForWorkers(staging)
			}

			var ready, blocked []reap.Staged
			for _, dir := range staging {
				r, b := reap.Recoverable(dir)
				ready = append(ready, r...)
				blocked = append(blocked, b...)
			}

			if len(ready) == 0 && len(blocked) == 0 {
				for _, dir := range staging {
					if err := reap.Cleanup(dir); err != nil {
						return err
					}
				}
				u.Note("everything staged has already been deleted")
				return nil
			}

			stagedTable(u, ready)
			reportBlocked(u, blocked)

			if dryRun {
				u.Printf("\ndry run: %s across %d item%s could be restored\n",
					u.Size.Render(mater.FormatSize(totalStaged(ready))),
					len(ready), mater.Plural(len(ready)))
				return nil
			}

			log, err := reap.OpenLog(cfg.LogFile)
			if err != nil {
				return err
			}
			defer log.Close()

			log.Printf("restore: %d item%s requested", len(ready), mater.Plural(len(ready)))
			restored, restoreErr := reap.Restore(ready, log)
			log.Printf("restore: %d item%s put back", restored, mater.Plural(restored))

			// A delete drops what it staged from the index, since that output
			// has left the build root by the time the delete saves. Asking Cargo
			// again gives back the owner of anything restored whose workspace
			// still exists, before a later failure can return without doing so.
			if restored > 0 {
				if _, err := scan(cmd, mater.SurveyOptions{}); err != nil {
					u.Warning("ownership not re-recorded: %v", err)
				}
			}

			for _, dir := range staging {
				if err := reap.Cleanup(dir); err != nil {
					return err
				}
			}
			if restoreErr != nil {
				return restoreErr
			}

			u.Blank()
			u.Success("restored %s across %d item%s",
				u.Size.Render(mater.FormatSize(totalStaged(ready[:restored]))),
				restored, mater.Plural(restored))
			return nil
		},
	}

	cmd.Flags().BoolVarP(&dryRun, "dry-run", "n", false, "show what could be restored without moving anything")
	return cmd
}

// waitForWorkers gives a running worker time to notice the stop sentinel and
// finish the item it is on. Polling the staging directory is enough: the worker
// removes each slot as it goes, so a directory that stops shrinking has stopped.
func waitForWorkers(staging []string) {
	const (
		settle = 400 * time.Millisecond
		limit  = 15 * time.Second
	)

	deadline := time.Now().Add(limit)
	previous := -1
	for time.Now().Before(deadline) {
		total := 0
		for _, dir := range staging {
			total += reap.Pending(dir)
		}
		if total == previous {
			return
		}
		previous = total
		time.Sleep(settle)
	}
}

func stagedTable(u *ui.UI, items []reap.Staged) {
	if len(items) == 0 {
		return
	}

	width := u.Width() - 20
	if width < 40 {
		width = 40
	}

	rows := make([][]string, 0, len(items))
	for _, s := range items {
		size := u.Muted.Render("—")
		if s.Size > 0 {
			size = u.Size.Render(mater.FormatSize(s.Size))
		}
		rows = append(rows, []string{size, ui.Elide(mater.ShortPath(s.Origin), width)})
	}
	u.Table([]string{"SIZE", "RESTORE TO"}, rows, map[int]bool{0: true})
}

func reportBlocked(u *ui.UI, items []reap.Staged) {
	if len(items) == 0 {
		return
	}
	u.Blank()
	u.Warning("%d item%s cannot be restored", len(items), mater.Plural(len(items)))
	for _, s := range items {
		switch {
		case s.Origin == "":
			u.Note("    slot %s — no recorded origin", s.Slot)
		default:
			u.Note("    %s — something already occupies that path", mater.ShortPath(s.Origin))
		}
	}
	u.Note("    these stay staged and will be deleted by the next prune or nuke")
}

func totalStaged(items []reap.Staged) int64 {
	var n int64
	for _, s := range items {
		n += s.Size
	}
	return n
}
