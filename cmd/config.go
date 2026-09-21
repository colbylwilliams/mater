package cmd

import (
	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/config"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
)

// newReapCmd is the detached worker. It is hidden because it is an internal
// step of 'prune' and 'nuke' rather than something to invoke by hand, but it
// stays a real subcommand so the parent can re-exec itself and exit.
func newReapCmd() *cobra.Command {
	return &cobra.Command{
		Use:    "reap <staging-dir>",
		Short:  "Delete a staged directory (internal)",
		Hidden: true,
		Args:   cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := shared.cfg

			log, err := reap.OpenLog(cfg.LogFile)
			if err != nil {
				return err
			}
			defer log.Close()

			if err := reap.Run(ctx(cmd), args[0], log); err != nil {
				log.Printf("delete failed: %v", err)
				return err
			}
			reap.PruneShards(cfg.BuildRoot)
			return nil
		},
	}
}

func newConfigCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "config",
		GroupID: "setup",
		Short:   "Show or create the configuration file",
	}
	cmd.AddCommand(newConfigShowCmd(), newConfigInitCmd(), newConfigPathCmd())
	return cmd
}

func newConfigShowCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "show",
		Short: "Print the settings in effect",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			source := u.Muted.Render("(defaults — no file)")
			if exists(cfg.Path()) {
				source = mater.ShortPath(cfg.Path())
			}

			u.Section("Configuration")
			u.Fields([][2]string{
				{"source", source},
				{"build_root", mater.ShortPath(cfg.BuildRoot)},
				{"session_state", mater.ShortPath(cfg.SessionState)},
				{"log_file", mater.ShortPath(cfg.LogFile)},
				{"stale_age", cfg.StaleAge},
				{"scan_node_modules", boolText(cfg.NodeModules())},
			})

			if len(cfg.Roots) > 0 {
				u.Section("Extra roots")
				for _, r := range cfg.Roots {
					u.Note("  %s", mater.ShortPath(r))
				}
			}
			if len(cfg.WorktreeGlobs) > 0 {
				u.Section("Worktree globs")
				for _, g := range cfg.WorktreeGlobs {
					u.Note("  %s", mater.ShortPath(g))
				}
			} else {
				u.Section("Worktree discovery")
				u.Note("  automatic, from %s", mater.ShortPath(cfg.SessionState))
			}
			u.Blank()
			return nil
		},
	}
}

func newConfigInitCmd() *cobra.Command {
	var force bool

	cmd := &cobra.Command{
		Use:   "init",
		Short: "Write a commented configuration file",
		Long: `Write a configuration file in which every key is commented out, so the file
documents the defaults without pinning them.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			path := cfg.Path()
			if exists(path) && !force {
				u.Warning("%s already exists — pass --force to overwrite", mater.ShortPath(path))
				return nil
			}
			if err := writeFile(path, config.Template); err != nil {
				return err
			}
			u.Success("wrote %s", mater.ShortPath(path))
			return nil
		},
	}

	cmd.Flags().BoolVar(&force, "force", false, "overwrite an existing file")
	return cmd
}

func newConfigPathCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "path",
		Short: "Print the configuration file path",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			shared.ui.Println(shared.cfg.Path())
			return nil
		},
	}
}

func boolText(v bool) string {
	if v {
		return "true"
	}
	return "false"
}
