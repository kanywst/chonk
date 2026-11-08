package pins

import (
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/walk"
)

func write(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCollect(t *testing.T) {
	home := t.TempDir()
	a, b := filepath.Join(home, "a"), filepath.Join(home, "b")
	write(t, filepath.Join(a, ".nvmrc"), "24\n")
	write(t, filepath.Join(a, "rust-toolchain.toml"), "[toolchain]\nchannel = \"stable\"\n")
	write(t, filepath.Join(b, ".nvmrc"), "22\n")
	write(t, filepath.Join(b, ".tool-versions"), "# a comment\nnodejs 20.1.0\npython 3.12.1\n")
	write(t, filepath.Join(home, ".nvm/alias/default"), "node\n")
	write(t, filepath.Join(home, ".rustup/settings.toml"), "default_toolchain = \"stable-aarch64-apple-darwin\"\n")

	s := Collect(home, []string{a, b})
	node := slices.Clone(s.Wanted["node"])
	slices.Sort(node)
	if !slices.Equal(node, []string{"20.1.0", "22", "24"}) {
		t.Errorf("node pins = %q", node)
	}
	if !slices.Equal(s.Wanted["rust"], []string{"stable"}) {
		t.Errorf("rust pins = %q", s.Wanted["rust"])
	}
	// nodejs in .tool-versions is the same tool as node.
	if !slices.Equal(s.Wanted["python"], []string{"3.12.1"}) {
		t.Errorf("python pins = %q", s.Wanted["python"])
	}
	if s.Default["node"] != "node" || s.Default["rust"] != "stable-aarch64-apple-darwin" {
		t.Errorf("defaults = %v", s.Default)
	}
}

func TestMatches(t *testing.T) {
	cases := []struct {
		v, pin string
		want   bool
	}{
		{"22.16.0", "22", true}, // a pin covers the versions under it
		{"22.16.0", "22.16.0", true},
		{"22.16.0", "2", false}, // not a version prefix, a number prefix
		{"22.16.0", "24", false},
		{"stable", "stable", true},
		{"", "22", false},
		{"22.16.0", "", false},
	}
	for _, tc := range cases {
		if got := matches(tc.v, tc.pin); got != tc.want {
			t.Errorf("matches(%q, %q) = %v", tc.v, tc.pin, got)
		}
	}
}

func TestVersion(t *testing.T) {
	cases := []struct{ tool, name, want string }{
		{"rust", "1.90.0-aarch64-apple-darwin", "1.90.0"},
		{"rust", "stable-aarch64-apple-darwin", "stable"},
		{"rust", "nightly-aarch64-apple-darwin", "nightly"},
		{"node", "v22.16.0", "22.16.0"},
		{"python", "3.12.1", "3.12.1"},
	}
	for _, tc := range cases {
		if got := version(tc.tool, tc.name); got != tc.want {
			t.Errorf("version(%q, %q) = %q, want %q", tc.tool, tc.name, got, tc.want)
		}
	}
}

func TestComparable(t *testing.T) {
	for _, p := range []string{"22", "1.90.0", "stable", "nightly", "beta"} {
		if !isVersion(p) {
			t.Errorf("%q should be a comparable version", p)
		}
	}
	// Aliases only the manager can resolve must stop the whole check.
	for _, p := range []string{"lts/*", "node", "latest", "system", "", ">=20"} {
		if isVersion(p) {
			t.Errorf("%q should not be a comparable version", p)
		}
	}
}

// tree builds a node for dir/<holds...>/<versions>.
func tree(root string, holds []string, versions map[string]int64) *walk.Node {
	n := &walk.Node{Name: root}
	cur := n
	for _, h := range holds {
		ch := &walk.Node{Name: h, Parent: cur}
		cur.Children = append(cur.Children, ch)
		cur = ch
	}
	for name, size := range versions {
		cur.Children = append(cur.Children, &walk.Node{Name: name, Size: size, Parent: cur})
	}
	slices.SortFunc(cur.Children, func(a, b *walk.Node) int { return cmpName(a.Name, b.Name) })
	return n
}

func cmpName(a, b string) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func item(id string, n *walk.Node) *owner.Item {
	o := owner.Owner{ID: id, Kind: owner.KindToolchain, Regen: "Installed again."}
	return &owner.Item{Owner: &o, Path: n.Name, Node: n, Size: 100}
}

func TestApplyFlagsOnlyUnpinnedVersions(t *testing.T) {
	n := tree("/h/.rustup", []string{"toolchains"}, map[string]int64{
		"stable-aarch64-apple-darwin": 10,
		"1.90.0-aarch64-apple-darwin": 20,
		"1.91.0-aarch64-apple-darwin": 30,
	})
	it := item("rustup", n)
	rep := &owner.Report{Items: []*owner.Item{it}}
	s := newSet()
	s.add("rust", "stable")
	s.Default["rust"] = "stable-aarch64-apple-darwin"
	Apply(rep, s)

	if len(it.Parts) != 2 {
		t.Fatalf("flagged %d versions, want 2", len(it.Parts))
	}
	if got := it.Reclaimable(); got != 50 {
		t.Errorf("reclaimable = %d, want 50", got)
	}
	if it.Reclaim() != "no project pins" {
		t.Errorf("label = %q", it.Reclaim())
	}
	for _, p := range it.Parts {
		if filepath.Base(p.Path) == "stable-aarch64-apple-darwin" {
			t.Error("the pinned and default toolchain was flagged")
		}
		if p.Owner.Safety != owner.Careful {
			t.Errorf("safety = %v, want careful", p.Owner.Safety)
		}
	}
}

// The default version is never flagged, even when no project pins it.
func TestApplyKeepsTheDefault(t *testing.T) {
	n := tree("/h/.nvm", []string{"versions", "node"}, map[string]int64{"v22.22.1": 10, "v25.4.0": 20})
	it := item("nvm", n)
	s := newSet()
	s.add("node", "22")
	s.Default["node"] = "25.4.0"
	Apply(&owner.Report{Items: []*owner.Item{it}}, s)
	if len(it.Parts) != 0 {
		t.Fatalf("flagged %d versions, want 0", len(it.Parts))
	}
}

// nvm's default is often the alias "node", meaning whichever version is
// newest. chonk cannot resolve that, so it must not call any version
// unpinned: the one in use would be among them.
func TestApplyStopsOnAnAliasDefault(t *testing.T) {
	n := tree("/h/.nvm", []string{"versions", "node"}, map[string]int64{"v22.22.1": 10, "v25.4.0": 20})
	it := item("nvm", n)
	s := newSet()
	s.add("node", "22")
	s.Default["node"] = "node"
	Apply(&owner.Report{Items: []*owner.Item{it}}, s)
	if len(it.Parts) != 0 {
		t.Fatalf("an alias default still flagged %d versions", len(it.Parts))
	}
}

// An alias pin could mean any version, so nothing is called unpinned.
func TestApplyStopsOnAnAlias(t *testing.T) {
	n := tree("/h/.nvm", []string{"versions", "node"}, map[string]int64{"v22.16.0": 10, "v25.4.0": 20})
	it := item("nvm", n)
	s := newSet()
	s.add("node", "lts/*")
	Apply(&owner.Report{Items: []*owner.Item{it}}, s)
	if len(it.Parts) != 0 {
		t.Fatalf("an alias pin still flagged %d versions", len(it.Parts))
	}
}

// nvm cannot be run by chonk, so it offers no command, only the hint.
func TestNvmOffersNoCommand(t *testing.T) {
	for _, m := range managers {
		o := unusedOwner(m)
		if m.ownerID == "nvm" && len(o.Actions) != 0 {
			t.Error("nvm offered a command chonk cannot run")
		}
		if o.Regen == "" || o.Why == "" {
			t.Errorf("%s has no explanation", m.ownerID)
		}
	}
}
