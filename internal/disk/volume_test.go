package disk

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVolumesReportsTheVolumeHoldingAPath(t *testing.T) {
	vols := Volumes(t.TempDir())
	if len(vols) != 1 {
		t.Fatalf("got %d volumes, want 1", len(vols))
	}
	v := vols[0]
	if v.Size <= 0 {
		t.Errorf("Size = %d, want a positive capacity", v.Size)
	}
	if v.Free < 0 || v.Free > v.Size {
		t.Errorf("Free = %d, want between 0 and Size %d", v.Free, v.Size)
	}
	if !filepath.IsAbs(v.Mount) {
		t.Errorf("Mount = %q, want an absolute path", v.Mount)
	}
}

// Every build directory under the build root sits on the same volume, and a
// summary has to show that volume once rather than once per directory.
func TestVolumesCountsEachVolumeOnce(t *testing.T) {
	dir := t.TempDir()
	a, b := filepath.Join(dir, "a"), filepath.Join(dir, "b")
	for _, p := range []string{a, b} {
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if got := Volumes(dir, a, b); len(got) != 1 {
		t.Errorf("got %d volumes for three paths on one filesystem, want 1", len(got))
	}
}

// A build root that has not been created yet still has a volume waiting for
// it: the one its nearest existing parent is on.
func TestVolumesResolvesAMissingPathThroughItsParent(t *testing.T) {
	dir := t.TempDir()
	want := Volumes(dir)
	got := Volumes(filepath.Join(dir, "not", "built", "yet"))
	if len(got) != 1 || len(want) != 1 {
		t.Fatalf("got %d volumes for a missing path, want 1", len(got))
	}
	if got[0].Mount != want[0].Mount || got[0].Size != want[0].Size {
		t.Errorf("missing path resolved to %+v, want its parent's volume %+v", got[0], want[0])
	}
}

// Output split across disks has to report each one, since room on one says
// nothing about the other.
func TestVolumesReportsEachDistinctVolume(t *testing.T) {
	dir := t.TempDir()
	other := otherVolume(t, dir)

	vols := Volumes(dir, other, dir)
	if len(vols) != 2 {
		t.Fatalf("got %d volumes, want 2", len(vols))
	}
	if vols[0].Mount == vols[1].Mount {
		t.Errorf("both volumes report mount %q", vols[0].Mount)
	}
}

func TestVolumesIgnoresAnEmptyPath(t *testing.T) {
	if got := Volumes(""); len(got) != 0 {
		t.Errorf("got %d volumes for an empty path, want 0", len(got))
	}
}

// The walk has to stop exactly at the device boundary: the mount point holds
// the path, and the directory above it belongs to something else.
func TestMountPointStopsAtTheDeviceBoundary(t *testing.T) {
	dir := t.TempDir()
	resolved, err := filepath.EvalSymlinks(dir)
	if err != nil {
		t.Fatal(err)
	}

	m := mountPoint(dir)
	if m != resolved && !strings.HasPrefix(resolved, strings.TrimSuffix(m, "/")+"/") {
		t.Fatalf("mountPoint = %q, which does not contain %q", m, resolved)
	}

	want, _ := device(resolved)
	if got, _ := device(m); got != want {
		t.Errorf("mountPoint %q is on device %d, want %d", m, got, want)
	}
	if parent := filepath.Dir(m); parent != m {
		if got, _ := device(parent); got == want {
			t.Errorf("%q above mountPoint %q is still on the same device", parent, m)
		}
	}
}

// A symlink into another volume is followed, so the target's volume is the one
// described rather than the directory the link sits in.
func TestMountPointFollowsSymlinks(t *testing.T) {
	dir := t.TempDir()
	other := otherVolume(t, dir)
	link := filepath.Join(dir, "link")
	if err := os.Symlink(other, link); err != nil {
		t.Skipf("symlinks unsupported here: %v", err)
	}
	if got, want := mountPoint(link), mountPoint(other); got != want {
		t.Errorf("mountPoint(link to %s) = %q, want %q", other, got, want)
	}
}

// otherVolume finds a directory on a different device from dir. /dev is a
// separate filesystem on both macOS and Linux, which matters on macOS: the
// system and data volumes share one device number, so / does not count.
func otherVolume(t *testing.T, dir string) string {
	t.Helper()
	dirDev, ok := device(dir)
	if !ok {
		t.Fatalf("cannot stat %s", dir)
	}
	for _, p := range []string{"/dev", "/"} {
		if d, ok := device(p); ok && d != dirDev {
			return p
		}
	}
	t.Skip("no second volume to compare against")
	return ""
}
