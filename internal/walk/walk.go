// Package walk measures directory trees by the space they take on disk.
//
// Sizes are allocated bytes (st_blocks * 512), not file lengths, so a sparse
// VM disk image counts for what it really holds. A file with several hard
// links is counted once.
package walk

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
)

// Marker is a set of well-known names found directly inside a directory.
// Owner rules match on these instead of reading the disk again.
type Marker uint32

// Markers chonk looks for.
const (
	MarkGit Marker = 1 << iota
	MarkCargo
	MarkPackageJSON
	MarkGoMod
	MarkPyproject
	MarkCacheDirTag
	MarkPyvenv
	MarkNextConfig
	MarkGradle
	MarkTerraform
	MarkMix
)

var markerNames = map[string]Marker{
	".git":             MarkGit,
	"Cargo.toml":       MarkCargo,
	"package.json":     MarkPackageJSON,
	"go.mod":           MarkGoMod,
	"pyproject.toml":   MarkPyproject,
	"requirements.txt": MarkPyproject,
	"CACHEDIR.TAG":     MarkCacheDirTag,
	"pyvenv.cfg":       MarkPyvenv,
	"next.config.js":   MarkNextConfig,
	"next.config.mjs":  MarkNextConfig,
	"next.config.ts":   MarkNextConfig,
	"build.gradle":     MarkGradle,
	"build.gradle.kts": MarkGradle,
	"main.tf":          MarkTerraform,
	"mix.exs":          MarkMix,
}

// Node is one directory. Size, Files and Touched cover the whole subtree.
type Node struct {
	Name     string // the full path for a root, the base name otherwise
	Parent   *Node
	Children []*Node // subdirectories, biggest first; dropped below Options.MinKeep
	Size     int64   // allocated bytes
	Logical  int64   // sum of file lengths, to spot sparse files
	Own      int64   // allocated bytes of files directly in this directory
	Files    int64
	Touched  int64 // newest mtime in the subtree, unix seconds
	Markers  Marker
	Denied   bool // this directory could not be read, so its size is unknown
	Skipped  bool // left out on purpose (Options.Skip)
}

// Path is the node's full path.
func (n *Node) Path() string {
	if n.Parent == nil {
		return n.Name
	}
	parts := []string{}
	for p := n; p != nil; p = p.Parent {
		parts = append(parts, p.Name)
	}
	slices.Reverse(parts)
	return filepath.Join(parts...)
}

// Has reports whether every marker in m was found directly in n.
func (n *Node) Has(m Marker) bool { return n.Markers&m == m }

// Child returns the subdirectory called name, or nil.
func (n *Node) Child(name string) *Node {
	for _, c := range n.Children {
		if c.Name == name {
			return c
		}
	}
	return nil
}

// Find returns the node at path under one of roots, or nil when it was not
// kept (missing, or below MinKeep).
func Find(roots []*Node, path string) *Node {
	path = filepath.Clean(path)
	for _, r := range roots {
		rel, err := filepath.Rel(r.Name, path)
		if err != nil || rel == ".." || len(rel) > 2 && rel[:3] == "../" {
			continue
		}
		n := r
		if rel == "." {
			return n
		}
		for part := range splitPath(rel) {
			if n = n.Child(part); n == nil {
				return nil
			}
		}
		return n
	}
	return nil
}

func splitPath(rel string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := 0
		for i := 0; i <= len(rel); i++ {
			if i == len(rel) || rel[i] == filepath.Separator {
				if i > start && !yield(rel[start:i]) {
					return
				}
				start = i + 1
			}
		}
	}
}

// Options tunes a walk.
type Options struct {
	Workers int   // concurrent directory readers; 0 means 2 per CPU (more only adds contention)
	MinKeep int64 // directories smaller than this keep their totals but drop their children
	// Skip returns true for directories that must not be entered (they are
	// still listed, with Denied set and no size).
	Skip func(path string) bool
	// Portable makes the walk use ReadDir and lstat even where a faster
	// bulk call exists. Tests use it to compare the two.
	Portable bool
}

// Progress counts what a walk has seen so far. It is safe to read while the
// walk runs.
type Progress struct {
	Files atomic.Int64
	Bytes atomic.Int64
	Dirs  atomic.Int64
}

type walker struct {
	ctx  context.Context
	opt  Options
	read func(string) ([]entry, error)
	sem  chan struct{}
	prog *Progress
	seen sync.Map // fileID -> struct{}, for files with more than one link
}

// Walk measures each root. Roots that do not exist are left out. A walk
// stays on each root's device: mount points below a root are not entered.
func Walk(ctx context.Context, roots []string, opt Options, prog *Progress) []*Node {
	if opt.Workers <= 0 {
		opt.Workers = 2 * runtime.NumCPU()
	}
	if prog == nil {
		prog = &Progress{}
	}
	w := &walker{ctx: ctx, opt: opt, read: readDir, sem: make(chan struct{}, opt.Workers), prog: prog}
	if opt.Portable {
		w.read = readDirPortable
	}
	var out []*Node
	for _, r := range roots {
		r = filepath.Clean(r)
		fi, err := os.Lstat(r)
		if err != nil || !fi.IsDir() {
			continue
		}
		st := statOf(fi)
		n := &Node{Name: r}
		w.dir(r, n, st.dev)
		out = append(out, n)
	}
	return out
}

func (w *walker) dir(path string, n *Node, dev uint64) {
	if w.ctx.Err() != nil {
		return
	}
	w.prog.Dirs.Add(1)
	entries, err := w.read(path)
	if err != nil && len(entries) == 0 {
		n.Denied = true
		return
	}

	var subdirs []*Node
	for _, e := range entries {
		if m, ok := markerNames[e.name]; ok {
			n.Markers |= m
		}
		if e.mtime > n.Touched {
			n.Touched = e.mtime
		}
		switch e.kind {
		case kindDir:
			if e.dev != dev {
				continue // a mount point
			}
			c := &Node{Name: e.name, Parent: n}
			if w.opt.Skip != nil && w.opt.Skip(filepath.Join(path, e.name)) {
				c.Skipped = true
			}
			subdirs = append(subdirs, c)
			continue
		case kindLink:
			continue
		}
		if e.nlink > 1 {
			if _, dup := w.seen.LoadOrStore(fileID{e.dev, e.ino}, struct{}{}); dup {
				continue
			}
		}
		n.Own += e.alloc
		n.Logical += e.size
		n.Files++
		w.prog.Files.Add(1)
		w.prog.Bytes.Add(e.alloc)
	}

	var wg sync.WaitGroup
	for _, c := range subdirs {
		if c.Skipped {
			continue
		}
		cpath := filepath.Join(path, c.Name)
		select {
		case w.sem <- struct{}{}:
			wg.Go(func() {
				defer func() { <-w.sem }()
				w.dir(cpath, c, dev)
			})
		default:
			w.dir(cpath, c, dev)
		}
	}
	wg.Wait()

	n.Size = n.Own
	for _, c := range subdirs {
		n.Size += c.Size
		n.Logical += c.Logical
		n.Files += c.Files
		n.Touched = max(n.Touched, c.Touched)
	}
	slices.SortFunc(subdirs, func(a, b *Node) int {
		switch {
		case a.Size > b.Size:
			return -1
		case a.Size < b.Size:
			return 1
		}
		return compareStrings(a.Name, b.Name)
	})
	kept := subdirs[:0]
	for _, c := range subdirs {
		if c.Size >= w.opt.MinKeep || c.Denied || c.Skipped {
			kept = append(kept, c)
		} else {
			c.Children = nil
		}
	}
	n.Children = slices.Clip(kept)
}

func compareStrings(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// fileID identifies a file across hard links.
type fileID struct{ dev, ino uint64 }

type stat struct {
	dev   uint64
	id    fileID
	nlink uint64
	alloc int64
	mtime int64
}

// Allocated is the space path takes on disk, and its length.
func Allocated(path string) (alloc, logical int64, err error) {
	fi, err := os.Lstat(path)
	if err != nil {
		return 0, 0, err
	}
	return statOf(fi).alloc, fi.Size(), nil
}

// MarkersOf reads the markers directly inside dir.
func MarkersOf(dir string) Marker {
	f, err := os.Open(dir)
	if err != nil {
		return 0
	}
	defer f.Close()
	names, _ := f.Readdirnames(-1)
	var m Marker
	for _, n := range names {
		m |= markerNames[n]
	}
	return m
}
