package cmd

import (
	"fmt"

	"github.com/colbylwilliams/mater/internal/disk"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/ui"
)

// itemTable renders a set of items as one row each: what it is, how big, how
// idle, and which work produced it.
func itemTable(u *ui.UI, items []mater.Item, showState bool) {
	if len(items) == 0 {
		return
	}

	headers := []string{"SIZE", "AGE", "KIND"}
	if showState {
		headers = append(headers, "STATE")
	}
	headers = append(headers, "WORK", "LOCATION")

	// Everything except the last two columns is narrow and fixed, so the
	// remaining width is split between the human label and the path.
	fixed := 8 + 6 + 13
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
	for _, it := range items {
		size := u.Muted.Render("—")
		if it.Sized {
			size = u.Size.Render(mater.FormatSize(it.Size))
		}

		age := u.Muted.Render("—")
		if a := it.Age(); a >= 0 {
			age = mater.FormatAge(a)
		}

		work := ui.Truncate(it.Title(), workWidth)
		if it.InUse {
			work += " " + u.Badge("in-use", "(in use)")
		}

		row := []string{size, age, it.Kind.String()}
		if showState {
			row = append(row, u.Badge(it.State.String(), it.State.String()))
		}
		row = append(row, work, u.Path.Render(ui.Elide(it.Detail(), locWidth)))
		rows = append(rows, row)
	}

	u.Table(headers, rows, right)
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

// summarise prints the headline figure for a set of items. Sizes are omitted
// when nothing was measured, so a --fast listing does not report 0B.
func summarise(u *ui.UI, items []mater.Item, orphans int, prefix string) {
	sized := false
	for _, it := range items {
		if it.Sized {
			sized = true
			break
		}
	}

	if sized {
		u.Printf("\n%s %s across %d item%s\n", prefix,
			u.Size.Render(mater.FormatSize(mater.TotalSize(items))),
			len(items), mater.Plural(len(items)))
	} else {
		u.Printf("\n%s %d item%s\n", prefix, len(items), mater.Plural(len(items)))
	}
	if orphans > 0 {
		u.Note("  includes %d orphan%s whose workspace no longer exists", orphans, mater.Plural(orphans))
	}
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

// freeSpace renders the room left on one volume. The mount point is named only
// when there are several volumes to tell apart.
func freeSpace(u *ui.UI, v disk.Volume, named bool) string {
	s := u.Size.Render(mater.FormatSize(v.Free)) + " of " + mater.FormatSize(v.Size)
	if named {
		s += " on " + mater.ShortPath(v.Mount)
	}
	return s
}

// reportFree follows a summary with the room left on each disk, so what is
// listed can be weighed against what is still available.
func reportFree(u *ui.UI, vols []disk.Volume) {
	for _, v := range vols {
		u.Printf("free space: %s\n", freeSpace(u, v, len(vols) > 1))
	}
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
		rows = append(rows, [2]string{key, freeSpace(u, v, len(vols) > 1)})
	}
	return rows
}
