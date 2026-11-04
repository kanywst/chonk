package walk

import (
	"context"
	"os"
	"testing"
	"time"
)

// TestHomeSpeed compares the two readers on a real directory. It runs only
// with CHONK_BENCH_DIR set.
func TestHomeSpeed(t *testing.T) {
	dir := os.Getenv("CHONK_BENCH_DIR")
	if dir == "" {
		t.Skip("set CHONK_BENCH_DIR")
	}
	for _, p := range []bool{true, false} {
		start := time.Now()
		n := Walk(context.Background(), []string{dir}, Options{MinKeep: 1 << 20, Portable: p}, nil)[0]
		t.Logf("portable=%v: %s, %d files, %d bytes", p, time.Since(start).Round(time.Millisecond), n.Files, n.Size)
	}
}
