package owner

import (
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
			name: "clean and pushed is safe",
			git:  Git{Known: true, Remote: remote},
			want: Safe,
			says: []string{"git clone " + remote},
		},
		{
			name:  "ignored build output chonk already lists stays safe",
			git:   Git{Known: true, Remote: remote, Ignored: []string{"target/", ".DS_Store"}},
			parts: []string{"/p/target"},
			want:  Safe,
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
			got, why, regen := tc.git.describe(parts)
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

// A clean, pushed project becomes safe to remove, and keeps a way to do it.
func TestApplyGit(t *testing.T) {
	n := &walk.Node{Name: "/p", Size: 10}
	o := projectOwner
	rep := &Report{Items: []*Item{{Owner: &o, Path: "/p", Node: n, Size: 10}}}
	rep.ApplyGit(map[string]Git{"/p": {Known: true, Remote: "git@h:o/r.git"}})

	it := rep.Items[0]
	if it.Owner.Safety != Safe {
		t.Fatalf("safety = %v, want safe", it.Owner.Safety)
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
