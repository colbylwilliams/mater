package cmd

import (
	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/disk"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
)

func newStatusCmd() *cobra.Command {
	var withSize bool

	cmd := &cobra.Command{
		Use:     "status",
		GroupID: "inspect",
		Short:   "Summarise build output and any delete in progress",
		Long: `Show what is on disk, how much of it is reclaimable, and whether a background
delete is still running.

Sizes are omitted by default because measuring means walking every tree. Pass
--size to include them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{})
			if err != nil {
				return err
			}

			var counts struct{ build, target, node, orphan, unknown int }
			for _, it := range sv.Items {
				switch it.Kind {
				case mater.KindBuildDir:
					counts.build++
				case mater.KindTarget:
					counts.target++
				case mater.KindNodeModules:
					counts.node++
				}
				switch it.State {
				case mater.StateOrphan:
					counts.orphan++
				case mater.StateUnknown:
					counts.unknown++
				}
			}

			u.Section("Build output")
			fields := [][2]string{
				{"build root", mater.ShortPath(cfg.BuildRoot)},
				{"build dirs", plural(counts.build, "directory", "directories")},
				{"checkouts", plural(len(sv.Checkouts), "scanned", "scanned")},
				{"target/", plural(counts.target, "directory", "directories")},
			}
			if cfg.NodeModules() {
				fields = append(fields, [2]string{"node_modules", plural(counts.node, "tree", "trees")})
			}
			if withSize {
				total := disk.Size(cfg.BuildRoot)
				fields = append(fields, [2]string{"on disk", u.Size.Render(mater.FormatSize(total.Bytes))})
			}
			u.Fields(fields)

			u.Section("Reclaimable")
			reclaim := [][2]string{}
			if counts.orphan > 0 {
				reclaim = append(reclaim, [2]string{"orphans",
					u.Warn.Render(plural(counts.orphan, "directory", "directories")) + u.Muted.Render("  workspace deleted — 'mater prune'")})
			} else {
				reclaim = append(reclaim, [2]string{"orphans", u.Good.Render("none")})
			}
			if counts.unknown > 0 {
				reclaim = append(reclaim, [2]string{"unattributed",
					plural(counts.unknown, "directory", "directories") + u.Muted.Render("  'mater index bootstrap'")})
			}
			u.Fields(reclaim)

			u.Section("Background delete")
			if pending := reap.Leftovers(cfg.BuildRoot); len(pending) > 0 {
				rows := make([][2]string, 0, len(pending))
				for _, p := range pending {
					n := reap.Pending(p)
					rows = append(rows, [2]string{mater.ShortPath(p),
						plural(n, "item left", "items left")})
				}
				u.Fields(rows)
			} else {
				u.Fields([][2]string{{"state", u.Good.Render("idle")}})
			}

			if recent := reap.Tail(cfg.LogFile, 5); len(recent) > 0 {
				u.Section("Recent activity")
				for _, line := range recent {
					u.Note("  %s", line)
				}
			}
			u.Blank()
			return nil
		},
	}

	cmd.Flags().BoolVar(&withSize, "size", false, "measure total bytes under the build root")
	return cmd
}

// plural renders a count with the right noun for readability in Fields output.
func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return itoa(n) + " " + many
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
