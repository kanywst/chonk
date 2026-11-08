// Package scan walks the machine and classifies what it finds.
package scan

import (
	"context"
	"os"
	"path/filepath"
	"slices"
	"time"

	"github.com/kanywst/chonk/internal/gitstate"
	"github.com/kanywst/chonk/internal/history"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/pins"
	"github.com/kanywst/chonk/internal/procs"
	"github.com/kanywst/chonk/internal/system"
	"github.com/kanywst/chonk/internal/walk"
	"github.com/kanywst/chonk/internal/workspaces"
)

// Roots are the places chonk looks: your home, apps, and where package
// managers install to. Ones that do not exist are skipped.
func Roots(home string) []string {
	return []string{
		home, "/Applications", "/opt/homebrew", "/usr/local", "/nix",
		// Outside your files, but with one known owner and a real size:
		// applied update installers, and the hibernation image.
		"/Library/Updates", "/private/var/vm",
	}
}

// Run walks roots and classifies them. Local APFS snapshots, which no
// directory shows, are added as an item of their own, and git is asked what
// each project holds that only this disk has.
func Run(ctx context.Context, home string, roots []string, prog *walk.Progress) *owner.Report {
	nodes := walk.Walk(ctx, roots, walk.Options{MinKeep: 1 << 20, Skip: skip}, prog)
	rep := owner.Classify(nodes, owner.Options{})
	if it := system.SnapshotItem(system.Snapshots(ctx, system.Runner(procs.Exec))); it != nil {
		rep.Items = append(rep.Items, it)
	}
	projects := rep.Projects()
	rep.ApplyGit(gitstate.Collect(ctx, gitstate.Runner(home), projects))
	pins.Apply(rep, pins.Collect(home, projects))
	workspaces.Apply(rep)
	return rep
}

// skip keeps the walk out of places that are not on this disk, or that
// macOS fills with views of other places.
func skip(path string) bool {
	switch filepath.Base(path) {
	case "CloudStorage", "Mobile Documents":
		// iCloud and file-provider mounts: mostly placeholders for files
		// that live elsewhere, and reading them can trigger downloads.
		return filepath.Base(filepath.Dir(path)) == "Library"
	}
	return false
}

// Snapshot is what history keeps of a report.
func Snapshot(r *owner.Report, now time.Time) history.Snapshot {
	s := history.Snapshot{Time: now, Total: r.Total, Items: map[string]int64{}}
	partial := map[string]bool{}
	for _, it := range r.Items {
		if it.SizeUnknown {
			continue
		}
		k := owner.Tilde(it.Path)
		s.Items[k] += it.Size
		if denied(it.Node) && !partial[k] {
			partial[k] = true
			s.Partial = append(s.Partial, k)
		}
	}
	slices.Sort(s.Partial)
	return s
}

// denied reports whether n or a directory under it could not be read.
// Unreadable directories are kept in the tree whatever their size, so this
// sees all of them.
func denied(n *walk.Node) bool {
	if n == nil {
		return false
	}
	if n.Denied {
		return true
	}
	return slices.ContainsFunc(n.Children, denied)
}

// Home is the user's home directory, and where ~ points in rules.
func Home() (string, error) {
	h, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	owner.SetHome(h)
	return h, nil
}
