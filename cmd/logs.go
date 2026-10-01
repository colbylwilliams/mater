package cmd

import (
	"bufio"
	"io"
	"os"
	"strings"
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

			r, err := openLogEnd(cfg.LogFile)
			if err != nil {
				return err
			}
			defer r.Close()
			return tail(r, ctx(cmd).Done(), u.Println)
		},
	}

	cmd.Flags().BoolVarP(&follow, "follow", "f", false, "keep printing as the delete proceeds")
	cmd.Flags().IntVarP(&lines, "lines", "n", 20, "number of trailing lines to show")
	return cmd
}

// pollInterval is how often a followed log is checked for new lines. Polling is
// used rather than a filesystem watcher because the writer is a detached
// process appending a handful of lines a minute, and a watcher would be more
// machinery than the job needs.
const pollInterval = 250 * time.Millisecond

// logReader yields the lines appended to a log after it was opened.
type logReader struct {
	f       *os.File
	r       *bufio.Reader
	partial string
}

// openLogEnd opens the log positioned at its end, so only lines written from
// now on are read.
func openLogEnd(path string) (*logReader, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := f.Seek(0, io.SeekEnd); err != nil {
		f.Close()
		return nil, err
	}
	return &logReader{f: f, r: bufio.NewReader(f)}, nil
}

// emit passes on every complete line read so far. A line caught before its
// newline lands is held back until the rest arrives, rather than split in two.
func (l *logReader) emit(fn func(...any)) error {
	for {
		chunk, err := l.r.ReadString('\n')
		if err == nil {
			fn(l.partial + strings.TrimSuffix(chunk, "\n"))
			l.partial = ""
			continue
		}
		l.partial += chunk
		if err == io.EOF {
			return nil
		}
		return err
	}
}

// Close releases the file. A nil reader is a no-op, so callers that may not
// have a log can defer it unconditionally.
func (l *logReader) Close() error {
	if l == nil {
		return nil
	}
	return l.f.Close()
}

// tail streams new lines until stop is closed, then reads to the end once more.
// The final pass matters when stop means a writer has exited: everything it
// wrote is in the file by then, so its closing lines are never cut off.
func tail(r *logReader, stop <-chan struct{}, emit func(...any)) error {
	t := time.NewTicker(pollInterval)
	defer t.Stop()

	for {
		if err := r.emit(emit); err != nil {
			return err
		}
		select {
		case <-stop:
			return r.emit(emit)
		case <-t.C:
		}
	}
}
