// Package mater discovers, attributes, and classifies reclaimable build output.
package mater

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/colbylwilliams/mater/internal/cargo"
	"github.com/colbylwilliams/mater/internal/config"
	"github.com/colbylwilliams/mater/internal/disk"
	"github.com/colbylwilliams/mater/internal/index"
	"github.com/colbylwilliams/mater/internal/inuse"
	"github.com/colbylwilliams/mater/internal/sessions"
)

// ShortPath abbreviates a home-relative path for display.
func ShortPath(p string) string { return config.Shorten(p) }

// Survey is one complete view of build output on disk.
type Survey struct {
	Items    []Item
	Index    *index.Index
	Sessions *sessions.Set
	Process  *inuse.Snapshot

	// Checkouts are every workspace scanned for target/ and node_modules.
	Checkouts []string
}

// SurveyOptions controls how much work a survey does.
type SurveyOptions struct {
	// SkipRefresh trusts the index as recorded instead of first asking Cargo
	// which build directory each live workspace maps to. That question is what
	// attributes output from a workspace no earlier run has seen, and what
	// re-checks an apparent orphan against every workspace Cargo can still
	// answer for. It costs one `cargo metadata` per Rust checkout, run several
	// at a time, so only a caller asked for an instant answer should skip it.
	SkipRefresh bool

	// DetectInUse takes a process snapshot. Only the commands that delete
	// anything need it.
	DetectInUse bool
}

// Scan builds a full picture of what exists, who owns it, and what is live.
func Scan(ctx context.Context, cfg *config.Config, opts SurveyOptions) (*Survey, error) {
	ix, err := index.Load(cfg.IndexFile())
	if err != nil {
		return nil, err
	}

	sess := sessions.Load(cfg.SessionState)
	checkouts := discoverCheckouts(cfg, sess)

	if !opts.SkipRefresh {
		var rust []string
		for _, c := range checkouts {
			if _, err := os.Stat(filepath.Join(c, "Cargo.toml")); err == nil {
				rust = append(rust, c)
			}
		}
		for _, m := range cargo.ResolveAll(ctx, rust) {
			ix.Set(m.BuildDir, m.Workspace)
		}
	}
	ix.Compact()

	s := &Survey{Index: ix, Sessions: sess, Checkouts: checkouts}
	// The snapshot comes after the Cargo queries, never alongside them: a
	// delete acts on it, so it has to be as fresh as the survey can make it.
	if opts.DetectInUse {
		s.Process = inuse.Take(ctx)
	}

	s.Items = append(s.Items, s.buildDirs(cfg)...)
	s.Items = append(s.Items, s.checkoutDirs(cfg, checkouts)...)

	sort.Slice(s.Items, func(i, j int) bool {
		if a, b := actionOrder(s.Items[i].State), actionOrder(s.Items[j].State); a != b {
			return a < b
		}
		return s.Items[i].Path < s.Items[j].Path
	})
	return s, nil
}

// actionOrder ranks states by how much attention they deserve, so the rows a
// reader can act on are the ones they see first.
func actionOrder(s State) int {
	switch s {
	case StateOrphan:
		return 0
	case StateUnknown:
		return 1
	default:
		return 2
	}
}

// buildDirs enumerates $BUILD_ROOT/<shard>/<hash> and attributes each one.
func (s *Survey) buildDirs(cfg *config.Config) []Item {
	var out []Item

	shards, err := os.ReadDir(cfg.BuildRoot)
	if err != nil {
		return out
	}
	for _, shard := range shards {
		// Staging directories from an in-flight delete are not build output.
		if !shard.IsDir() || strings.HasPrefix(shard.Name(), ".") {
			continue
		}
		shardPath := filepath.Join(cfg.BuildRoot, shard.Name())
		dirs, err := os.ReadDir(shardPath)
		if err != nil {
			continue
		}
		for _, d := range dirs {
			if !d.IsDir() || strings.HasPrefix(d.Name(), ".") {
				continue
			}
			out = append(out, s.describe(Item{
				Path:      filepath.Join(shardPath, d.Name()),
				Kind:      KindBuildDir,
				LastBuilt: disk.LastBuilt(filepath.Join(shardPath, d.Name())),
			}))
		}
	}
	return out
}

// checkoutDirs finds target/ and node_modules still living inside checkouts.
func (s *Survey) checkoutDirs(cfg *config.Config, checkouts []string) []Item {
	var out []Item

	for _, c := range checkouts {
		candidates := []struct {
			name string
			kind Kind
		}{{"target", KindTarget}}
		if cfg.NodeModules() {
			candidates = append(candidates, struct {
				name string
				kind Kind
			}{"node_modules", KindNodeModules})
		}

		for _, cand := range candidates {
			p := filepath.Join(c, cand.name)
			fi, err := os.Lstat(p)
			// A symlinked node_modules points at a store this tool does not own.
			if err != nil || !fi.IsDir() {
				continue
			}
			out = append(out, s.describe(Item{
				Path:      p,
				Kind:      cand.kind,
				Workspace: c,
				LastBuilt: fi.ModTime(),
			}))
		}
	}
	return out
}

// describe fills in ownership, state, and liveness for one item.
func (s *Survey) describe(it Item) Item {
	if it.Workspace == "" {
		if ws, ok := s.Index.Workspace(it.Path); ok {
			it.Workspace = ws
		}
	}

	switch {
	case it.Workspace == "":
		it.State = StateUnknown
	default:
		if _, err := os.Stat(it.Workspace); err != nil {
			it.State = StateOrphan
		} else {
			it.State = StateLive
		}
		if sess, ok := s.Sessions.Lookup(it.Workspace); ok {
			it.Session = sess.Label
			it.Repo = sess.Repo
			it.Branch = sess.Branch
		}
	}

	if s.Process != nil {
		it.InUse = s.Process.Check(it.Path)
	}
	return it
}

// Measure walks the selected items and records their sizes.
func Measure(items []Item, progress func(done, total int)) []Item {
	paths := make([]string, len(items))
	for i, it := range items {
		paths[i] = it.Path
	}
	usage := disk.SizeAll(paths, progress)
	for i := range items {
		if u, ok := usage[items[i].Path]; ok {
			items[i].Size = u.Bytes
			items[i].Sized = true
		}
	}
	return items
}

// TotalSize sums the measured size of items.
func TotalSize(items []Item) int64 {
	var total int64
	for _, it := range items {
		total += it.Size
	}
	return total
}

// discoverCheckouts lists every workspace worth scanning.
//
// Worktree parents are derived from Copilot session state by default rather
// than hard-coded, so a new worktree root is picked up without configuration.
// Globbing each parent, instead of trusting session roots alone, also catches
// worktrees created outside the app.
func discoverCheckouts(cfg *config.Config, sess *sessions.Set) []string {
	seen := map[string]struct{}{}
	var out []string

	add := func(p string) {
		if p == "" {
			return
		}
		if fi, err := os.Stat(p); err != nil || !fi.IsDir() {
			return
		}
		if _, dup := seen[p]; dup {
			return
		}
		seen[p] = struct{}{}
		out = append(out, p)
	}

	if len(cfg.WorktreeGlobs) > 0 {
		for _, g := range cfg.WorktreeGlobs {
			matches, err := filepath.Glob(g)
			if err != nil {
				continue
			}
			for _, m := range matches {
				add(m)
			}
		}
	} else {
		parents := map[string]struct{}{}
		for _, s := range sess.Recent() {
			if !strings.Contains(s.Root, string(filepath.Separator)+worktreeMarker+string(filepath.Separator)) {
				continue
			}
			if fi, err := os.Stat(s.Root); err != nil || !fi.IsDir() {
				continue
			}
			parents[filepath.Dir(s.Root)] = struct{}{}
		}
		dirs := make([]string, 0, len(parents))
		for p := range parents {
			dirs = append(dirs, p)
		}
		sort.Strings(dirs)
		for _, p := range dirs {
			entries, err := os.ReadDir(p)
			if err != nil {
				continue
			}
			for _, e := range entries {
				if e.IsDir() && !strings.HasPrefix(e.Name(), ".") {
					add(filepath.Join(p, e.Name()))
				}
			}
		}
	}

	for _, r := range cfg.Roots {
		add(r)
	}
	return out
}

// worktreeMarker is the path segment that identifies a Copilot worktree parent.
const worktreeMarker = "copilot-worktrees"
