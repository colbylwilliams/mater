// Package cmd wires mater's subcommands.
package cmd

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/config"
	"github.com/colbylwilliams/mater/internal/ui"
)

// env is the shared state every command needs: resolved settings and a styled
// writer. It is built once in the root's PersistentPreRunE.
type env struct {
	cfg *config.Config
	ui  *ui.UI
}

var (
	configPath string
	shared     = &env{}
)

// Root builds the command tree.
func Root() *cobra.Command {
	root := &cobra.Command{
		Use:   "mater",
		Short: "Keep Rust and Node build output in one place, and reclaim it",
		Long: `mater funnels Rust build output into a single build root and reclaims it
on demand.

Cargo normally writes intermediates into a target/ directory inside every
checkout. Across dozens of worktrees that is both enormous and impossible to
exempt from real-time malware scanning, because the paths keep changing.
Pointing build.build-dir at one stable root fixes both problems at once: the
bytes land in a single place, and that place can be excluded once.

mater then keeps track of which workspace produced each build directory, so
output whose worktree has been deleted can be identified and removed safely.

Run 'mater doctor' first to check the setup.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			shared.cfg = cfg
			shared.ui = ui.New(cmd.OutOrStdout(), cmd.ErrOrStderr())
			return nil
		},
	}

	root.PersistentFlags().StringVar(&configPath, "config", "",
		fmt.Sprintf("config file (default %s)", config.Shorten(config.DefaultPath())))

	root.AddGroup(
		&cobra.Group{ID: "inspect", Title: "Inspecting"},
		&cobra.Group{ID: "reclaim", Title: "Reclaiming"},
		&cobra.Group{ID: "setup", Title: "Setup"},
	)

	root.AddCommand(
		newStatusCmd(),
		newListCmd(),
		newPruneCmd(),
		newNukeCmd(),
		newRestoreCmd(),
		newIndexCmd(),
		newLogsCmd(),
		newDoctorCmd(),
		newConfigCmd(),
		newReapCmd(),
	)
	return root
}

// ctx returns the command's context, falling back to a background context when
// the command was constructed without one.
func ctx(cmd *cobra.Command) context.Context {
	if c := cmd.Context(); c != nil {
		return c
	}
	return context.Background()
}

// exists is a small readability helper used by the setup commands.
func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// globalFlags are the persistent flags a re-executed worker needs in order to
// resolve the same configuration as the process that started it.
func globalFlags() []string {
	if configPath == "" {
		return nil
	}
	return []string{"--config", configPath}
}
