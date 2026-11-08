package owner

import (
	"slices"
	"strings"
	"testing"

	"github.com/kanywst/chonk/internal/walk"
)

func TestGitDescribe(t *testing.T) {
	const remote = "https://github.com/o/r.git"
	cases := []struct {
		name  string
		git   Git
		parts []string
		want  Safety
		// regen must say these things, so the reason is never left implicit.
		says []string
	}{
		{
			name: "unknown keeps the cautious default",
			git:  Git{},
			want: Danger,
			says: []string{"not in git"},
		},
		{
			// Careful, not safe: a clone needs the remote and the network.
			name: "clean and pushed is careful, with the way back",
			git:  Git{Known: true, Remote: remote},
			want: Careful,
			says: []string{"git clone " + remote, "last fetch"},
		},
		{
			name:  "ignored build output chonk already lists is not held against it",
			git:   Git{Known: true, Remote: remote, Ignored: []string{"target/", ".DS_Store"}},
			parts: []string{"/p/target"},
			want:  Careful,
		},
		{
			name: "ignored local state is careful, and named",
			git:  Git{Known: true, Remote: remote, Ignored: []string{".wrangler/", ".env"}},
			want: Careful,
			says: []string{".wrangler/", ".env"},
		},
		{
			name: "no remote is danger even when clean",
			git:  Git{Known: true},
			want: Danger,
			says: []string{"Nothing to clone it back from"},
		},
		{
			name: "uncommitted changes are counted",
			git:  Git{Known: true, Remote: remote, Dirty: 3},
			want: Danger,
			says: []string{"3 uncommitted changes"},
		},
		{
			name: "unpushed commits name their branch",
			git:  Git{Known: true, Remote: remote, Unpushed: 2, Branches: []string{"feat/x"}},
			want: Danger,
			says: []string{"2 commits on feat/x", "no remote has"},
		},
		{
			name: "a lone stash is still only here",
			git:  Git{Known: true, Remote: remote, Stashes: 1},
			want: Danger,
			says: []string{"1 stash"},
		},
		{
			name: "every risk is reported, not just the first",
			git:  Git{Known: true, Remote: remote, Dirty: 1, Unpushed: 1, Stashes: 2},
			want: Danger,
			says: []string{"1 uncommitted change", "1 commit", "2 stashes"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var parts []*Item
			for _, p := range tc.parts {
				parts = append(parts, &Item{Path: p})
			}
			got, why, regen := tc.git.describe("/p", parts)
			if got != tc.want {
				t.Errorf("safety = %v, want %v (regen %q)", got, tc.want, regen)
			}
			if why == "" {
				t.Error("why is empty")
			}
			for _, s := range tc.says {
				if !strings.Contains(why+" "+regen, s) {
					t.Errorf("missing %q in %q / %q", s, why, regen)
				}
			}
		})
	}
}

// A clean, pushed project becomes removable, and keeps a way to do it. The
// node has a parent, because a project inside a scan root is what this
// describes; a root itself is covered below.
func TestApplyGit(t *testing.T) {
	n := &walk.Node{Name: "p", Size: 10, Parent: &walk.Node{Name: "/h"}}
	o := projectOwner
	rep := &Report{Items: []*Item{{Owner: &o, Path: "/p", Node: n, Size: 10}}}
	rep.ApplyGit(map[string]Git{"/p": {Known: true, Remote: "git@h:o/r.git"}})

	it := rep.Items[0]
	if it.Owner.Safety != Careful {
		t.Fatalf("safety = %v, want careful", it.Owner.Safety)
	}
	if len(it.Owner.Actions) == 0 {
		t.Fatal("a removable project offers no way to remove it")
	}
	if !it.Git.Known {
		t.Error("git state was not kept on the item")
	}
	// The shared owner value must not be rewritten for every other project.
	if projectOwner.Safety != Danger {
		t.Error("ApplyGit mutated the shared project owner")
	}
}

// A project chonk could not ask git about keeps its cautious description.
func TestApplyGitLeavesUnknownAlone(t *testing.T) {
	o := projectOwner
	rep := &Report{Items: []*Item{{Owner: &o, Path: "/p"}}}
	rep.ApplyGit(map[string]Git{})
	if rep.Items[0].Owner.Safety != Danger {
		t.Error("an unasked project stopped being danger")
	}
}

// A project that is a whole scan root is never offered for removal, however
// clean git says it is: classify strips a root's actions, and the git pass
// must not put one back.
func TestApplyGitNeverTrashesAScanRoot(t *testing.T) {
	root := &walk.Node{Name: "/h"}
	o := projectOwner
	o.Actions = nil
	rep := &Report{Items: []*Item{{Owner: &o, Path: "/h", Node: root, Size: 10}}}
	rep.ApplyGit(map[string]Git{"/h": {Known: true, Remote: "git@h:o/r.git"}})
	if len(rep.Items[0].Owner.Actions) != 0 {
		t.Errorf("a scan root was offered %v", rep.Items[0].Owner.Actions)
	}
}

// Removing a project takes the repositories inside it too, and git was
// asked only about the outer tree.
func TestApplyGitAccountsForNestedRepositories(t *testing.T) {
	parent := &walk.Node{Name: "/h"}
	outer := &Item{Owner: new(projectOwner), Path: "/h/p", Node: &walk.Node{Name: "p", Parent: parent}}
	inner := &Item{Owner: new(projectOwner), Path: "/h/p/vendor/x", Node: &walk.Node{Name: "x", Parent: parent}}
	rep := &Report{Items: []*Item{outer, inner}}

	clean := Git{Known: true, Remote: "git@h:o/r.git"}
	rep.ApplyGit(map[string]Git{
		"/h/p":          clean,
		"/h/p/vendor/x": {Known: true, Remote: "git@h:o/x.git", Unpushed: 1},
	})
	if outer.Owner.Safety != Danger {
		t.Errorf("outer safety = %v, want danger", outer.Owner.Safety)
	}
	for _, a := range outer.Owner.Actions {
		if a.Trash {
			t.Error("a project holding unpushed work in a nested repository was offered for the Trash")
		}
	}
	if !strings.Contains(outer.Owner.Regen, "vendor/x") {
		t.Errorf("regen does not name the nested repository: %q", outer.Owner.Regen)
	}

	// With the nested repository clean too, the outer one can go.
	outer.Owner = new(projectOwner)
	rep.ApplyGit(map[string]Git{"/h/p": clean, "/h/p/vendor/x": clean})
	var offered bool
	for _, a := range outer.Owner.Actions {
		offered = offered || a.Trash
	}
	if !offered {
		t.Error("a project whose nested repositories are all clean was not offered for removal")
	}
}

// A nested repository chonk could not ask about is not accounted for, so
// the outer one is not offered either.
func TestApplyGitTreatsAnUnknownNestedRepoAsHeldWork(t *testing.T) {
	parent := &walk.Node{Name: "/h"}
	outer := &Item{Owner: new(projectOwner), Path: "/h/p", Node: &walk.Node{Name: "p", Parent: parent}}
	inner := &Item{Owner: new(projectOwner), Path: "/h/p/n", Node: &walk.Node{Name: "n", Parent: parent}}
	rep := &Report{Items: []*Item{outer, inner}}
	rep.ApplyGit(map[string]Git{"/h/p": {Known: true, Remote: "git@h:o/r.git"}})
	for _, a := range outer.Owner.Actions {
		if a.Trash {
			t.Error("a project with an unexamined nested repository was offered for the Trash")
		}
	}
}

func TestList(t *testing.T) {
	cases := []struct {
		in   []string
		want string
	}{
		{nil, ""},
		{[]string{"a"}, "a"},
		{[]string{"a", "b"}, "a and b"},
		{[]string{"a", "b", "c"}, "a, b and c"},
	}
	for _, tc := range cases {
		if got := list(tc.in); got != tc.want {
			t.Errorf("list(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// Ignored state nothing explains could be a repository of its own, too
// small or too deep for the walk to have kept, so the project that holds it
// is described but never offered for removal.
func TestApplyGitOffersNothingWhenSomethingInsideIsUnexplained(t *testing.T) {
	n := &walk.Node{Name: "p", Parent: &walk.Node{Name: "/h"}}
	it := &Item{Owner: new(projectOwner), Path: "/h/p", Node: n, Size: 10}
	rep := &Report{Items: []*Item{it}}
	rep.ApplyGit(map[string]Git{"/h/p": {
		Known: true, Remote: "git@h:o/r.git", Ignored: []string{"vendor/"},
	}})
	if it.Owner.Safety != Careful {
		t.Errorf("safety = %v, want careful", it.Owner.Safety)
	}
	for _, a := range it.Owner.Actions {
		if a.Trash {
			t.Error("a project holding unexplained ignored state was offered for the Trash")
		}
	}
}

// A hand-made scratch/target is not the target/ that was measured as build
// output, so matching on the base name excused the wrong thing.
func TestUnexplainedComparesWholePaths(t *testing.T) {
	g := Git{Known: true, Remote: "git@h:o/r.git", Ignored: []string{"target/", "scratch/target/"}}
	left := g.unexplained("/h/p", []*Item{{Path: "/h/p/target"}})
	if !slices.Equal(left, []string{"scratch/target/"}) {
		t.Errorf("unexplained = %q, want only the one nothing measured", left)
	}
}

// A nested repository's own unexplained state is no more recoverable than a
// commit, so it holds the project around it just the same.
func TestHeldInsideCountsNestedIgnoredState(t *testing.T) {
	parent := &walk.Node{Name: "/h"}
	outer := &Item{Owner: new(projectOwner), Path: "/h/p", Node: &walk.Node{Name: "p", Parent: parent}}
	inner := &Item{Owner: new(projectOwner), Path: "/h/p/sub", Node: &walk.Node{Name: "sub", Parent: parent}}
	rep := &Report{Items: []*Item{outer, inner}}
	rep.ApplyGit(map[string]Git{
		"/h/p":     {Known: true, Remote: "git@h:o/r.git"},
		"/h/p/sub": {Known: true, Remote: "git@h:o/s.git", Ignored: []string{".env"}},
	})
	for _, a := range outer.Owner.Actions {
		if a.Trash {
			t.Error("a project whose submodule holds unexplained state was offered for the Trash")
		}
	}
	if !strings.Contains(outer.Owner.Regen, "sub") {
		t.Errorf("regen does not name the nested repository: %q", outer.Owner.Regen)
	}
}
