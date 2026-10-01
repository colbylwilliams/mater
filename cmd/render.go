package cmd

import (
	"fmt"

	"github.com/colbylwilliams/mater/internal/disk"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/ui"
)

// itemTable renders a set of items as one row each: what it is, how big, how
// idle, and which work produced it.
//
// foot is the ledger to be printed beneath the table, if any. The SIZE column
// is made wide enough for its figures as well as the sizes, and that width is
// returned so the ledger can stand its figures in the same column.
func itemTable(u *ui.UI, items []mater.Item, showState bool, foot [][2]string) int {
	if len(items) == 0 {
		return 0
	}

	sizes := make([]string, len(items))
	sizeWidth := ui.DisplayWidth("SIZE")
	for i, it := range items {
		sizes[i] = u.Muted.Render("—")
		if it.Sized {
			sizes[i] = u.Size.Render(mater.FormatSize(it.Size))
		}
		sizeWidth = max(sizeWidth, ui.DisplayWidth(sizes[i]))
	}
	for _, r := range foot {
		sizeWidth = max(sizeWidth, ui.DisplayWidth(r[0]))
	}

	// A column is as wide as its widest cell, so right-aligning the header to
	// the full width is what reserves room for a figure wider than any size.
	headers := []string{ui.AlignRight("SIZE", sizeWidth), "AGE", "KIND"}
	if showState {
		headers = append(headers, "STATE")
	}
	headers = append(headers, "WORK", "LOCATION")

	// Everything except the last two columns is narrow, so the remaining width
	// is split between the human label and the path. Budgets include padding.
	fixed := sizeWidth + 2 + 6 + 13
	if showState {
		fixed += 10
	}
	avail := u.Width() - fixed - 6
	if avail < 40 {
		avail = 40
	}
	workWidth := avail * 2 / 5
	locWidth := avail - workWidth

	right := map[int]bool{0: true, 1: true}

	rows := make([][]string, 0, len(items))
	for i, it := range items {
		age := u.Muted.Render("—")
		if a := it.Age(); a >= 0 {
			age = mater.FormatAge(a)
		}

		work := ui.Truncate(it.Title(), workWidth)
		if it.InUse {
			work += " " + u.Badge("in-use", "(in use)")
		}

		row := []string{sizes[i], age, it.Kind.String()}
		if showState {
			row = append(row, u.Badge(it.State.String(), it.State.String()))
		}
		row = append(row, work, u.Path.Render(ui.Elide(it.Detail(), locWidth)))
		rows = append(rows, row)
	}

	u.Table(headers, rows, right)
	return sizeWidth
}

// reportSkipped explains what was held back and how to override it.
func reportSkipped(u *ui.UI, skipped []mater.Item) {
	if len(skipped) == 0 {
		return
	}
	u.Warning("skipping %d item%s in use by a running process", len(skipped), mater.Plural(len(skipped)))
	for _, s := range skipped {
		u.Note("    %s", ui.Elide(mater.ShortPath(s.Path), u.Width()-6))
	}
	u.Note("    override with --include-running")
	u.Blank()
}

// footer is the ledger that closes a listing: what the items add up to, then
// the room left on each disk. The total is a dash when nothing was measured, as
// each unmeasured size is, so a --fast listing does not report 0B.
func footer(u *ui.UI, items []mater.Item, orphans int, label string, vols []disk.Volume) [][2]string {
	total := u.Muted.Render("—")
	for _, it := range items {
		if it.Sized {
			total = u.Size.Render(mater.FormatSize(mater.TotalSize(items)))
			break
		}
	}

	rows := [][2]string{{total,
		fmt.Sprintf("%s across %d item%s", label, len(items), mater.Plural(len(items)))}}
	if orphans > 0 {
		rows = append(rows, [2]string{"", u.Muted.Render(fmt.Sprintf(
			"includes %d orphan%s whose workspace no longer exists", orphans, mater.Plural(orphans)))})
	}
	return append(rows, freeRows(u, vols)...)
}

// measure sizes items with a live counter, since walking dozens of build trees
// is slow enough that silence looks like a hang.
func measure(u *ui.UI, items []mater.Item, label string) []mater.Item {
	p := u.Progress(fmt.Sprintf("%s %d item%s", label, len(items), mater.Plural(len(items))))
	out := mater.Measure(items, p.Update)
	p.Done()
	return out
}

// volumes finds the disks holding the build root and items. The build root is
// always included, and first, because the next build writes there whether or
// not anything sits there yet.
func volumes(buildRoot string, items []mater.Item) []disk.Volume {
	paths := make([]string, 0, len(items)+1)
	paths = append(paths, buildRoot)
	for _, it := range items {
		paths = append(paths, it.Path)
	}
	return disk.Volumes(paths...)
}

// capacity names the disk a free-space figure belongs to: its size, and its
// mount point when there are several volumes to tell apart.
func capacity(v disk.Volume, named bool) string {
	s := "of " + mater.FormatSize(v.Size)
	if named {
		s += " on " + mater.ShortPath(v.Mount)
	}
	return s
}

// A disk with less free space than these is running short, and its figure is
// coloured to say so. They are in the same binary units FormatSize prints.
const (
	freeLow      = 100 << 30
	freeCritical = 50 << 30
)

// freeFigure renders the room left on a disk: amber when it is running low,
// red when it is nearly gone.
func freeFigure(u *ui.UI, free int64) string {
	style := u.Size
	switch {
	case free < freeCritical:
		style = style.Foreground(u.Bad.GetForeground())
	case free < freeLow:
		style = style.Foreground(u.Warn.GetForeground())
	}
	return style.Render(mater.FormatSize(free))
}

// freeRows are the ledger rows for the room left on each disk, so what is
// listed can be weighed against what is still available.
func freeRows(u *ui.UI, vols []disk.Volume) [][2]string {
	rows := make([][2]string, 0, len(vols))
	for _, v := range vols {
		rows = append(rows, [2]string{freeFigure(u, v.Free),
			"free " + capacity(v, len(vols) > 1)})
	}
	return rows
}

// freeFields lays out the same figures as status fields, labelling only the
// first row so several volumes read as one entry.
func freeFields(u *ui.UI, vols []disk.Volume) [][2]string {
	rows := make([][2]string, 0, len(vols))
	for i, v := range vols {
		key := ""
		if i == 0 {
			key = "free space"
		}
		rows = append(rows, [2]string{key,
			freeFigure(u, v.Free) + " " + capacity(v, len(vols) > 1)})
	}
	return rows
}
