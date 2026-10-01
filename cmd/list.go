package cmd

import (
	"fmt"
	"sort"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/mater"
)

func newListCmd() *cobra.Command {
	var (
		orphansOnly bool
		fast        bool
		sortBy      string
	)

	cmd := &cobra.Command{
		Use:     "list",
		Aliases: []string{"ls"},
		GroupID: "inspect",
		Short:   "List every build directory with its size, age, and owner",
		Long: `List build output, one row per directory.

Build directories are named after their content hash, which is unrecognisable,
so each row is labelled with the work that produced it: the Copilot session
that created the worktree, or the checkout's own name.

Sizes are measured by walking each tree, which takes a moment. Pass --fast to
skip it. The listing ends with the free space left on disk either way: the
filesystem reports it directly, so it needs no walk.`,
		Example: `  # Everything, largest first
  mater list

  # Just what 'mater prune' would take
  mater list --orphans

  # Skip sizing for an instant answer
  mater list --fast --sort age`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			switch sortBy {
			case "size", "age", "name":
			default:
				return fmt.Errorf("unknown --sort %q: expected size, age, or name", sortBy)
			}

			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{DetectInUse: !fast})
			if err != nil {
				return err
			}

			items := sv.Items
			if orphansOnly {
				items = sv.Select(mater.ScopeOrphans, 0, true).Items
			}
			if len(items) == 0 {
				u.Note("no build output found under %s", mater.ShortPath(cfg.BuildRoot))
				reportFree(u, volumes(cfg.BuildRoot, nil))
				return nil
			}

			if !fast {
				items = measure(u, items, "sizing")
			}

			sort.SliceStable(items, func(i, j int) bool {
				switch {
				case sortBy == "age" || (sortBy == "size" && fast):
					// Nothing was measured, so size ordering would be arbitrary.
					return items[i].Age() > items[j].Age()
				case sortBy == "name":
					return items[i].Title() < items[j].Title()
				default:
					return items[i].Size > items[j].Size
				}
			})

			itemTable(u, items, !orphansOnly)
			summarise(u, items, countOrphans(items), "total:")
			reportFree(u, volumes(cfg.BuildRoot, items))
			u.Blank()
			return nil
		},
	}

	f := cmd.Flags()
	f.BoolVar(&orphansOnly, "orphans", false, "show only output whose workspace is gone")
	f.BoolVar(&fast, "fast", false, "skip sizing and liveness detection")
	f.StringVar(&sortBy, "sort", "size", "order rows by size, age, or name")
	return cmd
}

func countOrphans(items []mater.Item) int {
	n := 0
	for _, it := range items {
		if it.State == mater.StateOrphan {
			n++
		}
	}
	return n
}
