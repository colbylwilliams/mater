package cmd

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/colbylwilliams/mater/internal/ui"
)

// appendLog writes s to the end of the log at path, the way the worker does.
// It reports failure rather than failing the test, so a fake worker running on
// another goroutine can call it.
func appendLog(path, s string) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(s)
	return err
}

// openTestLog creates a log holding history and opens a reader at its end.
func openTestLog(t *testing.T, history string) (string, *logReader) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "mater.log")
	if err := appendLog(path, history); err != nil {
		t.Fatal(err)
	}
	r, err := openLogEnd(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { r.Close() })
	return path, r
}

// lines splits watch output into its non-empty lines.
func lines(out string) []string {
	var got []string
	for _, l := range strings.Split(escapeCodes.ReplaceAllString(out, ""), "\n") {
		if l != "" {
			got = append(got, l)
		}
	}
	return got
}

// A watch begins where the log ends, so lines from earlier runs are not
// replayed as this delete's progress.
func TestLogReaderStartsAtTheEnd(t *testing.T) {
	path, r := openTestLog(t, "delete finished: an earlier run\n")
	if err := appendLog(path, "delete started: 1 item\n"); err != nil {
		t.Fatal(err)
	}

	var got []string
	if err := r.emit(func(a ...any) { got = append(got, fmt.Sprint(a...)) }); err != nil {
		t.Fatal(err)
	}
	if want := []string{"delete started: 1 item"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A line caught mid-write comes out whole once its newline lands, not as two
// fragments.
func TestLogReaderHoldsBackAPartialLine(t *testing.T) {
	path, r := openTestLog(t, "")
	var got []string
	emit := func(a ...any) { got = append(got, fmt.Sprint(a...)) }

	if err := appendLog(path, "  [1/2]  3.2G  ~/.rust-build/ab"); err != nil {
		t.Fatal(err)
	}
	if err := r.emit(emit); err != nil {
		t.Fatal(err)
	}
	if len(got) != 0 {
		t.Fatalf("emitted %q before the line was complete", got)
	}

	if err := appendLog(path, "/abcdef  (3s)\n"); err != nil {
		t.Fatal(err)
	}
	if err := r.emit(emit); err != nil {
		t.Fatal(err)
	}
	if want := []string{"  [1/2]  3.2G  ~/.rust-build/ab/abcdef  (3s)"}; !slices.Equal(got, want) {
		t.Errorf("got %q, want %q", got, want)
	}
}

// The worker writes its closing line moments before it exits, usually between
// two polls. The watch still has to print it before returning.
func TestWatchPrintsTheWorkersClosingLine(t *testing.T) {
	path, r := openTestLog(t, "")
	var out bytes.Buffer

	err := watch(context.Background(), ui.New(&out, &out), r, func() error {
		if err := appendLog(path, "delete started: 1 item\n"); err != nil {
			return err
		}
		// Well inside one poll interval, so only the final read can see what
		// follows.
		time.Sleep(pollInterval / 5)
		return appendLog(path, "delete finished: 4K in 1 item, 0s\n")
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}

	want := []string{"delete started: 1 item", "delete finished: 4K in 1 item, 0s"}
	if got := lines(out.String()); !slices.Equal(got, want) {
		t.Errorf("printed %q, want %q", got, want)
	}
}

// A worker that ends badly fails the command, so a script chaining on the
// watch does not carry on as if the space had been freed.
func TestWatchReportsAWorkerThatEndedBadly(t *testing.T) {
	_, r := openTestLog(t, "")
	var out bytes.Buffer
	failure := errors.New("background delete ended with exit status 1")

	err := watch(context.Background(), ui.New(&out, &out), r, func() error { return failure })
	if !errors.Is(err, failure) {
		t.Fatalf("watch = %v, want it to wrap %v", err, failure)
	}
}

// An interrupt ends the watch at once, without waiting for the worker, and is
// not a failure: the delete is still running, which is what was asked for.
func TestInterruptEndsTheWatchButNotTheWorker(t *testing.T) {
	_, r := openTestLog(t, "")
	var out bytes.Buffer

	release := make(chan struct{})
	defer close(release)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	done := make(chan error, 1)
	go func() {
		done <- watch(ctx, ui.New(&out, &out), r, func() error {
			<-release
			return nil
		})
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("watch = %v, want nil after an interrupt", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("watch was still waiting on the worker after an interrupt")
	}
	if !strings.Contains(out.String(), "carries on in the background") {
		t.Errorf("output %q does not say the delete carries on", out.String())
	}
}

// Without a log to read the watch shows no progress, but it still lasts as long
// as the worker does.
func TestWatchWithoutALogStillWaitsForTheWorker(t *testing.T) {
	var out bytes.Buffer
	var exited atomic.Bool

	err := watch(context.Background(), ui.New(&out, &out), nil, func() error {
		time.Sleep(pollInterval / 5)
		exited.Store(true)
		return nil
	})
	if err != nil {
		t.Fatalf("watch: %v", err)
	}
	if !exited.Load() {
		t.Error("watch returned before the worker exited")
	}
}
