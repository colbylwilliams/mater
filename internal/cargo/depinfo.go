package cargo

import (
	"cmp"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
)

// depInfoGlobs are where a build directory keeps dep-info: under each profile,
// and under each target triple when cross-compiling. Build scripts get a
// directory of their own per unit.
var depInfoGlobs = []string{
	"*/deps/*.d",
	"*/examples/*.d",
	"*/build/*/*.d",
	"*/*/deps/*.d",
	"*/*/examples/*.d",
	"*/*/build/*/*.d",
}

// maxDepInfoSize skips anything too large to be dep-info. Even a crate with
// thousands of modules writes a few hundred kilobytes.
const maxDepInfoSize = 8 << 20

// maxNominees bounds how many directories are offered for one build directory.
// The workspace root is almost always the first; the rest only matter when a
// workspace sat below a directory that was deleted along with it.
const maxNominees = 8

// DepInfoPaths lists the files a build directory's dep-info names by absolute
// path, cleaned and sorted.
//
// rustc writes a Makefile-style .d file for every unit it compiles, naming each
// file the unit read and each environment variable it consulted through env!.
// Cargo runs rustc from the workspace root, so the workspace's own sources are
// recorded relative to it and say nothing about where it lived. What is
// recorded absolutely is anything reached through an absolute path: a
// clippy.toml, a path dependency outside the workspace, or an env! value such
// as CARGO_MANIFEST_DIR. That variable names a directory, so it is reported as
// the manifest inside it, which keeps every path returned a file.
func DepInfoPaths(buildDir string) []string {
	seen := map[string]struct{}{}
	add := func(p string) {
		if filepath.IsAbs(p) {
			seen[filepath.Clean(p)] = struct{}{}
		}
	}

	for _, pattern := range depInfoGlobs {
		files, _ := filepath.Glob(filepath.Join(buildDir, pattern))
		for _, f := range files {
			fi, err := os.Stat(f)
			if err != nil || !fi.Mode().IsRegular() || fi.Size() > maxDepInfoSize {
				continue
			}
			data, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			parseDepInfo(string(data), add)
		}
	}

	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// envEscapes undoes rustc's escaping of env-dep values, which only protects
// newlines and backslashes.
var envEscapes = strings.NewReplacer(`\\`, `\`, `\n`, "\n", `\r`, "\r")

// parseDepInfo reports every path a dep-info file names. Rules read
// `target: dep dep …`, each dependency is repeated as an empty rule of its own,
// and a space inside a path is escaped with a backslash. Comment lines carry
// `# env-dep:NAME=value` for each variable read through env!.
func parseDepInfo(data string, add func(string)) {
	for line := range strings.Lines(data) {
		line = strings.TrimRight(line, "\r\n")

		if env, ok := strings.CutPrefix(line, "# env-dep:"); ok {
			name, value, ok := strings.Cut(env, "=")
			if !ok {
				continue
			}
			value = envEscapes.Replace(value)
			if name == "CARGO_MANIFEST_DIR" {
				value = filepath.Join(value, "Cargo.toml")
			}
			add(value)
			continue
		}
		if strings.HasPrefix(line, "#") {
			continue
		}
		for _, field := range depFields(line) {
			add(strings.TrimSuffix(field, ":"))
		}
	}
}

// depFields splits a rule on the spaces rustc left unescaped.
func depFields(line string) []string {
	var (
		out []string
		b   strings.Builder
	)
	flush := func() {
		if b.Len() > 0 {
			out = append(out, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(line); i++ {
		switch c := line[i]; {
		case c == '\\' && i+1 < len(line) && line[i+1] == ' ':
			b.WriteByte(' ')
			i++
		case c == ' ' || c == '\t':
			flush()
		default:
			b.WriteByte(c)
		}
	}
	flush()
	return out
}

// Nominate lists directories that may have been the workspace root behind
// buildDir, in the order worth probing them. exclude names further trees whose
// contents say nothing about a workspace, such as the build root.
//
// A workspace root sits above every file its build read from inside it, and
// only a file that is gone can speak for a workspace that is gone, so each
// missing directory above a missing file is a nominee. The shallowest come
// first: a deleted worktree takes its whole tree with it, and its root is the
// topmost directory to have gone.
//
// A nominee is only a lead, which Probe either proves or discards, but probing
// writes a stub where the directory was, so nominees are kept to places a
// deleted workspace could plausibly have lived: below the home directory and
// not directly in it, outside Cargo's and rustup's own trees, whose files are
// recorded absolutely but come and go with their caches, and outside any Cargo
// project that still exists. A directory missing from a live project was
// removed from that project, not deleted along with a workspace, and a stub
// inside one is answered for the project rather than for itself.
func Nominate(buildDir string, exclude ...string) []string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return nil
	}
	home = filepath.Clean(home)
	exclude = append([]string{buildDir, cargoHome(home), rustupHome(home)}, exclude...)

	type nominee struct {
		path          string
		depth, weight int
	}
	found := map[string]*nominee{}
	inProject := map[string]bool{}

	for _, p := range DepInfoPaths(buildDir) {
		if !inside(p, home) || slices.ContainsFunc(exclude, func(x string) bool {
			return x != "" && (p == x || inside(p, x))
		}) {
			continue
		}
		if _, err := os.Lstat(p); err == nil {
			continue
		}

		// Collect the missing directories above p, deepest first, stopping at
		// the first one that still exists.
		var missing []string
		dir := filepath.Dir(p)
		for !exists(dir) {
			missing = append(missing, dir)
			dir = filepath.Dir(dir)
		}
		if len(missing) == 0 || dir == home || withinProject(dir, home, inProject) {
			continue
		}

		for i, d := range missing {
			n, ok := found[d]
			if !ok {
				n = &nominee{path: d, depth: len(missing) - i}
				found[d] = n
			}
			n.weight++
		}
	}

	ranked := make([]*nominee, 0, len(found))
	for _, n := range found {
		ranked = append(ranked, n)
	}
	slices.SortFunc(ranked, func(a, b *nominee) int {
		return cmp.Or(
			cmp.Compare(a.depth, b.depth),
			cmp.Compare(b.weight, a.weight),
			strings.Compare(a.path, b.path),
		)
	})

	out := make([]string, 0, min(len(ranked), maxNominees))
	for _, n := range ranked[:min(len(ranked), maxNominees)] {
		out = append(out, n.path)
	}
	return out
}

// withinProject reports whether dir is inside a Cargo project that still
// exists, looking no higher than home. Answers are cached per directory, since
// the missing files of one build share their surviving ancestors.
func withinProject(dir, home string, cache map[string]bool) bool {
	var visited []string
	found := false
	for d := dir; inside(d, home); d = filepath.Dir(d) {
		if v, ok := cache[d]; ok {
			found = v
			break
		}
		visited = append(visited, d)
		if exists(filepath.Join(d, "Cargo.toml")) {
			found = true
			break
		}
	}
	for _, d := range visited {
		cache[d] = found
	}
	return found
}

// cargoHome is where Cargo keeps its registry and git checkouts.
func cargoHome(home string) string {
	return toolHome("CARGO_HOME", filepath.Join(home, ".cargo"))
}

// rustupHome is where rustup keeps its toolchains.
func rustupHome(home string) string {
	return toolHome("RUSTUP_HOME", filepath.Join(home, ".rustup"))
}

func toolHome(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		if abs, err := filepath.Abs(v); err == nil {
			return abs
		}
	}
	return fallback
}

// inside reports whether p lies strictly below dir.
func inside(p, dir string) bool {
	rel, err := filepath.Rel(dir, p)
	return err == nil && rel != "." && rel != ".." &&
		!strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}
