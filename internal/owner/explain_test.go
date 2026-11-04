package owner

import (
	"os"
	"path/filepath"
	"testing"
)

func mk(t *testing.T, path string) {
	t.Helper()
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatal(err)
	}
}

func TestExplain(t *testing.T) {
	h := t.TempDir()
	SetHome(h)
	mk(t, filepath.Join(h, "Library/Application Support/rancher-desktop/lima/0"))
	mk(t, filepath.Join(h, "proj/.git"))
	mk(t, filepath.Join(h, "proj/target/debug"))
	if err := os.WriteFile(filepath.Join(h, "proj/Cargo.toml"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	mk(t, filepath.Join(h, "proj/src"))
	mk(t, filepath.Join(h, "stuff/x"))

	for _, tc := range []struct {
		path, id, claimed, project string
	}{
		{"Library/Application Support/rancher-desktop/lima/0", "rancher-desktop", "Library/Application Support/rancher-desktop/lima", ""},
		{"Library/Application Support/rancher-desktop", "appsupport:rancher-desktop", "Library/Application Support/rancher-desktop", ""},
		{"proj/target/debug", "cargo-target", "proj/target", "proj"},
		{"proj/src", "project", "proj", ""},
		{"stuff/x", "unclaimed", "stuff/x", ""},
	} {
		o, claimed, project := Explain(filepath.Join(h, tc.path))
		wantProject := ""
		if tc.project != "" {
			wantProject = filepath.Join(h, tc.project)
		}
		if o.ID != tc.id || claimed != filepath.Join(h, tc.claimed) || project != wantProject {
			t.Errorf("Explain(%s) = %s %s %s", tc.path, o.ID, claimed, project)
		}
	}
}
