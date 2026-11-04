package owner

import (
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/kanywst/chonk/internal/walk"
)

// Options tunes classification.
type Options struct {
	// MinItem is the smallest unclaimed directory listed on its own; smaller
	// unclaimed space is summed into one "small files" item per root.
	MinItem int64
}

// Report is everything a scan found, grouped by owner.
type Report struct {
	Items []*Item // biggest first; projects carry their build outputs in Parts
	Total int64
	Roots []*walk.Node
}

// ByKind returns the items of one kind, biggest first.
func (r *Report) ByKind(k Kind) []*Item {
	var out []*Item
	for _, it := range r.Items {
		if it.Owner.Kind == k {
			out = append(out, it)
		}
	}
	return out
}

// KindsBySize lists the kinds that have items, biggest total first, with
// unclaimed space always last.
func (r *Report) KindsBySize() []Kind {
	var ks []Kind
	for k := range KindUnclaimed {
		if r.KindTotal(k) > 0 {
			ks = append(ks, k)
		}
	}
	slices.SortStableFunc(ks, func(a, b Kind) int { return cmpSize(r.KindTotal(a), r.KindTotal(b)) })
	if r.KindTotal(KindUnclaimed) > 0 {
		ks = append(ks, KindUnclaimed)
	}
	return ks
}

// KindTotal sums the items of one kind.
func (r *Report) KindTotal(k Kind) int64 {
	var n int64
	for _, it := range r.ByKind(k) {
		n += it.Size
	}
	return n
}

type classifier struct {
	rules     []rule
	artifacts []artifact
	anchors   []string // rule paths, to know when a claim must look deeper
	opt       Options
	items     []*Item
	small     map[string]int64
}

// Classify assigns every byte under roots to an owner.
func Classify(roots []*walk.Node, opt Options) *Report {
	if opt.MinItem <= 0 {
		opt.MinItem = 64 << 20
	}
	rs := rules()
	c := &classifier{rules: rs, artifacts: artifacts(), anchors: anchors(rs), opt: opt, small: map[string]int64{}}
	rep := &Report{Roots: roots}
	for _, r := range roots {
		rep.Total += r.Size
		if c.visit(r, Tilde(r.Name)) == 0 {
			c.unclaimed(r, Tilde(r.Name))
		}
	}
	for root, n := range c.small {
		if n <= 0 {
			continue
		}
		o := unclaimedOwner
		o.Name = "small files"
		o.Why = "Unclaimed directories too small to list one by one, added up."
		c.items = append(c.items, &Item{Owner: &o, Path: Untilde(root), Size: n, Hotspot: ""})
	}
	slices.SortStableFunc(c.items, func(a, b *Item) int { return cmpSize(a.Size, b.Size) })
	rep.Items = c.items
	return rep
}

func cmpSize(a, b int64) int {
	switch {
	case a > b:
		return -1
	case a < b:
		return 1
	}
	return 0
}

// anchors are the paths rules name: an exact path, or the parent whose
// children a rule claims.
func anchors(rs []rule) []string {
	out := make([]string, 0, len(rs))
	for _, r := range rs {
		out = append(out, r.path)
	}
	return out
}

// deeper reports whether a rule names a path strictly below rel.
func (c *classifier) deeper(rel string) bool {
	for _, a := range c.anchors {
		if strings.HasPrefix(a, rel+"/") {
			return true
		}
	}
	return false
}

func (c *classifier) match(cd cand) *Owner {
	for _, r := range c.rules {
		if o := r.match(cd); o != nil {
			return o
		}
	}
	return nil
}

func newItem(o *Owner, n *walk.Node) *Item {
	return &Item{Owner: o, Path: n.Path(), Node: n, Size: n.Size, Logical: n.Logical, Files: n.Files, Touched: unix(n.Touched)}
}

func unix(s int64) time.Time {
	if s == 0 {
		return time.Time{}
	}
	return time.Unix(s, 0)
}

// visit classifies n and returns how many of its bytes some owner claimed.
// When it returns 0 the caller decides how to report n as unclaimed, so an
// unclaimed region is listed once, at its top.
func (c *classifier) visit(n *walk.Node, rel string) int64 {
	cd := cand{Path: n.Path(), Rel: rel, Node: n}
	if o := c.match(cd); o != nil {
		if !c.deeper(rel) {
			c.items = append(c.items, newItem(o, n))
			return n.Size
		}
		// A broad rule (~/Library, an app's support directory) claims n, but
		// a more specific rule names something inside it: let that one have
		// its part, and keep the rest.
		it := newItem(o, n)
		c.items = append(c.items, it)
		for _, ch := range n.Children {
			crel := rel + "/" + ch.Name
			if !c.onAnchorPath(crel) {
				continue
			}
			got := c.visit(ch, crel)
			if got == 0 {
				continue // stays with the broad owner
			}
			it.Size -= got
		}
		return n.Size
	}
	if n.Has(walk.MarkGit) && isProjectPlace(rel) {
		return c.project(n, rel)
	}

	var claimed int64
	results := make([]int64, len(n.Children))
	for i, ch := range n.Children {
		results[i] = c.visit(ch, rel+"/"+ch.Name)
		claimed += results[i]
	}
	if claimed == 0 {
		return 0
	}
	for i, ch := range n.Children {
		if results[i] == 0 {
			c.unclaimed(ch, rel+"/"+ch.Name)
		}
	}
	// What is left: files directly in n, and children too small to keep.
	rest := n.Size
	for _, ch := range n.Children {
		rest -= ch.Size
	}
	c.small[c.rootOf(rel)] += rest
	return n.Size
}

func (c *classifier) onAnchorPath(rel string) bool {
	for _, a := range c.anchors {
		if a == rel || strings.HasPrefix(a, rel+"/") {
			return true
		}
	}
	return false
}

// isProjectPlace leaves out git trees that are part of an app or tool, like
// Homebrew's taps or an editor's extension checkouts.
func isProjectPlace(rel string) bool {
	return strings.HasPrefix(rel, "~/") && !strings.HasPrefix(rel, "~/Library/")
}

func (c *classifier) rootOf(rel string) string {
	if strings.HasPrefix(rel, "~") {
		return "~"
	}
	if i := strings.IndexByte(rel[1:], '/'); i >= 0 {
		return rel[:i+1]
	}
	return rel
}

// unclaimed lists n as unclaimed, or adds it to the small-files total.
func (c *classifier) unclaimed(n *walk.Node, rel string) {
	if n.Size < c.opt.MinItem {
		c.small[c.rootOf(rel)] += n.Size
		return
	}
	o := unclaimedOwner
	it := newItem(&o, n)
	if h := hotspot(n); h != n {
		it.Hotspot = h.Path()
	}
	if n.Denied {
		o.Why = "chonk could not read this directory, so its size is unknown."
	}
	c.items = append(c.items, it)
}

// hotspot descends while one child holds most of n.
func hotspot(n *walk.Node) *walk.Node {
	for len(n.Children) > 0 {
		top := n.Children[0]
		if top.Size*10 < n.Size*8 {
			break
		}
		n = top
	}
	return n
}

// project claims a git working tree and the build outputs inside it.
func (c *classifier) project(n *walk.Node, rel string) int64 {
	o := projectOwner
	o.Name = filepath.Base(n.Path())
	it := newItem(&o, n)
	c.items = append(c.items, it)
	c.projectParts(it, n, rel, 0)
	return n.Size
}

func (c *classifier) projectParts(it *Item, n *walk.Node, rel string, depth int) {
	if depth > 8 {
		return
	}
	for _, ch := range n.Children {
		crel := rel + "/" + ch.Name
		if ch.Has(walk.MarkGit) {
			// A nested repository (a submodule, a clone kept inside) is its
			// own project.
			it.Size -= c.project(ch, crel)
			continue
		}
		if a := c.artifact(ch, n); a != nil {
			part := newItem(a, ch)
			part.Project = it
			it.Parts = append(it.Parts, part)
			continue
		}
		c.projectParts(it, ch, crel, depth+1)
	}
	slices.SortStableFunc(it.Parts, func(a, b *Item) int { return cmpSize(a.Size, b.Size) })
}

func (c *classifier) artifact(n, parent *walk.Node) *Owner {
	for _, a := range c.artifacts {
		if a.match(n, parent) {
			o := a.owner
			return &o
		}
	}
	return nil
}

// Why finds the item that accounts for path.
func (r *Report) Why(path string) (*Item, *Item) {
	path = filepath.Clean(path)
	var best *Item
	for _, it := range r.Items {
		if within(path, it.Path) && (best == nil || len(it.Path) > len(best.Path)) {
			best = it
		}
	}
	if best == nil {
		return nil, nil
	}
	for _, p := range best.Parts {
		if within(path, p.Path) {
			return best, p
		}
	}
	return best, nil
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}
