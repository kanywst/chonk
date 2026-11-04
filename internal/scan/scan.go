// Package scan walks the machine and classifies what it finds.
package scan

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/kanywst/chonk/internal/history"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/procs"
	"github.com/kanywst/chonk/internal/system"
	"github.com/kanywst/chonk/internal/walk"
)

// Roots are the places chonk looks: your home, apps, and where package
// managers install to. Ones that do not exist are skipped.
func Roots(home string) []string {
	return []string{home, "/Applications", "/opt/homebrew", "/usr/local", "/nix"}
}

// Run walks roots and classifies them. Local APFS snapshots, which no
// directory shows, are added as an item of their own.
func Run(ctx context.Context, roots []string, prog *walk.Progress) *owner.Report {
	nodes := walk.Walk(ctx, roots, walk.Options{MinKeep: 1 << 20, Skip: skip}, prog)
	rep := owner.Classify(nodes, owner.Options{})
	if it := system.SnapshotItem(system.Snapshots(ctx, system.Runner(procs.Exec))); it != nil {
		rep.Items = append(rep.Items, it)
	}
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
	for _, it := range r.Items {
		if !it.SizeUnknown {
			s.Items[owner.Tilde(it.Path)] += it.Size
		}
	}
	return s
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
