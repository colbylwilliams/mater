package cmd

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/colbylwilliams/mater/internal/mater"
	"github.com/colbylwilliams/mater/internal/reap"
	"github.com/colbylwilliams/mater/internal/ui"
)

type verdict int

const (
	pass verdict = iota
	warn
	fail
	info
)

type check struct {
	name   string
	result verdict
	detail string
	fix    string
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:     "doctor",
		GroupID: "setup",
		Short:   "Check that the build root and index are set up correctly",
		Long: `Verify the pieces mater depends on.

The central requirement is that Cargo writes its intermediates into one stable
directory instead of a target/ inside every checkout. That single path is what
makes a real-time scanning exclusion possible: worktrees come and go, but the
build root does not.

Nothing here changes your configuration. Where a fix is needed, the command to
run is printed for you to apply.`,
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			u, cfg := shared.ui, shared.cfg

			checks := []check{
				checkCargo(),
				checkBuildDir(cfg.BuildRoot),
				checkBuildRoot(cfg.BuildRoot),
				checkExclusion(cfg.BuildRoot),
				checkSessionState(cfg.SessionState),
				checkTooling(),
				checkStaging(cfg.BuildRoot),
			}

			// Doctor records what it learned like every command, but reports a
			// failure to write as the index check's verdict, not as a warning.
			sv, err := mater.Scan(ctx(cmd), cfg, mater.SurveyOptions{})
			if err == nil {
				checks = append(checks, checkIndex(sv, sv.Index.Save()))
			}

			u.Section("Checks")
			rows := make([][]string, 0, len(checks))
			width := u.Width() - 32
			if width < 40 {
				width = 40
			}
			for _, c := range checks {
				rows = append(rows, []string{
					verdictMark(u, c.result),
					c.name,
					ui.Truncate(c.detail, width),
				})
			}
			u.Table([]string{"", "CHECK", "RESULT"}, rows, nil)

			var fixes []check
			for _, c := range checks {
				if c.fix != "" {
					fixes = append(fixes, c)
				}
			}
			if len(fixes) > 0 {
				u.Section("Suggested")
				for _, c := range fixes {
					u.Printf("  %s\n", u.Strong.Render(c.name))
					for _, line := range strings.Split(c.fix, "\n") {
						u.Printf("    %s\n", u.Info.Render(line))
					}
					u.Blank()
				}
			} else {
				u.Blank()
				u.Success("everything checks out")
			}
			return nil
		},
	}
}

func verdictMark(u *ui.UI, v verdict) string {
	switch v {
	case pass:
		return u.Good.Render("✓")
	case warn:
		return u.Warn.Render("!")
	case fail:
		return u.Bad.Render("✗")
	default:
		return u.Muted.Render("·")
	}
}

func checkCargo() check {
	out, err := exec.Command("cargo", "--version").Output()
	if err != nil {
		return check{
			name:   "cargo",
			result: fail,
			detail: "not found on PATH",
			fix:    "Install Rust: https://rustup.rs",
		}
	}
	return check{name: "cargo", result: pass, detail: strings.TrimSpace(string(out))}
}

// checkBuildDir confirms Cargo is configured to write outside the checkout.
func checkBuildDir(buildRoot string) check {
	home, _ := os.UserHomeDir()
	path := filepath.Join(home, ".cargo", "config.toml")

	value, ok := cargoBuildDir(path)
	if !ok {
		return check{
			name:   "build.build-dir",
			result: fail,
			detail: "not set — Cargo is still writing target/ inside each checkout",
			fix: fmt.Sprintf("Add to %s:\n\n  [build]\n  build-dir = \"%s/{workspace-path-hash}\"",
				mater.ShortPath(path), buildRoot),
		}
	}

	// Everything before the first template placeholder is the stable prefix,
	// and that prefix is what has to match the build root.
	prefix := strings.TrimRight(strings.SplitN(value, "{", 2)[0], "/")
	if prefix != strings.TrimRight(buildRoot, "/") {
		return check{
			name:   "build.build-dir",
			result: fail,
			detail: fmt.Sprintf("points at %s, but mater manages %s",
				mater.ShortPath(prefix), mater.ShortPath(buildRoot)),
			fix: fmt.Sprintf("Set build_root in the mater config to %s, or repoint Cargo at %s",
				mater.ShortPath(prefix), mater.ShortPath(buildRoot)),
		}
	}
	if !strings.Contains(value, "{") {
		return check{
			name:   "build.build-dir",
			result: warn,
			detail: "no path template — every workspace would share one build directory",
			fix: fmt.Sprintf("Use a per-workspace path in %s:\n\n  build-dir = \"%s/{workspace-path-hash}\"",
				mater.ShortPath(path), buildRoot),
		}
	}
	return check{name: "build.build-dir", result: pass, detail: mater.ShortPath(value)}
}

// cargoBuildDir reads build.build-dir from a Cargo config. Only one key from
// one table is needed, so the file is scanned rather than fully parsed.
func cargoBuildDir(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()

	section := ""
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "[") {
			section = strings.Trim(line, "[]")
			continue
		}
		if section != "build" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "build-dir" {
			continue
		}
		value = strings.TrimSpace(value)
		if i := strings.Index(value, "#"); i >= 0 {
			value = strings.TrimSpace(value[:i])
		}
		return strings.Trim(value, `"'`), true
	}
	return "", false
}

func checkBuildRoot(buildRoot string) check {
	fi, err := os.Stat(buildRoot)
	switch {
	case err != nil:
		return check{
			name:   "build root",
			result: warn,
			detail: mater.ShortPath(buildRoot) + " does not exist yet",
			fix:    "It is created by the first build. Nothing to do.",
		}
	case !fi.IsDir():
		return check{name: "build root", result: fail, detail: mater.ShortPath(buildRoot) + " is not a directory"}
	}

	entries, err := os.ReadDir(buildRoot)
	if err != nil {
		return check{name: "build root", result: fail, detail: err.Error()}
	}
	shards := 0
	for _, e := range entries {
		if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
			shards++
		}
	}
	return check{
		name:   "build root",
		result: pass,
		detail: fmt.Sprintf("%s — %d shard%s", mater.ShortPath(buildRoot), shards, mater.Plural(shards)),
	}
}

// checkExclusion reports the one path that has to be exempted from real-time
// scanning. mater never queries or changes Defender: the command is printed for
// you to run, so tamper protection is never in the way.
func checkExclusion(buildRoot string) check {
	return check{
		name:   "scanner exclusion",
		result: info,
		detail: "exclude " + mater.ShortPath(buildRoot) + " once; worktrees need no further changes",
		fix:    "mdatp exclusion folder add --path " + buildRoot,
	}
}

func checkSessionState(dir string) check {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return check{
			name:   "session state",
			result: warn,
			detail: mater.ShortPath(dir) + " unreadable — build dirs will show as unattributed",
		}
	}
	return check{
		name:   "session state",
		result: pass,
		detail: fmt.Sprintf("%d session%s in %s", len(entries), mater.Plural(len(entries)), mater.ShortPath(dir)),
	}
}

// checkTooling verifies the liveness probes are available. Without them a
// delete cannot tell that a process is using its target.
func checkTooling() check {
	var missing []string
	for _, tool := range []string{"lsof", "ps", "pgrep", "git"} {
		if _, err := exec.LookPath(tool); err != nil {
			missing = append(missing, tool)
		}
	}
	if len(missing) > 0 {
		return check{
			name:   "liveness probes",
			result: warn,
			detail: "missing " + strings.Join(missing, ", ") + " — in-use detection is degraded",
		}
	}
	return check{name: "liveness probes", result: pass, detail: "lsof, ps, pgrep, git available"}
}

func checkStaging(buildRoot string) check {
	leftovers := reap.Leftovers(buildRoot)
	if len(leftovers) == 0 {
		return check{name: "staging", result: pass, detail: "no delete in progress"}
	}
	pending := 0
	for _, l := range leftovers {
		pending += reap.Pending(l)
	}
	return check{
		name:   "staging",
		result: info,
		detail: fmt.Sprintf("%d item%s waiting to be deleted", pending, mater.Plural(pending)),
		fix:    "Follow along with 'mater logs --follow', or let the next prune adopt it.",
	}
}

func checkIndex(sv *mater.Survey, saveErr error) check {
	if saveErr != nil {
		cause := saveErr
		var pe *fs.PathError
		if errors.As(saveErr, &pe) {
			cause = pe.Err
		}
		return check{
			name:   "index",
			result: warn,
			detail: fmt.Sprintf("not writable (%v) — nothing new is recorded", cause),
		}
	}

	unknown := 0
	for _, it := range sv.Items {
		if it.Kind == mater.KindBuildDir && it.Workspace == "" {
			unknown++
		}
	}
	if unknown > 0 {
		return check{
			name:   "index",
			result: warn,
			detail: fmt.Sprintf("%d of %d build director%s unattributed", unknown, sv.Index.Len()+unknown,
				map[bool]string{true: "y", false: "ies"}[unknown == 1]),
			fix: "mater index bootstrap",
		}
	}
	return check{
		name:   "index",
		result: pass,
		detail: fmt.Sprintf("%d mapping%s recorded", sv.Index.Len(), mater.Plural(sv.Index.Len())),
	}
}

// writeFile creates path and its parent directory.
func writeFile(path, content string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("create directory: %w", err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}
	return nil
}
