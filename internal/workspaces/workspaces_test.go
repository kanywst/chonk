package workspaces

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/walk"
)

func TestLocalPath(t *testing.T) {
	cases := []struct{ uri, want string }{
		{"file:///Users/x/p", "/Users/x/p"},
		{"file:///Users/x/a%20b", "/Users/x/a b"},
		// A workspace on another machine or an unmounted volume is not gone.
		{"vscode-remote://ssh-remote%2Bhost/home/x/p", ""},
		{"file:///Volumes/External/p", ""},
		{"", ""},
		{"file://relative", ""},
	}
	for _, tc := range cases {
		if got := localPath(tc.uri); got != tc.want {
			t.Errorf("localPath(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

func TestStale(t *testing.T) {
	dir := t.TempDir()
	gone := filepath.Join(dir, "gone")
	here := filepath.Join(dir, "here")
	if err := os.Mkdir(here, 0o755); err != nil {
		t.Fatal(err)
	}

	write := func(name, body string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}
	cases := []struct {
		name string
		body string
		want bool
	}{
		{"missing.json", `{"folder":"file://` + gone + `"}`, true},
		{"present.json", `{"folder":"file://` + here + `"}`, false},
		{"multiroot.json", `{"workspace":"file://` + gone + `/x.code-workspace"}`, true},
		// Nothing checkable means not stale, never the other way round.
		{"untitled.json", `{}`, false},
		{"broken.json", `not json`, false},
		{"remote.json", `{"folder":"vscode-remote://ssh-remote%2Bh/p"}`, false},
	}
	for _, tc := range cases {
		if got := stale(write(tc.name, tc.body)); got != tc.want {
			t.Errorf("%s: stale = %v, want %v", tc.name, got, tc.want)
		}
	}
	if stale(filepath.Join(dir, "no-such-file.json")) {
		t.Error("a workspace with no workspace.json was called stale")
	}
}

func TestApply(t *testing.T) {
	home := t.TempDir()
	here := filepath.Join(home, "project")
	if err := os.Mkdir(here, 0o755); err != nil {
		t.Fatal(err)
	}

	// Two stored workspaces: one whose folder is gone, one still here.
	ws := filepath.Join(home, "Code/User/workspaceStorage")
	for name, folder := range map[string]string{
		"aaa": filepath.Join(home, "deleted"),
		"bbb": here,
	} {
		d := filepath.Join(ws, name)
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
		body := `{"folder":"file://` + folder + `"}`
		if err := os.WriteFile(filepath.Join(d, "workspace.json"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	code := node(filepath.Join(home, "Code"), nil)
	user := node("User", code)
	store := node("workspaceStorage", user)
	a := node("aaa", store)
	a.Size = 40
	b := node("bbb", store)
	b.Size = 60

	o := owner.Owner{ID: "appsupport:Code", Kind: owner.KindApp, Regen: "The app may lose settings."}
	it := &owner.Item{Owner: &o, Path: code.Name, Node: code, Size: 100}
	Apply(&owner.Report{Items: []*owner.Item{it}})

	if len(it.Parts) != 1 {
		t.Fatalf("flagged %d workspaces, want 1", len(it.Parts))
	}
	if filepath.Base(it.Parts[0].Path) != "aaa" {
		t.Errorf("flagged %q, want the one whose folder is gone", it.Parts[0].Path)
	}
	if it.Reclaimable() != 40 {
		t.Errorf("reclaimable = %d, want 40", it.Reclaimable())
	}
	if it.Reclaim() != "for folders that are gone" {
		t.Errorf("label = %q", it.Reclaim())
	}
	if it.Parts[0].Owner.Safety != owner.Safe {
		t.Errorf("safety = %v, want safe", it.Parts[0].Owner.Safety)
	}
}

// An app that is not one of the editors is left alone.
func TestApplyIgnoresOtherApps(t *testing.T) {
	o := owner.Owner{ID: "appsupport:Slack", Kind: owner.KindApp}
	it := &owner.Item{Owner: &o, Path: "/h/Slack", Node: node("/h/Slack", nil)}
	Apply(&owner.Report{Items: []*owner.Item{it}})
	if len(it.Parts) != 0 {
		t.Error("a non-editor app was given workspace parts")
	}
}

func node(name string, parent *walk.Node) *walk.Node {
	n := &walk.Node{Name: name, Parent: parent}
	if parent != nil {
		parent.Children = append(parent.Children, n)
	}
	return n
}
