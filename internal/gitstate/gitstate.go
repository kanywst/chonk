// Package gitstate asks git what a working tree holds that only this disk
// has, so chonk can say whether removing a project loses anything.
package gitstate

import (
	"context"
	"net/url"
	"os/exec"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/reclaim"
)

// Run runs one git command in dir. Tests replace it.
type Run func(ctx context.Context, dir string, args ...string) (string, error)

// safeFlags go before every subcommand. git runs inside directories found
// on disk, and a repository's own .git/config is read there: core.fsmonitor
// names a program `git status` executes, and a tree that was copied or
// unpacked rather than cloned can carry one. A -c on the command line wins
// over any config file, so these turn that off for every call.
var safeFlags = []string{
	"--no-optional-locks",
	"-c", "core.fsmonitor=false",
	"-c", "core.hooksPath=/dev/null",
}

// Runner returns a Run that calls the real git, or nil when git is not
// installed.
func Runner(home string) Run {
	git, err := reclaim.LookPath(home, "git")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, append(safeFlags, args...)...)
		cmd.Dir = dir
		cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
}

// Collect asks git about every dir, a few at a time. Each tree gets its own
// deadline rather than a share of one budget: a slow repository must not
// make the next one look clean because the time ran out.
func Collect(ctx context.Context, run Run, dirs []string) map[string]owner.Git {
	out := map[string]owner.Git{}
	if run == nil || len(dirs) == 0 {
		return out
	}
	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(4, min(runtime.NumCPU(), 12)))
	for _, dir := range dirs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			dctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			defer cancel()
			g := of(dctx, run, dir)
			mu.Lock()
			out[dir] = g
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

// remoteName is what a remote may be called for chonk to pass it back to
// git. Anything else comes from .git/config and is not worth arguing with.
var remoteName = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]*$`)

// of asks git about one working tree. Every question that fails leaves the
// state unknown rather than empty: an unanswered tree keeps the cautious
// description it had, while a tree that looks clean because git never
// answered would be offered for removal.
func of(ctx context.Context, run Run, dir string) owner.Git {
	var g owner.Git
	// A remote that has the history is what makes a clone possible, so ask
	// for it first: without one, nothing else changes the answer.
	remotes, err := run(ctx, dir, "remote")
	if err != nil {
		return g
	}
	g.Known = true
	for name := range strings.SplitSeq(remotes, "\n") {
		name = strings.TrimSpace(name)
		if !remoteName.MatchString(name) {
			continue
		}
		url, err := run(ctx, dir, "remote", "get-url", name)
		if err != nil {
			continue
		}
		if u := elsewhere(url); u != "" {
			g.Remote = u
			break
		}
	}
	if g.Remote == "" {
		return g
	}

	out, err := run(ctx, dir, "status", "--porcelain", "--ignored=traditional")
	if err != nil {
		g.Known = false
		return g
	}
	for line := range strings.SplitSeq(out, "\n") {
		if len(line) < 4 {
			continue
		}
		if path, ok := strings.CutPrefix(line, "!! "); ok {
			g.Ignored = append(g.Ignored, path)
			continue
		}
		g.Dirty++
	}

	// HEAD and --tags as well as --branches: a commit made on a detached
	// HEAD, or left only under a tag, is on this disk and nowhere else.
	out, err = run(ctx, dir, "log", "HEAD", "--branches", "--tags", "--not", "--remotes", "--format=%h %D")
	if err != nil {
		g.Known = false
		return g
	}
	g.Unpushed, g.Branches = unpushed(out)

	out, err = run(ctx, dir, "stash", "list")
	if err != nil {
		g.Known = false
		return g
	}
	if out != "" {
		g.Stashes = len(strings.Split(out, "\n"))
	}
	return g
}

// elsewhere is the remote URL with any credential stripped, when the remote
// is somewhere other than this disk. A path or a file:// URL is another
// directory on the same machine, which a scan may be about to clean out
// too, so it is not somewhere the history is safe.
func elsewhere(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	// scp-style, git@host:path/to.git: a host, so not this disk.
	if !strings.Contains(raw, "://") {
		host, _, ok := strings.Cut(raw, ":")
		if !ok || host == "" || strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, ".") {
			return ""
		}
		if _, user, hasUser := strings.Cut(host, "@"); hasUser && user == "" {
			return ""
		}
		return raw
	}
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || u.Scheme == "file" {
		return ""
	}
	// A URL can carry a token in its userinfo, and the remote is shown in
	// the view and in --json, so it never keeps the credential.
	if u.User != nil {
		if name := u.User.Username(); name != "" {
			u.User = url.User(name)
		} else {
			u.User = nil
		}
	}
	return u.String()
}

// unpushed counts the commits no remote has and names the branches they sit
// on, from `--format=%h %D` lines: the hash makes every commit a non-empty
// line to count, and %D carries the refs pointing at it, so the branch names
// come out of the same walk. Only a commit at a branch tip is decorated,
// which is why the count and the names are read separately.
func unpushed(out string) (int, []string) {
	out = strings.TrimSpace(out)
	if out == "" {
		return 0, nil
	}
	var names []string
	seen := map[string]bool{}
	lines := strings.Split(out, "\n")
	for _, line := range lines {
		_, decor, _ := strings.Cut(line, " ")
		for ref := range strings.SplitSeq(decor, ", ") {
			ref = strings.TrimSpace(strings.TrimPrefix(ref, "HEAD -> "))
			// A branch name may contain a slash, so only tags and the
			// detached HEAD are dropped. By construction no remote ref can
			// point at a commit this walk returned.
			if ref == "" || ref == "HEAD" || strings.HasPrefix(ref, "tag: ") || seen[ref] {
				continue
			}
			seen[ref] = true
			names = append(names, ref)
		}
	}
	return len(lines), names
}
