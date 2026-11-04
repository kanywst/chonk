package history

import (
	"path/filepath"
	"testing"
	"time"
)

func TestStoreBaselineDiff(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "s", "snapshots.jsonl")}
	if snaps, err := s.Load(); err != nil || snaps != nil {
		t.Fatalf("missing file: %v %v", snaps, err)
	}
	now := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for i, total := range []int64{100, 200, 300} {
		snap := Snapshot{Time: now.Add(time.Duration(i-2) * 36 * time.Hour), Total: total, Items: map[string]int64{"~/a": total, "~/gone": 5}}
		if i == 2 {
			delete(snap.Items, "~/gone")
			snap.Items["~/new"] = 7 << 20
		}
		if err := s.Add(snap, 2); err != nil {
			t.Fatal(err)
		}
	}
	snaps, err := s.Load()
	if err != nil || len(snaps) != 2 {
		t.Fatalf("kept %d, want 2: %v", len(snaps), err)
	}
	base, ok := Baseline(snaps, now, 24*time.Hour)
	if !ok || base.Total != 200 {
		t.Fatalf("baseline = %+v", base)
	}
	if b, _ := Baseline(snaps, now, 1000*time.Hour); b.Total != 200 {
		t.Errorf("with nothing old enough, the oldest is used: %d", b.Total)
	}
	if _, ok := Baseline(nil, now, time.Hour); ok {
		t.Error("no history should not be ok")
	}
	ch := Diff(base, snaps[1])
	if len(ch) != 3 || ch[0].Key != "~/new" || ch[0].Delta() != 7<<20 {
		t.Fatalf("diff = %+v", ch)
	}
}
