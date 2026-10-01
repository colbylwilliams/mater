package cmd

import (
	"bytes"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/colbylwilliams/mater/internal/disk"
	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/ui"
)

var escapeCodes = regexp.MustCompile("\x1b\\[[0-9;]*m")

const gib = 1 << 30

var (
	dataVolume     = disk.Volume{Mount: "/System/Volumes/Data", Free: 90 * gib, Size: 926 * gib}
	externalVolume = disk.Volume{Mount: "/Volumes/External", Free: 1200 * gib, Size: 1800 * gib}
)

// A single disk needs no introduction, but figures for several are useless
// unless each says which disk it describes. With no table above them, the
// figures still share a right edge.
func TestFreeRowsNameMountsOnlyWhenThereAreSeveral(t *testing.T) {
	tests := []struct {
		name string
		vols []disk.Volume
		want string
	}{
		{"one volume", []disk.Volume{dataVolume}, "90.0G  free of 926.0G\n"},
		{"several volumes", []disk.Volume{dataVolume, externalVolume},
			"90.0G  free of 926.0G on /System/Volumes/Data\n" +
				" 1.2T  free of 1.8T on /Volumes/External\n"},
		{"no volume", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			u := ui.New(&out, &out)
			u.Ledger(freeRows(u, tt.vols), 0)
			if got := escapeCodes.ReplaceAllString(out.String(), ""); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

// firstField finds where a line's first field starts and ends, in display
// cells, along with the field itself.
func firstField(line string) (field string, start, end int) {
	trimmed := strings.TrimLeft(line, " ")
	field, _, _ = strings.Cut(trimmed, " ")
	start = len(line) - len(trimmed)
	return field, start, start + ui.DisplayWidth(field)
}

// A ledger closing a table stands its figures in the SIZE column, so a total
// reads as the sum of the sizes above it. That has to hold when the sizes are
// the widest thing in the column, when a figure is, and when nothing was
// measured at all.
func TestLedgerFiguresStandInTheSizeColumn(t *testing.T) {
	built := time.Now().Add(-2 * time.Hour)
	tests := []struct {
		name      string
		items     []mater.Item
		orphans   int
		wantTotal string
	}{
		{"sizes are widest", []mater.Item{
			{Path: "/b/1", LastBuilt: built, Size: 140 * gib, Sized: true},
			{Path: "/b/2", LastBuilt: built, Size: 865 << 20, Sized: true, State: mater.StateOrphan},
		}, 1, "140.8G"},
		{"the total is widest", []mater.Item{
			{Path: "/b/1", LastBuilt: built, Size: 5 * gib, Sized: true},
			{Path: "/b/2", LastBuilt: built, Size: 5 * gib, Sized: true},
		}, 0, "10.0G"},
		{"nothing measured", []mater.Item{
			{Path: "/b/1", LastBuilt: built},
			{Path: "/b/2", LastBuilt: built},
		}, 0, "—"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			u := ui.New(&out, &out)
			foot := footer(u, tt.items, tt.orphans, "total", []disk.Volume{dataVolume})
			width := itemTable(u, tt.items, true, foot)
			u.Ledger(foot, width)

			text := strings.TrimRight(escapeCodes.ReplaceAllString(out.String(), ""), "\n")
			lines := strings.Split(text, "\n")
			if want := 2 + len(tt.items) + len(foot); len(lines) != want {
				t.Fatalf("got %d lines, want %d:\n%s", len(lines), want, text)
			}

			header, _, edge := firstField(lines[0])
			if header != "SIZE" {
				t.Fatalf("header leads with %q, want SIZE:\n%s", header, text)
			}
			for _, line := range lines[2 : 2+len(tt.items)] {
				if _, _, end := firstField(line); end != edge {
					t.Errorf("size ends at %d, want %d:\n%s", end, edge, text)
				}
			}

			for i, line := range lines[2+len(tt.items):] {
				field, start, end := firstField(line)
				if foot[i][0] == "" {
					// A continuation carries no figure, so it opens on the text column.
					if start != edge+2 {
						t.Errorf("continuation %q starts at %d, want %d:\n%s", line, start, edge+2, text)
					}
					continue
				}
				if end != edge {
					t.Errorf("figure %q ends at %d, want %d:\n%s", field, end, edge, text)
				}
				if after := strings.TrimLeft(line, " ")[len(field):]; !strings.HasPrefix(after, "  ") || strings.HasPrefix(after, "   ") {
					t.Errorf("text of %q does not start two cells after its figure", line)
				}
			}

			if total, _, _ := firstField(lines[2+len(tt.items)]); total != tt.wantTotal {
				t.Errorf("total = %q, want %q", total, tt.wantTotal)
			}
		})
	}
}

func TestFreeFieldsLabelsTheFirstRowOnly(t *testing.T) {
	var out bytes.Buffer
	rows := freeFields(ui.New(&out, &out), []disk.Volume{dataVolume, externalVolume})

	want := [][2]string{
		{"free space", "90.0G of 926.0G on /System/Volumes/Data"},
		{"", "1.2T of 1.8T on /Volumes/External"},
	}
	if len(rows) != len(want) {
		t.Fatalf("got %d rows, want %d", len(rows), len(want))
	}
	for i := range want {
		got := [2]string{rows[i][0], escapeCodes.ReplaceAllString(rows[i][1], "")}
		if got != want[i] {
			t.Errorf("row %d = %q, want %q", i, got, want[i])
		}
	}
}

// The build root is where the next build writes, so its disk is reported even
// when nothing listed lives there, and ahead of any other.
func TestVolumesLeadWithTheBuildRoot(t *testing.T) {
	buildRoot := t.TempDir()
	root := disk.Volumes(buildRoot)
	if len(root) == 0 {
		// The disk package's own tests pin down which platforms have a backend.
		t.Skip("free space is not read on this platform")
	}

	if got := volumes(buildRoot, nil); len(got) != 1 || got[0].Mount != root[0].Mount {
		t.Errorf("volumes with no items = %+v, want just the build root's %+v", got, root[0])
	}

	elsewhere := "/dev"
	if len(disk.Volumes(buildRoot, elsewhere)) != 2 {
		t.Skipf("%s shares a volume with the temp directory", elsewhere)
	}
	got := volumes(buildRoot, []mater.Item{{Path: elsewhere}, {Path: buildRoot}})
	if len(got) != 2 {
		t.Fatalf("got %d volumes, want 2", len(got))
	}
	if got[0].Mount != root[0].Mount {
		t.Errorf("first volume is %q, want the build root's %q", got[0].Mount, root[0].Mount)
	}
}
