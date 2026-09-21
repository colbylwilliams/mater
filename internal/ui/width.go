package ui

import (
	"os"
	"strconv"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/term"
)

// defaultWidth is used when no terminal is attached, so piped output stays
// stable rather than varying with whatever launched it.
const defaultWidth = 100

// Width is the usable terminal width.
func (u *UI) Width() int {
	if !u.tty {
		return defaultWidth
	}
	if w, _, err := term.GetSize(os.Stdout.Fd()); err == nil && w > 0 {
		return w
	}
	if c, err := strconv.Atoi(os.Getenv("COLUMNS")); err == nil && c > 0 {
		return c
	}
	return defaultWidth
}

// Truncate shortens s to max display cells, marking the cut with an ellipsis.
// Cells are counted rather than bytes so wide glyphs in session names do not
// overflow the column.
func Truncate(s string, max int) string {
	if max <= 1 || lipgloss.Width(s) <= max {
		return s
	}
	runes := []rune(s)
	for len(runes) > 0 {
		candidate := string(runes) + "…"
		if lipgloss.Width(candidate) <= max {
			return candidate
		}
		runes = runes[:len(runes)-1]
	}
	return "…"
}

// Elide shortens a path from the left, which keeps the identifying tail
// visible where a prefix like ~/GitHub/github is shared by everything.
func Elide(p string, max int) string {
	if max <= 1 || lipgloss.Width(p) <= max {
		return p
	}
	runes := []rune(p)
	for i := range runes {
		candidate := "…" + string(runes[i:])
		if lipgloss.Width(candidate) <= max {
			return candidate
		}
	}
	return "…"
}
