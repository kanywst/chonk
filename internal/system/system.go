// Package system reports what lies outside the directories chonk walks:
// how much of the volume the scan accounts for, and APFS local snapshots,
// which hold on to space that deleting files does not give back.
package system

import (
	"context"
	"fmt"
	"strings"
	"syscall"

	"github.com/kanywst/chonk/internal/owner"
)

// Volume is the space on the volume holding path.
type Volume struct {
	Total, Used, Free int64
}

// VolumeOf reads the volume's numbers with statfs. Used counts what the
// volume's own blocks hold, so on APFS other volumes in the same container
// (the sealed system, Preboot, VM swap) are not in it.
func VolumeOf(path string) (Volume, error) {
	var st syscall.Statfs_t
	if err := syscall.Statfs(path, &st); err != nil {
		return Volume{}, err
	}
	bs := int64(st.Bsize)                                                                           //nolint:unconvert // already int64 on linux
	total := int64(st.Blocks) * bs                                                                  //nolint:gosec // block counts fit
	return Volume{Total: total, Used: total - int64(st.Bfree)*bs, Free: int64(st.Bavail) * bs}, nil //nolint:gosec // same
}

// Runner runs a command and returns its output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Snapshots lists Time Machine's local snapshots on the data volume. An
// error (no tmutil, not macOS) is no snapshots.
func Snapshots(ctx context.Context, run Runner) []string {
	out, err := run(ctx, "tmutil", "listlocalsnapshots", "/System/Volumes/Data")
	if err != nil {
		out, err = run(ctx, "tmutil", "listlocalsnapshots", "/")
		if err != nil {
			return nil
		}
	}
	var snaps []string
	for line := range strings.SplitSeq(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "com.apple.") {
			snaps = append(snaps, line)
		}
	}
	return snaps
}

// SnapshotOwner is the owner of local snapshots. Their size is not known
// until they are gone: APFS does not say how many blocks only a snapshot
// still holds.
var SnapshotOwner = owner.Owner{
	ID: "apfs-snapshots", Name: "Time Machine local snapshots", Kind: owner.KindSystem, Safety: owner.Careful,
	Why:   "Time Machine keeps hourly APFS snapshots on this disk between backups. Each one holds on to every block that was deleted after it was taken, so deleting files frees nothing while a snapshot remembers them.",
	Regen: "You lose the local restore points; backups on the Time Machine disk are not touched. macOS deletes them by itself within a day, or when space runs low.",
	Actions: []owner.Action{{
		Label: "Thin local snapshots now", Cmd: []string{"tmutil", "thinlocalsnapshots", "/", "999999999999", "4"},
		Safety: owner.Careful, Needs: "tmutil",
		Note: "Asks macOS to free as much as it can from local snapshots at the highest urgency. chonk measures what came back.",
	}},
}

// SnapshotItem is the report line for snapshots, or nil when there are none.
func SnapshotItem(snaps []string) *owner.Item {
	if len(snaps) == 0 {
		return nil
	}
	o := SnapshotOwner
	o.Name = fmt.Sprintf("%s (%d)", o.Name, len(snaps))
	return &owner.Item{Owner: &o, Path: "/", SizeUnknown: true}
}
