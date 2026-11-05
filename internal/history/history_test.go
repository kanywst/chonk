package history

import (
	"path/filepath"
	"sync"
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

func TestAddConcurrent(t *testing.T) {
	s := Store{Path: filepath.Join(t.TempDir(), "snapshots.jsonl")}
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Go(func() {
			if err := s.Add(Snapshot{Total: int64(i), Items: map[string]int64{"~/a": int64(i)}}, 0); err != nil {
				t.Error(err)
			}
		})
	}
	wg.Wait()
	if snaps, err := s.Load(); err != nil || len(snaps) != 20 {
		t.Errorf("kept %d of 20: %v", len(snaps), err)
	}
}

func TestPartial(t *testing.T) {
	// The terminal read ~/Library in full; the daily job, without Full Disk
	// Access, could not read Mail inside it.
	full := Snapshot{Total: 100, Items: map[string]int64{"~/Library": 60, "~/go": 40}}
	job := Snapshot{Total: 75, Items: map[string]int64{"~/Library": 30, "~/go": 45}, Partial: []string{"~/Library"}}
	if m := Mismatched(full, job); len(m) != 1 || !m["~/Library"] {
		t.Errorf("mismatched = %v", m)
	}
	if ch := Diff(full, job); len(ch) != 1 || ch[0].Key != "~/go" {
		t.Errorf("diff = %+v", ch)
	}
	if d := TotalDelta(full, job); d != 5 {
		t.Errorf("total delta = %d, want 5 (only ~/go grew)", d)
	}
	// Two scans that both could not read it are compared as they are.
	job2 := Snapshot{Total: 80, Items: map[string]int64{"~/Library": 35, "~/go": 45}, Partial: []string{"~/Library"}}
	if m := Mismatched(job, job2); len(m) != 0 {
		t.Errorf("both partial: %v", m)
	}
	if d := TotalDelta(job, job2); d != 5 {
		t.Errorf("total delta = %d", d)
	}
}
