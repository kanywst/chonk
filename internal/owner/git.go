package owner

import (
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// Git is what git says about a working tree. A project whose history is on a
// remote and whose tree is clean can be removed and cloned back; one with
// work only on this disk cannot. Known is false when chonk could not ask.
type Git struct {
	Known bool
	// Remote is a remote that has this repository, empty when it has none.
	Remote string
	// Dirty counts changed tracked files and untracked ones, as
	// `git status --porcelain` reports them.
	Dirty int
	// Unpushed counts commits on local branches that no remote has, and
	// Branches names those branches. A branch whose work was squash-merged
	// upstream still counts: git cannot tell that apart from work that
	// would be lost, so chonk reports the fact and leaves the call to you.
	Unpushed int
	Branches []string
	Stashes  int
	// Ignored are ignored paths that are not build output chonk recognises:
	// local state that a fresh clone would not bring back.
	Ignored []string
}

// clean reports whether git holds nothing that only this disk has.
func (g Git) clean() bool { return g.Dirty == 0 && g.Unpushed == 0 && g.Stashes == 0 }

// describe is the safety, why and regen a project gets from its git state.
// parts are the build outputs chonk already found inside it, which a clone
// does not need to bring back.
func (g Git) describe(parts []*Item) (Safety, string, string) {
	if !g.Known {
		return projectOwner.Safety, projectOwner.Why, projectOwner.Regen
	}
	if g.Remote == "" {
		return Danger, "A git working tree with no remote: this disk is the only place its history exists.",
			"Nothing to clone it back from."
	}
	if !g.clean() {
		return Danger, "A git working tree with work that only this disk has.", "Only here: " + g.risks() + "."
	}
	// Careful, not safe: safe means something regenerates by itself, and a
	// clone is a step you take, needing the network and the remote to still
	// be there. What is clean is also only clean as of this scan.
	left := g.unexplained(parts)
	clone := "`git clone " + g.Remote + "` brings the history back, as long as that remote still has what your last fetch saw"
	if len(left) == 0 {
		return Careful, "A git working tree that was clean and fully pushed when this scan ran.",
			clone + "; the build output inside is rebuilt."
	}
	// Ignored state chonk cannot account for could be anything, a clone
	// with its own unpushed work included, so this says what is there and
	// offers nothing. Only the branch above is offered for removal.
	return Careful, "A git working tree, clean and pushed, holding local state that is not in git.",
		clone + ", but not " + list(left) + ", which nothing here explains."
}

// risks names what only this disk has, biggest worry first.
func (g Git) risks() string {
	var out []string
	if g.Dirty > 0 {
		out = append(out, count(g.Dirty, "uncommitted change", "uncommitted changes"))
	}
	if g.Unpushed > 0 {
		s := count(g.Unpushed, "commit", "commits")
		if len(g.Branches) > 0 {
			s += " on " + list(g.Branches)
		}
		out = append(out, s+" that no remote has")
	}
	if g.Stashes > 0 {
		out = append(out, count(g.Stashes, "stash", "stashes"))
	}
	return list(out)
}

// unexplained drops the ignored paths that are build output chonk already
// lists as a part of the project, leaving the local state nothing explains.
func (g Git) unexplained(parts []*Item) []string {
	known := make(map[string]bool, len(parts))
	for _, p := range parts {
		known[filepath.Base(p.Path)] = true
	}
	var out []string
	for _, p := range g.Ignored {
		base := filepath.Base(strings.TrimSuffix(p, "/"))
		if known[base] || throwaway[base] {
			continue
		}
		out = append(out, p)
		if len(out) == 4 {
			break
		}
	}
	return out
}

// throwaway are ignored files that carry nothing worth warning about.
var throwaway = map[string]bool{".DS_Store": true, "Thumbs.db": true, ".netrwhist": true}

func count(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return strconv.Itoa(n) + " " + many
}

// list joins items the way a sentence would.
func list(ss []string) string {
	switch len(ss) {
	case 0:
		return ""
	case 1:
		return ss[0]
	case 2:
		return ss[0] + " and " + ss[1]
	}
	return strings.Join(ss[:len(ss)-1], ", ") + " and " + ss[len(ss)-1]
}

// ApplyGit retells each project in terms of what git says about it. States
// are keyed by the project's path; a project with no entry keeps the
// cautious description it already has.
func (r *Report) ApplyGit(states map[string]Git) {
	projects := r.ByKind(KindProject)
	for _, it := range projects {
		g, ok := states[it.Path]
		if !ok {
			continue
		}
		it.Git = g
		o := *it.Owner
		o.Safety, o.Why, o.Regen = g.describe(it.Parts)
		// Removing a project takes everything under it, and git was asked
		// only about this tree: a repository nested inside, a submodule or
		// a clone kept in an ignored directory, answers for itself. Say so
		// and offer nothing while one of those still holds work.
		if keep := heldInside(it, projects, states); keep != "" {
			o.Safety = Danger
			o.Regen = "Removing this takes " + Tilde(keep) + " with it, which has work only this disk has."
			it.Owner = &o
			continue
		}
		// A scan root is never offered for removal, whatever git says about
		// it: a dotfiles repository at ~, or a /usr/local that is a
		// checkout, is a root first. Classify drops the actions of a root
		// for this reason, and this must not put one back.
		// Only a tree with nothing unaccounted for inside it: an ignored
		// directory chonk cannot explain may hold a repository of its own,
		// too small or too deep for the walk to have kept, and removing
		// the project would take that with it.
		if o.Safety != Danger && !isRoot(it) && len(g.unexplained(it.Parts)) == 0 {
			o.Actions = append(append([]Action{}, o.Actions...), trash(o.Safety))
		}
		it.Owner = &o
	}
}

// heldInside names a repository nested under it that would go too and is
// not safe to lose: one with work only this disk has, or one git could not
// answer for. It returns "" when everything inside is accounted for.
func heldInside(it *Item, projects []*Item, states map[string]Git) string {
	for _, other := range projects {
		if other == it || !strings.HasPrefix(other.Path, it.Path+"/") {
			continue
		}
		g, ok := states[other.Path]
		if !ok || !g.Known || g.Remote == "" || !g.clean() {
			return other.Path
		}
	}
	return ""
}

// roots are the scan roots, so a project that is one can be recognised from
// its path rather than from the shape of a walk. `chonk why` walks only the
// directory it was asked about, where every node is parentless, and judging
// by that made every project look like a root.
var roots []string

// SetRoots names the scan roots, which are never offered for removal.
func SetRoots(rs []string) { roots = slices.Clone(rs) }

// isRoot reports whether the item is a whole scan root.
func isRoot(it *Item) bool {
	for _, r := range roots {
		if it.Path == filepath.Clean(r) {
			return true
		}
	}
	// Without roots to compare against, fall back to the shape of the walk.
	return len(roots) == 0 && it.Node != nil && it.Node.Parent == nil
}

// Projects are the paths of every project in the report, for the git pass.
func (r *Report) Projects() []string {
	var out []string
	for _, it := range r.Items {
		if it.Owner.Kind == KindProject {
			out = append(out, it.Path)
		}
	}
	return out
}
