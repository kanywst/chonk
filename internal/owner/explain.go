package owner

import (
	"path/filepath"
	"strings"

	"github.com/kanywst/chonk/internal/walk"
)

// Explain finds who owns path without scanning the whole disk: it asks the
// rules about each directory from the top down to path, the way Classify
// would meet them. It returns the directory the owner claims and, for a
// build output, the project it is in. The caller measures them.
func Explain(path string) (o *Owner, claimed string, project string) {
	path = filepath.Clean(path)
	rs := rules()
	anc := anchors(rs)
	target := Tilde(path)

	// Every directory from the top down to path.
	var chain []string
	for p := path; ; p = filepath.Dir(p) {
		chain = append([]string{p}, chain...)
		if p == "/" || p == home {
			break
		}
	}
	c := &classifier{rules: rs, anchors: anc}
	for i, dir := range chain {
		rel := Tilde(dir)
		n := &walk.Node{Name: filepath.Base(dir), Markers: walk.MarkersOf(dir)}
		if found := c.match(cand{Path: dir, Rel: rel, Node: n}); found != nil {
			if !anchorOnPath(anc, rel, target) {
				return found, dir, ""
			}
			o, claimed = found, dir // broad owner; a deeper rule may take over
			continue
		}
		if n.Has(walk.MarkGit) && isProjectPlace(rel) {
			return explainProject(chain[i:])
		}
	}
	if o != nil {
		return o, claimed, ""
	}
	u := unclaimedOwner
	return &u, path, ""
}

// anchorOnPath reports whether a rule names a path below rel that is on the
// way to target (or below it).
func anchorOnPath(anchors []string, rel, target string) bool {
	for _, a := range anchors {
		if strings.HasPrefix(a, rel+"/") && (target == a || strings.HasPrefix(target, a+"/") || strings.HasPrefix(a, target+"/")) {
			return true
		}
	}
	return false
}

// explainProject walks down from a project root to the target, the last
// element of chain, looking for a build output or a nested repository.
func explainProject(chain []string) (*Owner, string, string) {
	proj := chain[0]
	arts := artifacts()
	for i := 1; i < len(chain); i++ {
		dir, parent := chain[i], chain[i-1]
		n := &walk.Node{Name: filepath.Base(dir), Markers: walk.MarkersOf(dir)}
		if n.Has(walk.MarkGit) {
			return explainProject(chain[i:])
		}
		p := &walk.Node{Name: filepath.Base(parent), Markers: walk.MarkersOf(parent)}
		for _, a := range arts {
			if a.match(n, p) {
				o := a.owner
				return &o, dir, proj
			}
		}
	}
	o := projectOwner
	o.Name = filepath.Base(proj)
	return &o, proj, ""
}
