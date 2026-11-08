// Package reclaim runs an owner's action and measures what it gave back.
package reclaim

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/kanywst/chonk/internal/owner"
)

// Result is what one action did.
type Result struct {
	Time     time.Time `json:"time"`
	Owner    string    `json:"owner"`
	Path     string    `json:"path"`
	Action   string    `json:"action"`
	Command  string    `json:"command"`
	Expected int64     `json:"expected_bytes"`
	// Freed is the change in free space on the item's volume. Other
	// programs writing at the same time make it approximate.
	Freed   int64  `json:"freed_bytes"`
	Trashed bool   `json:"trashed,omitempty"`
	Output  string `json:"output,omitempty"`
	Error   string `json:"error,omitempty"`
}

// Free is the space available to an unprivileged user on path's volume.
func Free(path string) (free, total int64, err error) {
	var st syscall.Statfs_t
	for p := path; ; p = filepath.Dir(p) {
		if err = syscall.Statfs(p, &st); err == nil || p == "/" {
			break
		}
	}
	if err != nil {
		return 0, 0, err
	}
	bs := int64(st.Bsize)                                    //nolint:unconvert // already int64 on linux
	return int64(st.Bavail) * bs, int64(st.Blocks) * bs, nil //nolint:gosec // same
}

// Env is the environment actions run with: PATH gets the places tools that
// chonk calls are installed to, since a GUI-launched terminal or a plain
// shell may not have them.
func Env(home string, extra []string) []string {
	path := strings.Join([]string{
		filepath.Join(home, ".rd", "bin"),
		"/opt/homebrew/bin",
		"/usr/local/bin",
		"/run/current-system/sw/bin",
		"/nix/var/nix/profiles/default/bin",
		filepath.Join(home, ".nix-profile", "bin"),
		filepath.Join(home, ".local", "share", "mise", "shims"),
		filepath.Join(home, ".cargo", "bin"),
		filepath.Join(home, "go", "bin"),
		os.Getenv("PATH"),
		"/usr/bin", "/bin", "/usr/sbin", "/sbin",
	}, ":")
	env := append(os.Environ(), "PATH="+path)
	for _, e := range extra {
		env = append(env, strings.ReplaceAll(e, "~/", home+"/"))
	}
	return env
}

// LookPath finds a program the way an action would.
func LookPath(home, name string) (string, error) {
	for _, e := range Env(home, nil) {
		if p, ok := strings.CutPrefix(e, "PATH="); ok {
			for dir := range strings.SplitSeq(p, ":") {
				if dir == "" {
					continue
				}
				full := filepath.Join(dir, name)
				if fi, err := os.Stat(full); err == nil && !fi.IsDir() && fi.Mode()&0o111 != 0 {
					return full, nil
				}
			}
		}
	}
	return "", fmt.Errorf("%s is not installed", name)
}

// Runner runs one action. Tests replace it.
type Runner func(ctx context.Context, a owner.Action, env []string) (string, error)

// Exec runs a for real.
func Exec(ctx context.Context, a owner.Action, env []string) (string, error) {
	cmd := exec.CommandContext(ctx, a.Cmd[0], a.Cmd[1:]...)
	cmd.Dir = a.Dir
	cmd.Env = env
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	err := cmd.Run()
	return strings.TrimSpace(out.String()), err
}

// Do runs action a on item it and measures the free space before and after.
// Moving to the Trash frees nothing until the Trash is emptied, and the
// result says so.
func Do(ctx context.Context, home string, it *owner.Item, a owner.Action, run Runner) Result {
	a = a.Expand(it.Path)
	r := Result{Time: time.Now(), Owner: it.Owner.ID, Path: it.Path, Action: a.Label, Command: a.Command(), Expected: it.Size}
	before, _, _ := Free(it.Path)
	var err error
	switch {
	case a.Trash:
		err = Trash(home, it.Path)
		r.Trashed = err == nil
	case a.Needs != "" && func() bool { _, e := LookPath(home, a.Needs); return e != nil }():
		err = fmt.Errorf("%s is not installed", a.Needs)
	default:
		// Run the program the check above found, not whatever comes first
		// on this process's own PATH.
		if p, e := LookPath(home, a.Cmd[0]); e == nil {
			a.Cmd[0] = p
		}
		r.Output, err = run(ctx, a, Env(home, a.Env))
	}
	if err != nil {
		r.Error = lastLine(r.Output)
		if r.Error == "" {
			r.Error = err.Error()
		}
	}
	// APFS frees blocks a moment after the last close.
	time.Sleep(300 * time.Millisecond)
	after, _, _ := Free(it.Path)
	r.Freed = after - before
	return r
}

func lastLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.LastIndexByte(s, '\n'); i >= 0 {
		return s[i+1:]
	}
	return s
}

// Trash moves path into the user's Trash, renaming it if the name is taken.
func Trash(home, path string) error {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return fmt.Errorf("refusing to trash a relative path: %s", path)
	}
	// Never the home, a scan root, or anything holding the Trash itself.
	refuse := []string{"/", home, "/Applications", "/opt/homebrew", "/usr/local", "/nix",
		"/Library/Updates", "/private/var/vm", filepath.Join(home, "Library")}
	// The per-user container and the directories in it are the live cache
	// and TMPDIR of every running process, so they are refused by name
	// even if some rule ever offers them.
	if c := owner.UserTempDir(); c != "" {
		refuse = append(refuse, c)
		for _, d := range []string{"C", "T", "X", "0"} {
			refuse = append(refuse, filepath.Join(c, d))
		}
	}
	for _, p := range refuse {
		if path == filepath.Clean(p) {
			return fmt.Errorf("refusing to trash %s", path)
		}
	}
	dir := filepath.Join(home, ".Trash")
	if path == dir || strings.HasPrefix(dir, path+"/") {
		return fmt.Errorf("refusing to trash %s: it holds the Trash", path)
	}
	if _, err := os.Stat(dir); err != nil {
		dir = filepath.Join(home, ".local", "share", "Trash", "files")
		if err := os.MkdirAll(dir, 0o700); err != nil {
			return err
		}
	}
	base := filepath.Base(path)
	dst := filepath.Join(dir, base)
	for i := 2; ; i++ {
		_, err := os.Lstat(dst)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return err
		}
		if i > 1000 {
			return fmt.Errorf("the Trash already holds 1000 items called %s", base)
		}
		dst = filepath.Join(dir, fmt.Sprintf("%s %d", base, i))
	}
	if err := os.Rename(path, dst); err != nil {
		var le *os.LinkError
		if errors.As(err, &le) && errors.Is(le.Err, syscall.EXDEV) {
			return fmt.Errorf("%s is on another volume than the Trash; remove it yourself", path)
		}
		return err
	}
	return nil
}

// Ledger appends results to a JSON-lines file.
type Ledger struct{ Path string }

// Append records r.
func (l Ledger) Append(r Result) error {
	if l.Path == "" {
		return nil
	}
	if err := os.MkdirAll(filepath.Dir(l.Path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(l.Path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	b, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = f.Write(append(b, '\n'))
	return err
}
