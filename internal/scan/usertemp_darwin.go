//go:build darwin

package scan

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
)

// UserTemp is the directory macOS gives this login for its caches,
// temporary files and clones of running apps: /var/folders/XX/YYYY, holding
// C, T, X and 0. Only this account's own container is returned. The rest of
// /var/folders belongs to the machine's other accounts, which are not this
// scan's to read.
//
// It can hold gigabytes that no directory under your home explains: a
// running app's code-sign clone is a second copy of its whole bundle.
var UserTemp = sync.OnceValue(func() string {
	// getconf asks the system what this login's container is, which is
	// right even when TMPDIR has been pointed somewhere else.
	for _, key := range []string{"DARWIN_USER_CACHE_DIR", "DARWIN_USER_TEMP_DIR"} {
		if d := container(confstr(key)); d != "" {
			return d
		}
	}
	return container(os.TempDir())
})

func confstr(key string) string {
	out, err := exec.Command("/usr/bin/getconf", key).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

// container is the parent of a C/T/X directory, when it really is one of
// this login's per-user directories and it exists.
func container(dir string) string {
	if dir == "" {
		return ""
	}
	parent := filepath.Dir(filepath.Clean(dir))
	// /var/folders/XX/YYYY: two levels under /var/folders, and no deeper,
	// so a path pointed elsewhere never widens the scan.
	if filepath.Dir(filepath.Dir(parent)) != "/var/folders" {
		return ""
	}
	if fi, err := os.Stat(parent); err != nil || !fi.IsDir() {
		return ""
	}
	return parent
}
