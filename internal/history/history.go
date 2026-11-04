// Package history keeps per-item totals from past scans, so chonk can say
// what grew.
package history

import (
	"bufio"
	"cmp"
	"encoding/json/v2"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// Snapshot is one scan's totals, keyed by item path.
type Snapshot struct {
	Time  time.Time        `json:"time"`
	Total int64            `json:"total_bytes"`
	Items map[string]int64 `json:"items"`
}

// Store is a JSON-lines file of snapshots, oldest first.
type Store struct{ Path string }

// StateDir is where chonk keeps its files: $XDG_STATE_HOME/chonk, or
// ~/.local/state/chonk.
func StateDir(home string) string {
	if d := os.Getenv("XDG_STATE_HOME"); d != "" {
		return filepath.Join(d, "chonk")
	}
	return filepath.Join(home, ".local", "state", "chonk")
}

// Load reads every snapshot. A missing file is no history, not an error.
func (s Store) Load() ([]Snapshot, error) {
	f, err := os.Open(s.Path)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	var out []Snapshot
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 1<<20), 64<<20)
	for sc.Scan() {
		var snap Snapshot
		if json.Unmarshal(sc.Bytes(), &snap) == nil {
			out = append(out, snap)
		}
	}
	return out, sc.Err()
}

// Add appends snap, keeping at most keep snapshots (0 keeps all).
func (s Store) Add(snap Snapshot, keep int) error {
	all, err := s.Load()
	if err != nil {
		return err
	}
	all = append(all, snap)
	if keep > 0 && len(all) > keep {
		all = all[len(all)-keep:]
	}
	if err := os.MkdirAll(filepath.Dir(s.Path), 0o700); err != nil {
		return err
	}
	tmp := s.Path + ".tmp"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	w := bufio.NewWriter(f)
	for _, sn := range all {
		b, err := json.Marshal(sn)
		if err != nil {
			_ = f.Close()
			return err
		}
		_, _ = w.Write(append(b, '\n'))
	}
	if err := w.Flush(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.Path)
}

// Baseline picks the snapshot to compare now against: the newest one at
// least minAge old, or else the oldest one there is. ok is false when there
// is no history.
func Baseline(snaps []Snapshot, now time.Time, minAge time.Duration) (Snapshot, bool) {
	if len(snaps) == 0 {
		return Snapshot{}, false
	}
	for _, snap := range slices.Backward(snaps) {
		if now.Sub(snap.Time) >= minAge {
			return snap, true
		}
	}
	return snaps[0], true
}

// Change is how one item moved between two snapshots.
type Change struct {
	Key         string
	Before, Now int64
}

// Delta is Now - Before.
func (c Change) Delta() int64 { return c.Now - c.Before }

// Diff lists every item whose size changed, largest change first.
func Diff(old, cur Snapshot) []Change {
	var out []Change
	for k, v := range cur.Items {
		if o := old.Items[k]; o != v {
			out = append(out, Change{Key: k, Before: o, Now: v})
		}
	}
	for k, v := range old.Items {
		if _, ok := cur.Items[k]; !ok {
			out = append(out, Change{Key: k, Before: v})
		}
	}
	sortChanges(out)
	return out
}

func sortChanges(cs []Change) {
	abs := func(n int64) int64 {
		if n < 0 {
			return -n
		}
		return n
	}
	slices.SortFunc(cs, func(a, b Change) int {
		if c := cmp.Compare(abs(b.Delta()), abs(a.Delta())); c != 0 {
			return c
		}
		return strings.Compare(a.Key, b.Key)
	})
}
