package cmd

import (
	"bytes"
	"regexp"
	"testing"

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
// unless each says which disk it describes.
func TestReportFreeNamesMountsOnlyWhenThereAreSeveral(t *testing.T) {
	tests := []struct {
		name string
		vols []disk.Volume
		want string
	}{
		{"one volume", []disk.Volume{dataVolume}, "free space: 90.0G of 926.0G\n"},
		{"several volumes", []disk.Volume{dataVolume, externalVolume},
			"free space: 90.0G of 926.0G on /System/Volumes/Data\n" +
				"free space: 1.2T of 1.8T on /Volumes/External\n"},
		{"no volume", nil, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			reportFree(ui.New(&out, &out), tt.vols)
			if got := escapeCodes.ReplaceAllString(out.String(), ""); got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
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
