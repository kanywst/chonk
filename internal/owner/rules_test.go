package owner

import (
	"strings"
	"testing"
)

// The /var/folders rules are generated per login, so they only exist once
// the scan has named the container.
func TestTempRules(t *testing.T) {
	t.Cleanup(func() { SetUserTemp("") })

	SetUserTemp("")
	if len(tempRules()) != 0 {
		t.Error("rules were made for a container that was never named")
	}

	SetUserTemp("/var/folders/ab/cd/")
	rs := tempRules()
	if len(rs) == 0 {
		t.Fatal("no rules for a named container")
	}
	// The trailing slash must not survive into the rule paths.
	for _, r := range rs {
		if strings.Contains(r.path, "//") {
			t.Errorf("rule path %q has a doubled separator", r.path)
		}
	}

	// A code-sign clone is named after the app and says quitting it is the
	// way back, since that is what actually frees the space.
	var o Owner
	for _, r := range rs {
		if r.path == "/var/folders/ab/cd/X" {
			o = r.owner("com.google.Chrome.code_sign_clone")
		}
	}
	if !strings.Contains(o.Name, "Chrome") {
		t.Errorf("clone owner name = %q, want it to name the app", o.Name)
	}
	if !strings.Contains(o.Regen, "quits") {
		t.Errorf("clone regen = %q, want it to say the app quitting frees it", o.Regen)
	}
	if o.Safety == Safe {
		t.Error("a clone of a running app was called safe to remove")
	}
}

// The live per-user cache and TMPDIR must never carry an action that moves
// the directory itself: every running process is using it, and macOS only
// recreates it at the next login.
func TestTempRulesOfferNoTrashOnLiveDirs(t *testing.T) {
	t.Cleanup(func() { SetUserTemp("") })
	SetUserTemp("/var/folders/ab/cd")
	for _, r := range tempRules() {
		for _, want := range []string{"/var/folders/ab/cd", "/var/folders/ab/cd/C", "/var/folders/ab/cd/T"} {
			if r.path != want || r.each {
				continue
			}
			o := r.owner("")
			for _, a := range o.Actions {
				if a.Trash {
					t.Errorf("%s offers to trash the live directory itself", want)
				}
			}
		}
	}
}
