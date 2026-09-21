package ui

import (
	"fmt"
	"sync"
	"time"
)

// Progress reports how far a long measurement has got.
//
// Sizing dozens of build trees takes real time, and a terminal that prints
// nothing for twenty seconds looks hung. On a terminal the line is rewritten in
// place; anywhere else a single line is printed up front so logs stay readable.
type Progress struct {
	u        *UI
	label    string
	mu       sync.Mutex
	last     time.Time
	tty      bool
	finished bool
}

// Progress starts a counter.
func (u *UI) Progress(label string) *Progress {
	p := &Progress{u: u, label: label, tty: u.tty}
	if !p.tty {
		fmt.Fprintf(u.out, "%s…\n", label)
	}
	return p
}

// Update advances the counter. Redraws are throttled so a fast run does not
// spend its time writing escape sequences.
func (p *Progress) Update(done, total int) {
	if p == nil || !p.tty {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	if done < total && time.Since(p.last) < 80*time.Millisecond {
		return
	}
	p.last = time.Now()
	fmt.Fprintf(p.u.out, "\r\033[K%s %s",
		p.u.Muted.Render(p.label),
		p.u.Muted.Render(fmt.Sprintf("%d/%d", done, total)))
}

// Done clears the counter line.
func (p *Progress) Done() {
	if p == nil || !p.tty {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.finished {
		return
	}
	p.finished = true
	fmt.Fprint(p.u.out, "\r\033[K")
}
