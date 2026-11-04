package system

import (
	"context"
	"errors"
	"testing"
)

func TestSnapshots(t *testing.T) {
	run := func(_ context.Context, _ string, args ...string) ([]byte, error) {
		if args[1] == "/System/Volumes/Data" {
			return []byte("Snapshots for disk /System/Volumes/Data:\ncom.apple.TimeMachine.2026-11-04-120000.local\ncom.apple.TimeMachine.2026-11-04-130000.local\n"), nil
		}
		return nil, errors.New("no")
	}
	s := Snapshots(context.Background(), run)
	if len(s) != 2 {
		t.Fatalf("snapshots = %v", s)
	}
	it := SnapshotItem(s)
	if it == nil || !it.SizeUnknown || it.Owner.Name != "Time Machine local snapshots (2)" {
		t.Errorf("item = %+v", it)
	}
	if SnapshotItem(nil) != nil {
		t.Error("no snapshots, no item")
	}
	none := func(context.Context, string, ...string) ([]byte, error) { return nil, errors.New("no tmutil") }
	if Snapshots(context.Background(), none) != nil {
		t.Error("no tmutil should be no snapshots")
	}
}

func TestVolumeOf(t *testing.T) {
	v, err := VolumeOf(t.TempDir())
	if err != nil || v.Total <= 0 || v.Used <= 0 || v.Free <= 0 || v.Used > v.Total {
		t.Errorf("volume = %+v %v", v, err)
	}
}
