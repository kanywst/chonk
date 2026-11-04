package owner

import (
	"path/filepath"
	"testing"

	"github.com/kanywst/chonk/internal/walk"
)

const gib = int64(1) << 30

// tree builds a walk tree from paths and sizes. Sizes are leaf directory
// sizes; parents add up their children.
func tree(root string, leaves map[string]int64, markers map[string]walk.Marker) *walk.Node {
	r := &walk.Node{Name: root}
	for p, size := range leaves {
		n := r
		for _, part := range splitAll(p) {
			c := n.Child(part)
			if c == nil {
				c = &walk.Node{Name: part, Parent: n}
				n.Children = append(n.Children, c)
			}
			n = c
		}
		n.Own += size
	}
	for p, m := range markers {
		n := r
		for _, part := range splitAll(p) {
			n = n.Child(part)
		}
		n.Markers |= m
	}
	var sum func(n *walk.Node) int64
	sum = func(n *walk.Node) int64 {
		n.Size = n.Own
		for _, c := range n.Children {
			n.Size += sum(c)
		}
		// biggest first, as walk does
		for i := 1; i < len(n.Children); i++ {
			for j := i; j > 0 && n.Children[j].Size > n.Children[j-1].Size; j-- {
				n.Children[j], n.Children[j-1] = n.Children[j-1], n.Children[j]
			}
		}
		return n.Size
	}
	sum(r)
	return r
}

func splitAll(p string) []string {
	var out []string
	for p != "." && p != "" {
		out = append([]string{filepath.Base(p)}, out...)
		p = filepath.Dir(p)
	}
	return out
}

func find(t *testing.T, r *Report, path string) *Item {
	t.Helper()
	for _, it := range r.Items {
		if it.Path == path {
			return it
		}
	}
	t.Fatalf("no item at %s; have %v", path, paths(r))
	return nil
}

func paths(r *Report) []string {
	var out []string
	for _, it := range r.Items {
		out = append(out, it.Path)
	}
	return out
}

func TestClassify(t *testing.T) {
	SetHome("/home/u")
	h := tree("/home/u", map[string]int64{
		"Library/Application Support/rancher-desktop/lima/0":   80 * gib,
		"Library/Application Support/rancher-desktop/electron": gib / 8,
		"Library/Caches/com.spotify.client":                    2 * gib,
		"Library/Preferences":                                  gib / 100,
		"proj/src":                                             gib,
		"proj/target/debug":                                    10 * gib,
		"proj/vendor/sub/.git":                                 gib / 100,
		"proj/vendor/sub/node_modules/x":                       3 * gib,
		"mystery/deep/blob":                                    5 * gib,
		"mystery/deep/other":                                   gib / 10,
		"tiny":                                                 1 << 20,
		".local/share/containers/podman/machine/applehv":       16 * gib,
	}, map[string]walk.Marker{
		"proj":            walk.MarkGit | walk.MarkCargo,
		"proj/vendor/sub": walk.MarkGit | walk.MarkPackageJSON,
	})
	rep := Classify([]*walk.Node{h}, Options{})

	var sum int64
	for _, it := range rep.Items {
		sum += it.Size
	}
	if sum != rep.Total || rep.Total != h.Size {
		t.Fatalf("items add up to %d, total %d, tree %d: every byte must be counted once", sum, rep.Total, h.Size)
	}

	vm := find(t, rep, "/home/u/Library/Application Support/rancher-desktop/lima")
	if vm.Owner.ID != "rancher-desktop" || vm.Size != 80*gib {
		t.Errorf("rancher = %s %d", vm.Owner.ID, vm.Size)
	}
	app := find(t, rep, "/home/u/Library/Application Support/rancher-desktop")
	if app.Owner.Kind != KindApp || app.Size != gib/8 {
		t.Errorf("the app's own data should keep only what the VM rule did not take: %d", app.Size)
	}
	if it := find(t, rep, "/home/u/Library/Caches/com.spotify.client"); it.Owner.Name != "Spotify cache" || it.Owner.Safety != Safe {
		t.Errorf("spotify = %+v", it.Owner)
	}
	if it := find(t, rep, "/home/u/.local/share/containers/podman/machine"); it.Owner.Kind != KindVM {
		t.Errorf("podman = %+v", it.Owner)
	}

	p := find(t, rep, "/home/u/proj")
	if p.Owner.Kind != KindProject {
		t.Fatalf("proj kind = %v", p.Owner.Kind)
	}
	if len(p.Parts) != 1 || p.Parts[0].Owner.ID != "cargo-target" || p.Parts[0].Size != 10*gib {
		t.Errorf("proj parts = %v", p.Parts)
	}
	sub := find(t, rep, "/home/u/proj/vendor/sub")
	if sub.Reclaimable() != 3*gib {
		t.Errorf("nested repo's node_modules = %d", sub.Reclaimable())
	}
	if p.Size != 11*gib {
		t.Errorf("proj size = %d, want 11 GiB (the nested repo is its own project)", p.Size)
	}

	m := find(t, rep, "/home/u/mystery")
	if m.Owner.Kind != KindUnclaimed || m.Hotspot != "/home/u/mystery/deep/blob" {
		t.Errorf("mystery = %v hotspot %q", m.Owner.Kind, m.Hotspot)
	}

	ks := rep.KindsBySize()
	if ks[0] != KindVM || ks[len(ks)-1] != KindUnclaimed {
		t.Errorf("kinds = %v", ks)
	}

	it, part := rep.Why("/home/u/proj/target/debug/foo")
	if it != p || part == nil || part.Owner.ID != "cargo-target" {
		t.Errorf("Why = %v %v", it, part)
	}
}

func TestActionExpandAndCommand(t *testing.T) {
	a := Action{Cmd: []string{"cargo", "clean"}, Dir: "{parent}", Env: []string{"X=1"}}.Expand("/p/my proj/target")
	if a.Dir != "/p/my proj" {
		t.Errorf("dir = %q", a.Dir)
	}
	if got := a.Command(); got != "X=1 cd '/p/my proj' && cargo clean" {
		t.Errorf("command = %q", got)
	}
	if got := (Action{Trash: true}).Command(); got != "move to the Trash" {
		t.Errorf("trash command = %q", got)
	}
}

func TestPrettyBundle(t *testing.T) {
	for in, want := range map[string]string{
		"com.spotify.client":           "Spotify",
		"VUTU7AKEUR.jp.naver.line.mac": "Line",
		"Google":                       "Google",
		"com.microsoft.VSCode":         "VSCode",
	} {
		if got := prettyBundle(in); got != want {
			t.Errorf("prettyBundle(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTilde(t *testing.T) {
	SetHome("/home/u")
	if Tilde("/home/u/x") != "~/x" || Tilde("/home/user2") != "/home/user2" || Untilde("~/x") != "/home/u/x" {
		t.Error("tilde round trip")
	}
}

func TestClassifyReviewCases(t *testing.T) {
	SetHome("/home/u")
	h := tree("/home/u", map[string]int64{
		"Library/Developer/Toolchains":        4 * gib,
		"Library/Developer/Xcode/DerivedData": 2 * gib,
		"Library/Developer/Xcode/UserData":    3 * gib,
		"Documents/code/app/.git":             gib / 100,
		"Documents/code/app/node_modules/x":   gib,
		"Documents/notes":                     gib,
		".lima/_config":                       gib / 10,
		".lima/default":                       5 * gib,
		"vendored/.git":                       gib / 100,
		"vendored/deps/redis":                 gib,
		"scattered/a":                         1 << 20,
	}, map[string]walk.Marker{
		"Documents/code/app": walk.MarkGit | walk.MarkPackageJSON,
		"vendored":           walk.MarkGit,
	})
	rep := Classify([]*walk.Node{h}, Options{})
	var sum int64
	for _, it := range rep.Items {
		sum += it.Size
		if it.Owner.Kind == KindUnclaimed && it.Path == "/home/u" && len(it.Owner.Actions) > 0 {
			t.Errorf("%s (%s) must offer no action", it.Name(), it.Path)
		}
	}
	if sum != rep.Total {
		t.Fatalf("sum %d != total %d", sum, rep.Total)
	}
	lib := find(t, rep, "/home/u/Library")
	if lib.Owner.ID != "library" || lib.Size != 7*gib {
		t.Errorf("Library (other) = %s %d, want the 7 GiB no specific rule took", lib.Owner.ID, lib.Size)
	}
	app := find(t, rep, "/home/u/Documents/code/app")
	if app.Owner.Kind != KindProject || app.Reclaimable() != gib {
		t.Errorf("project in Documents = %v %d", app.Owner.Kind, app.Reclaimable())
	}
	if docs := find(t, rep, "/home/u/Documents"); docs.Size != gib {
		t.Errorf("Documents = %d, want only the notes", docs.Size)
	}
	for _, it := range rep.Items {
		if it.Owner.ID == "lima:_config" {
			t.Error("~/.lima/_config is not a VM")
		}
	}
	if v := find(t, rep, "/home/u/vendored"); len(v.Parts) != 0 {
		t.Errorf("vendored deps/ without mix.exs is not a build output: %v", v.Parts)
	}
}
