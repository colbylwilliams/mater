package reap

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// maxLogLines caps the log so an unattended machine cannot grow it without
// bound; the oldest half is dropped when the cap is passed.
const maxLogLines = 2000

// Log is an append-only record of delete activity, shared by the foreground
// command and the detached worker.
type Log struct {
	mu   sync.Mutex
	path string
	f    *os.File
}

// OpenLog opens path for appending, creating its directory if needed.
func OpenLog(path string) (*Log, error) {
	if path == "" {
		return &Log{}, nil
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create log directory: %w", err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open log %s: %w", path, err)
	}
	return &Log{path: path, f: f}, nil
}

// Path is the file being written, empty when logging is disabled.
func (l *Log) Path() string {
	if l == nil {
		return ""
	}
	return l.path
}

// Printf appends one timestamped line.
func (l *Log) Printf(format string, args ...any) {
	if l == nil || l.f == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	fmt.Fprintf(l.f, "%s %s\n", time.Now().Format("2006-01-02 15:04:05"), fmt.Sprintf(format, args...))
}

// Close flushes and releases the file.
func (l *Log) Close() error {
	if l == nil || l.f == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	err := l.f.Close()
	l.f = nil
	return err
}

// Tail returns the last n lines of the log, oldest first.
func Tail(path string, n int) []string {
	f, err := os.Open(path)
	if err != nil {
		return nil
	}
	defer f.Close()

	ring := make([]string, 0, n)
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 8<<10), 1<<20)
	for sc.Scan() {
		if len(ring) == n {
			ring = ring[1:]
		}
		ring = append(ring, sc.Text())
	}
	return ring
}

// Rotate trims the log once it passes maxLogLines.
func Rotate(path string) {
	lines := Tail(path, maxLogLines+1)
	if len(lines) <= maxLogLines {
		return
	}
	keep := Tail(path, maxLogLines/2)

	tmp, err := os.CreateTemp(filepath.Dir(path), ".log-*")
	if err != nil {
		return
	}
	defer os.Remove(tmp.Name())

	w := bufio.NewWriter(tmp)
	for _, l := range keep {
		fmt.Fprintln(w, l)
	}
	if w.Flush() != nil || tmp.Close() != nil {
		return
	}
	os.Rename(tmp.Name(), path)
}
