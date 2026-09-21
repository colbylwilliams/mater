package cmd

import (
	"bufio"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
)

func newLogsCmd() *cobra.Command {
	var (
		follow bool
		lines  int
	)

	cmd := &cobra.Command{
		Use:     "logs",
		GroupID: "inspect",
		Short:   "Show progress of background deletes",
		Long: `Print the delete log.

Deletes run detached, so this is where their per-item progress ends up.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			if !exists(cfg.LogFile) {
				u.Note("no log yet at %s", mater.ShortPath(cfg.LogFile))
				return nil
			}
			for _, line := range reap.Tail(cfg.LogFile, lines) {
				u.Println(line)
			}
			if !follow {
				return nil
			}
			return tail(ctx(cmd).Done(), cfg.LogFile, u.Println)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the delete proceeds")
	cmd.Flags().IntVarP(&lines, "lines", "n", 20, "number of trailing lines to show")
	return cmd
}

// tail streams new lines until the context is cancelled. Polling is used rather
// than a filesystem watcher because the writer is a detached process appending
// a handful of lines a minute, and a watcher would be more machinery than the
// job needs.
func tail(done <-chan struct{}, path string, emit func(...any)) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		return err
	}
	r := bufio.NewReader(f)

	for {
		select {
		case <-done:
			return nil
		default:
		}

		line, err := r.ReadString('\n')
		if len(line) > 0 {
			emit(line[:len(line)-1])
			continue
		}
		if err != nil && err != io.EOF {
			return err
		}
		time.Sleep(250 * time.Millisecond)
	}
}
