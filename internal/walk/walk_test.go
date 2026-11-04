package walk

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func write(t *testing.T, path string, n int) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, make([]byte, n), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWalkCountsAllocatedBytesAndLinksOnce(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "a", "big"), 2<<20)
	write(t, filepath.Join(root, "a", "Cargo.toml"), 10)
	if err := os.Link(filepath.Join(root, "a", "big"), filepath.Join(root, "b-link")); err != nil {
		t.Fatal(err)
	}
	// A sparse file: 1 GiB long, nothing written.
	f, err := os.Create(filepath.Join(root, "sparse"))
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Truncate(1 << 30); err != nil {
		t.Fatal(err)
	}
	_ = f.Close()

	var prog Progress
	nodes := Walk(context.Background(), []string{root, filepath.Join(root, "missing")}, Options{}, &prog)
	if len(nodes) != 1 {
		t.Fatalf("got %d roots, want 1 (missing roots are skipped)", len(nodes))
	}
	r := nodes[0]
	if r.Size < 2<<20 || r.Size > 3<<20 {
		t.Errorf("size = %d, want about 2 MiB: the link counted once and the sparse file near zero", r.Size)
	}
	if r.Logical < 1<<30 {
		t.Errorf("logical = %d, want at least the sparse file's 1 GiB length", r.Logical)
	}
	a := r.Child("a")
	if a == nil || !a.Has(MarkCargo) {
		t.Fatalf("a = %+v, want a kept child with the Cargo marker", a)
	}
	if got := a.Path(); got != filepath.Join(root, "a") {
		t.Errorf("Path() = %q", got)
	}
	if Find(nodes, filepath.Join(root, "a")) != a {
		t.Error("Find did not return a")
	}
	if Find(nodes, "/elsewhere") != nil {
		t.Error("Find outside the roots should be nil")
	}
	if prog.Files.Load() != 3 {
		t.Errorf("files seen = %d, want 3 (the link is not a new file)", prog.Files.Load())
	}
}

func TestWalkDropsSmallChildren(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "small", "x"), 100)
	write(t, filepath.Join(root, "big", "x"), 3<<20)
	nodes := Walk(context.Background(), []string{root}, Options{MinKeep: 1 << 20}, nil)
	r := nodes[0]
	if len(r.Children) != 1 || r.Children[0].Name != "big" {
		t.Fatalf("children = %v, want only big", r.Children)
	}
	var sum int64
	for _, c := range r.Children {
		sum += c.Size
	}
	if r.Size <= sum {
		t.Error("the dropped child's size should still count in the parent")
	}
}

func TestSkip(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CloudStorage", "x"), 3<<20)
	nodes := Walk(context.Background(), []string{root}, Options{Skip: func(p string) bool { return filepath.Base(p) == "CloudStorage" }}, nil)
	c := nodes[0].Child("CloudStorage")
	if c == nil || !c.Denied || c.Size != 0 {
		t.Fatalf("skipped dir = %+v, want listed, denied, size 0", c)
	}
}

func TestMarkersOf(t *testing.T) {
	root := t.TempDir()
	write(t, filepath.Join(root, "CACHEDIR.TAG"), 1)
	write(t, filepath.Join(root, "package.json"), 1)
	if m := MarkersOf(root); m != MarkCacheDirTag|MarkPackageJSON {
		t.Errorf("markers = %b", m)
	}
}
