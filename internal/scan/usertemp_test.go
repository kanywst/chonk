//go:build darwin

package scan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// container accepts only a real directory two levels under /var/folders, so
// a TMPDIR pointed elsewhere never widens the scan.
func TestContainerRejectsPathsOutsideVarFolders(t *testing.T) {
	for _, dir := range []string{
		"", "/tmp/claude-501/T", "/var/folders/T", "/var/folders/a/b/c/T",
		filepath.Join(t.TempDir(), "T"),
	} {
		if got := container(dir); got != "" {
			t.Errorf("container(%q) = %q, want empty", dir, got)
		}
	}
}

// On a real macOS login the container exists and holds the per-user
// directories, so the resolver must find it.
func TestUserTempFindsThisLoginsContainer(t *testing.T) {
	got := UserTemp()
	if got == "" {
		t.Skip("no /var/folders container for this login")
	}
	if !strings.HasPrefix(got, "/var/folders/") {
		t.Fatalf("UserTemp() = %q, want a path under /var/folders", got)
	}
	if fi, err := os.Stat(got); err != nil || !fi.IsDir() {
		t.Fatalf("UserTemp() = %q, which is not a directory", got)
	}
	// It is the container, not one of the directories inside it.
	for _, inside := range []string{"C", "T"} {
		if filepath.Base(got) == inside {
			t.Errorf("UserTemp() = %q, want its parent", got)
		}
	}
}
