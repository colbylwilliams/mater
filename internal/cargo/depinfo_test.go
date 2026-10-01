package cargo

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// depInfo renders a dep-info file the way rustc writes one: a rule per output,
// an empty rule per dependency, and env-dep comments after.
func depInfo(outputs []string, deps []string, env ...string) string {
	escaped := make([]string, len(deps))
	for i, d := range deps {
		escaped[i] = strings.ReplaceAll(d, " ", `\ `)
	}
	var b strings.Builder
	for _, o := range outputs {
		fmt.Fprintf(&b, "%s: %s\n\n", o, strings.Join(escaped, " "))
	}
	for _, d := range escaped {
		fmt.Fprintf(&b, "%s:\n", d)
	}
	b.WriteString("\n")
	for _, e := range env {
		fmt.Fprintf(&b, "# env-dep:%s\n", e)
	}
	return b.String()
}

// sandboxHome points the home directory, and Cargo's and rustup's below it,
// at a fresh directory so nominations can be checked against a known tree.
func sandboxHome(t *testing.T) string {
	t.Helper()
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)
	t.Setenv("CARGO_HOME", filepath.Join(home, ".cargo"))
	t.Setenv("RUSTUP_HOME", filepath.Join(home, ".rustup"))
	return home
}

func TestDepInfoPathsReadsRulesAndEnvDeps(t *testing.T) {
	bd := t.TempDir()
	writeFile(t, filepath.Join(bd, "debug", "deps", "a-1.d"), depInfo(
		[]string{bd + "/debug/deps/a-1.d", bd + "/debug/deps/liba-1.rmeta"},
		[]string{"/ws/clippy.toml", "src/lib.rs", "/with space/x.rs", "/ws/crates/../crates/a/src/lib.rs"},
		"CARGO_MANIFEST_DIR=/ws/crates/a",
		"CLIPPY_CONF_DIR",
		"CLIPPY_ARGS=--no-deps__CLIPPY_HACKERY__",
		`ODD=/back\\slash`,
	)+"# checksum:blake3=00 file_len:1 /ws/from-a-comment.rs\n")
	writeFile(t, filepath.Join(bd, "aarch64-apple-darwin", "debug", "deps", "b-2.d"),
		depInfo([]string{bd + "/b-2.d"}, []string{"/cross/lib.rs"}))
	writeFile(t, filepath.Join(bd, "debug", "build", "c-3", "build_script_build-3.d"),
		depInfo([]string{bd + "/c-3.d"}, []string{"/ws/build.rs"}))
	writeFile(t, filepath.Join(bd, "debug", "examples", "d-4.d"),
		depInfo([]string{bd + "/d-4.d"}, []string{"/ws/examples/d.rs"}))
	writeFile(t, filepath.Join(bd, "debug", "incremental", "a-1", "s-1.d"),
		depInfo([]string{bd + "/s-1.d"}, []string{"/not/dep-info/incremental.rs"}))
	writeFile(t, filepath.Join(bd, "debug", "deps", "notes.txt"), "/not/dep-info/notes.rs:\n")

	got := DepInfoPaths(bd)

	for _, want := range []string{
		"/ws/clippy.toml",
		"/with space/x.rs",
		"/ws/crates/a/src/lib.rs",
		"/ws/crates/a/Cargo.toml",
		`/back\slash`,
		"/cross/lib.rs",
		"/ws/build.rs",
		"/ws/examples/d.rs",
		bd + "/debug/deps/a-1.d",
	} {
		if !slices.Contains(got, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, unwanted := range []string{
		"src/lib.rs",
		"/ws/crates/a",
		"/ws/from-a-comment.rs",
		"/not/dep-info/incremental.rs",
		"/not/dep-info/notes.rs",
	} {
		if slices.Contains(got, unwanted) {
			t.Errorf("unexpectedly reported %q", unwanted)
		}
	}
	if !slices.IsSorted(got) {
		t.Errorf("paths are not sorted: %v", got)
	}
}

// Every filter exists to keep a probe from writing a stub somewhere a deleted
// workspace could not have been, so each one is exercised here alongside the
// one lead that should survive them all.
func TestNominateOffersMissingDirectoriesShallowestFirst(t *testing.T) {
	home := sandboxHome(t)
	buildRoot := filepath.Join(home, ".rust-build")
	bd := filepath.Join(buildRoot, "ab", "0123456789abcd")

	work := filepath.Join(home, "work")
	live := filepath.Join(home, "live")
	writeFile(t, filepath.Join(work, "present.rs"), "")
	writeFile(t, filepath.Join(live, "Cargo.toml"), "[workspace]\n")
	ws := filepath.Join(work, "wt")

	writeFile(t, filepath.Join(bd, "debug", "deps", "a-1.d"), depInfo(
		[]string{bd + "/debug/deps/a-1.d"},
		[]string{
			ws + "/clippy.toml",
			ws + "/crates/a/src/lib.rs",
			"src/relative.rs",
			work + "/present.rs",                     // still on disk
			work + "/vanished.rs",                    // gone, but its directory was not
			live + "/crates/removed/src/lib.rs",      // removed from a project that still exists
			home + "/gone/x.rs",                      // would recreate a directory directly in home
			home + "/.cargo/registry/src/idx/s/a.rs", // Cargo's cache
			home + "/.rustup/toolchains/t/lib.rs",    // rustup's toolchains
			buildRoot + "/cd/ef/debug/build/x/out/g.rs",
			"/outside-home/x.rs",
		},
		"CARGO_MANIFEST_DIR="+ws+"/crates/a",
	))

	got := Nominate(bd, buildRoot)
	want := []string{
		ws,
		filepath.Join(ws, "crates"),
		filepath.Join(ws, "crates", "a"),
		filepath.Join(ws, "crates", "a", "src"),
	}
	if !slices.Equal(got, want) {
		t.Errorf("Nominate =\n  %v\nwant\n  %v", got, want)
	}
}

// A workspace that is a single package names its root only through
// CARGO_MANIFEST_DIR, with nothing missing above it.
func TestNominateFindsASinglePackageWorkspace(t *testing.T) {
	home := sandboxHome(t)
	bd := filepath.Join(home, ".rust-build", "ab", "0123456789abcd")
	ws := filepath.Join(home, "work", "solo")
	if err := os.MkdirAll(filepath.Dir(ws), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(bd, "debug", "deps", "solo-1.d"),
		depInfo([]string{bd + "/solo-1.d"}, []string{"src/lib.rs"}, "CARGO_MANIFEST_DIR="+ws))

	if got := Nominate(bd); !slices.Equal(got, []string{ws}) {
		t.Errorf("Nominate = %v, want [%s]", got, ws)
	}
}

// A probe writes through symlinks, so where a stub would land is decided by
// the physical path of the nearest surviving directory, not by how dep-info
// spelled it.
func TestNominateJudgesWhereAStubWouldLand(t *testing.T) {
	home := sandboxHome(t)
	elsewhere, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	bd := filepath.Join(home, ".rust-build", "ab", "0123456789abcd")
	live := filepath.Join(home, "live")
	writeFile(t, filepath.Join(live, "Cargo.toml"), "[workspace]\n")
	if err := os.MkdirAll(filepath.Join(live, "crates"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(home, ".cargo", "registry", "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"away":     elsewhere,                                        // outside home
		"cache":    filepath.Join(home, ".cargo", "registry", "src"), // into Cargo's cache
		"inner":    filepath.Join(live, "crates"),                    // into a live project
		"homeward": home,                                             // straight back to home
	} {
		if err := os.Symlink(target, filepath.Join(home, link)); err != nil {
			t.Fatal(err)
		}
	}

	writeFile(t, filepath.Join(bd, "debug", "deps", "a-1.d"), depInfo(
		[]string{bd + "/debug/deps/a-1.d"},
		[]string{
			home + "/away/gone/clippy.toml",
			home + "/cache/idx/serde/src/lib.rs",
			home + "/inner/removed/src/lib.rs",
			home + "/homeward/gone/clippy.toml",
		},
	))

	if got := Nominate(bd); len(got) != 0 {
		t.Errorf("Nominate offered %v, want nothing", got)
	}
}

func TestNominateIsBounded(t *testing.T) {
	home := sandboxHome(t)
	bd := filepath.Join(home, ".rust-build", "ab", "0123456789abcd")
	work := filepath.Join(home, "work")
	if err := os.MkdirAll(work, 0o755); err != nil {
		t.Fatal(err)
	}
	var deps []string
	for i := range 3 * maxNominees {
		deps = append(deps, fmt.Sprintf("%s/wt-%02d/clippy.toml", work, i))
	}
	writeFile(t, filepath.Join(bd, "debug", "deps", "a-1.d"), depInfo([]string{bd + "/a-1.d"}, deps))

	if got := Nominate(bd); len(got) != maxNominees {
		t.Errorf("Nominate offered %d directories, want %d", len(got), maxNominees)
	}
}

// The point of a nominee is that Cargo can confirm it. This asks real Cargo
// for the build directory of a workspace, deletes the workspace, and checks
// that the nominee read back from dep-info probes to that same directory.
func TestNomineeProbesBackToItsBuildDirectory(t *testing.T) {
	if _, err := exec.LookPath("cargo"); err != nil {
		t.Skip("cargo is not installed")
	}
	realHome, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	// Moving HOME must not hide the toolchain from rustup's cargo proxy.
	t.Setenv("CARGO_HOME", cargoHome(realHome))
	t.Setenv("RUSTUP_HOME", rustupHome(realHome))
	home, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("HOME", home)

	work := filepath.Join(home, "work")
	root := filepath.Join(home, "build-root")
	writeFile(t, filepath.Join(work, ".cargo", "config.toml"),
		fmt.Sprintf("[build]\nbuild-dir = %q\n", root+"/{workspace-path-hash}"))

	ws := filepath.Join(work, "wt")
	writeFile(t, filepath.Join(ws, "Cargo.toml"),
		"[package]\nname = \"wt\"\nversion = \"0.0.0\"\nedition = \"2021\"\n\n[lib]\npath = \"lib.rs\"\n")
	writeFile(t, filepath.Join(ws, "lib.rs"), "")

	ctx := context.Background()
	bd, ok := Resolve(ctx, ws)
	if !ok || !inside(bd, root) {
		t.Skipf("cargo does not honour build.build-dir here (resolved %q)", bd)
	}
	writeFile(t, filepath.Join(bd, "debug", "deps", "wt-0123456789abcdef.d"), depInfo(
		[]string{bd + "/debug/deps/wt-0123456789abcdef.d"},
		[]string{"lib.rs"},
		"CARGO_MANIFEST_DIR="+ws,
	))
	if err := os.RemoveAll(ws); err != nil {
		t.Fatal(err)
	}

	nominees := Nominate(bd, root)
	if !slices.Equal(nominees, []string{ws}) {
		t.Fatalf("Nominate = %v, want [%s]", nominees, ws)
	}
	got := ProbeAll(ctx, nominees)
	if len(got) != 1 || got[0].BuildDir != bd || got[0].Workspace != ws {
		t.Fatalf("ProbeAll = %+v, want %s → %s", got, ws, bd)
	}
	if _, err := os.Lstat(ws); err == nil {
		t.Error("the probe left the workspace path behind")
	}
}
