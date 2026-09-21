// Package ui renders mater's terminal output.
package ui

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"

	"charm.land/lipgloss/v2"
	"charm.land/lipgloss/v2/table"
	"github.com/charmbracelet/colorprofile"
	"github.com/charmbracelet/x/term"
)

// UI writes styled output to a single destination.
//
// Colour is resolved once, through a profile-aware writer that downsamples or
// strips escape sequences to match the destination. A piped or redirected run
// therefore degrades to plain text everywhere at the same time, and NO_COLOR is
// honoured without any call site knowing about it.
type UI struct {
	out io.Writer
	err io.Writer
	tty bool

	Heading lipgloss.Style
	Muted   lipgloss.Style
	Strong  lipgloss.Style
	Size    lipgloss.Style
	Path    lipgloss.Style
	Good    lipgloss.Style
	Warn    lipgloss.Style
	Bad     lipgloss.Style
	Info    lipgloss.Style
	Border  lipgloss.Style
}

// New builds a UI for the given streams.
func New(out, errw io.Writer) *UI {
	tty := false
	if f, ok := out.(*os.File); ok {
		tty = term.IsTerminal(f.Fd())
	}

	// The background query needs a real terminal on both ends; anywhere else it
	// reports dark, which is the safer assumption for an unknown destination.
	dark := true
	if tty && term.IsTerminal(os.Stdin.Fd()) {
		dark = lipgloss.HasDarkBackground(os.Stdin, os.Stdout)
	}
	c := lipgloss.LightDark(dark)

	var (
		muted  = c(lipgloss.Color("244"), lipgloss.Color("245"))
		green  = c(lipgloss.Color("28"), lipgloss.Color("78"))
		amber  = c(lipgloss.Color("130"), lipgloss.Color("215"))
		red    = c(lipgloss.Color("160"), lipgloss.Color("203"))
		blue   = c(lipgloss.Color("25"), lipgloss.Color("117"))
		border = c(lipgloss.Color("250"), lipgloss.Color("238"))
	)

	return &UI{
		out:     colorprofile.NewWriter(out, os.Environ()),
		err:     colorprofile.NewWriter(errw, os.Environ()),
		tty:     tty,
		Heading: lipgloss.NewStyle().Bold(true).Foreground(blue),
		Muted:   lipgloss.NewStyle().Foreground(muted),
		Strong:  lipgloss.NewStyle().Bold(true),
		Size:    lipgloss.NewStyle().Bold(true),
		Path:    lipgloss.NewStyle().Foreground(muted),
		Good:    lipgloss.NewStyle().Foreground(green),
		Warn:    lipgloss.NewStyle().Foreground(amber),
		Bad:     lipgloss.NewStyle().Foreground(red),
		Info:    lipgloss.NewStyle().Foreground(blue),
		Border:  lipgloss.NewStyle().Foreground(border),
	}
}

// Interactive reports whether a prompt can be answered.
func (u *UI) Interactive() bool {
	return u.tty && term.IsTerminal(os.Stdin.Fd())
}

// Printf writes a formatted line to the output stream.
func (u *UI) Printf(format string, args ...any) { fmt.Fprintf(u.out, format, args...) }

// Println writes a line to the output stream.
func (u *UI) Println(args ...any) { fmt.Fprintln(u.out, args...) }

// Blank writes an empty line.
func (u *UI) Blank() { fmt.Fprintln(u.out) }

// Section prints a heading with a blank line above it.
func (u *UI) Section(title string) {
	fmt.Fprintf(u.out, "\n%s\n", u.Heading.Render(title))
}

// Note prints a muted remark.
func (u *UI) Note(format string, args ...any) {
	fmt.Fprintf(u.out, "%s\n", u.Muted.Render(fmt.Sprintf(format, args...)))
}

// Success prints a positive result.
func (u *UI) Success(format string, args ...any) {
	fmt.Fprintf(u.out, "%s %s\n", u.Good.Render("✓"), fmt.Sprintf(format, args...))
}

// Warning prints a caution to the output stream, since it accompanies a result
// rather than replacing it.
func (u *UI) Warning(format string, args ...any) {
	fmt.Fprintf(u.out, "%s %s\n", u.Warn.Render("!"), fmt.Sprintf(format, args...))
}

// Failure prints an error to the error stream.
func (u *UI) Failure(format string, args ...any) {
	fmt.Fprintf(u.err, "%s %s\n", u.Bad.Render("✗"), fmt.Sprintf(format, args...))
}

// Table renders rows under a ruled header. Alignment is per column so numbers
// line up on the right and text stays left.
func (u *UI) Table(headers []string, rows [][]string, right map[int]bool) {
	if len(rows) == 0 {
		return
	}

	t := table.New().
		Border(lipgloss.NormalBorder()).
		BorderTop(false).BorderBottom(false).
		BorderLeft(false).BorderRight(false).
		BorderColumn(false).BorderRow(false).
		BorderHeader(true).
		BorderStyle(u.Border).
		Headers(headers...).
		Rows(rows...).
		StyleFunc(func(row, col int) lipgloss.Style {
			s := lipgloss.NewStyle().Padding(0, 1)
			if right[col] {
				s = s.Align(lipgloss.Right)
			}
			if row == table.HeaderRow {
				s = s.Bold(true).Foreground(u.Muted.GetForeground())
			}
			return s
		})
	fmt.Fprintln(u.out, t)
}

// Fields renders aligned key/value pairs, for summaries that are not tabular.
func (u *UI) Fields(pairs [][2]string) {
	width := 0
	for _, p := range pairs {
		if w := lipgloss.Width(p[0]); w > width {
			width = w
		}
	}
	for _, p := range pairs {
		fmt.Fprintf(u.out, "  %s  %s\n",
			u.Muted.Render(fmt.Sprintf("%-*s", width, p[0])), p[1])
	}
}

// Confirm asks a yes/no question. A non-interactive stream is never treated as
// consent; the caller is told to pass --yes instead.
func (u *UI) Confirm(question string) (bool, error) {
	if !u.Interactive() {
		return false, fmt.Errorf("not a terminal — re-run with --yes to proceed without confirming")
	}
	fmt.Fprintf(u.out, "\n%s %s ", question, u.Muted.Render("[y/N]"))

	line, err := bufio.NewReader(os.Stdin).ReadString('\n')
	if err != nil && line == "" {
		return false, nil
	}
	// A pty delivers the line with a trailing carriage return.
	answer := strings.ToLower(strings.TrimSpace(strings.TrimSuffix(line, "\r")))
	return strings.HasPrefix(answer, "y"), nil
}

// Badge renders a short state label in the colour that matches its severity.
func (u *UI) Badge(kind, text string) string {
	switch kind {
	case "orphan":
		return u.Warn.Render(text)
	case "live":
		return u.Good.Render(text)
	case "in-use":
		return u.Info.Render(text)
	default:
		return u.Muted.Render(text)
	}
}
