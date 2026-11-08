package scan

import (
	"slices"
	"testing"
	"time"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/walk"
)

func TestSnapshotPartial(t *testing.T) {
	lib := &walk.Node{Name: "Library"}
	lib.Children = []*walk.Node{{Name: "Caches", Parent: lib}, {Name: "Mail", Parent: lib, Denied: true}}
	rep := &owner.Report{Total: 30, Items: []*owner.Item{
		{Owner: &owner.Owner{ID: "unclaimed"}, Path: "/x/Library", Node: lib, Size: 10},
		{Owner: &owner.Owner{ID: "go"}, Path: "/x/go", Node: &walk.Node{Name: "go"}, Size: 20},
	}}
	s := Snapshot(rep, time.Now())
	if !slices.Equal(s.Partial, []string{"/x/Library"}) {
		t.Errorf("partial = %v", s.Partial)
	}
	if s.Items["/x/go"] != 20 {
		t.Errorf("items = %v", s.Items)
	}
}

// Roots reaches past your own files, to the places outside the home
// directory that hold space with one known owner.
func TestRootsCoverSystemPlaces(t *testing.T) {
	got := Roots("/h")
	for _, want := range []string{"/h", "/Applications", "/opt/homebrew", "/usr/local", "/nix", "/Library/Updates", "/private/var/vm"} {
		if !slices.Contains(got, want) {
			t.Errorf("Roots is missing %q: %q", want, got)
		}
	}
}
