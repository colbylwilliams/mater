package cargo

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// Resolve runs in every Rust checkout on every survey, with Cargo's output
// discarded. A checkout pinning a toolchain this machine lacks must not have
// rustup fetch it first, whatever the caller's own environment says.
func TestResolveNeverLetsRustupInstallAToolchain(t *testing.T) {
	bin := t.TempDir()
	script := "#!/bin/sh\n" +
		"[ \"$RUSTUP_AUTO_INSTALL\" = 0 ] || exit 1\n" +
		"echo '{\"build_directory\":\"/build/ab/cdef\"}'\n"
	if err := os.WriteFile(filepath.Join(bin, "cargo"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("RUSTUP_AUTO_INSTALL", "1")

	ws := t.TempDir()
	if err := os.WriteFile(filepath.Join(ws, "Cargo.toml"), []byte("[workspace]\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	got, ok := Resolve(context.Background(), ws)
	if !ok || got != "/build/ab/cdef" {
		t.Errorf("Resolve = %q, %v; want cargo run with RUSTUP_AUTO_INSTALL=0", got, ok)
	}
}
