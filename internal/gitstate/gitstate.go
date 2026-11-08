// Package gitstate asks git what a working tree holds that only this disk
// has, so chonk can say whether removing a project loses anything.
package gitstate

import (
	"context"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/reclaim"
)

// Run runs one git command in dir. Tests replace it.
type Run func(ctx context.Context, dir string, args ...string) (string, error)

// Runner returns a Run that calls the real git, or nil when git is not
// installed.
func Runner(home string) Run {
	git, err := reclaim.LookPath(home, "git")
	if err != nil {
		return nil
	}
	return func(ctx context.Context, dir string, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, git, args...)
		cmd.Dir = dir
		// Keep git from reading the user's config hooks or prompting.
		cmd.Env = append(cmd.Environ(), "GIT_OPTIONAL_LOCKS=0", "GIT_TERMINAL_PROMPT=0")
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}
}

// Collect asks git about every dir, a few at a time. The whole pass is
// bounded: a tree git cannot answer for in time is left unknown, which
// keeps its project described as cautiously as before.
func Collect(ctx context.Context, run Run, dirs []string) map[string]owner.Git {
	out := map[string]owner.Git{}
	if run == nil || len(dirs) == 0 {
		return out
	}
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var mu sync.Mutex
	var wg sync.WaitGroup
	sem := make(chan struct{}, max(4, min(runtime.NumCPU(), 12)))
	for _, dir := range dirs {
		wg.Go(func() {
			sem <- struct{}{}
			defer func() { <-sem }()
			g := of(ctx, run, dir)
			mu.Lock()
			out[dir] = g
			mu.Unlock()
		})
	}
	wg.Wait()
	return out
}

// of asks git about one working tree.
func of(ctx context.Context, run Run, dir string) owner.Git {
	var g owner.Git
	// A remote that has the history is what makes a clone possible, so ask
	// for it first: without one, nothing else changes the answer.
	remotes, err := run(ctx, dir, "remote")
	if err != nil {
		return g
	}
	g.Known = true
	if name, _, _ := strings.Cut(remotes, "\n"); name != "" {
		if url, err := run(ctx, dir, "remote", "get-url", strings.TrimSpace(name)); err == nil {
			g.Remote = url
		}
	}
	if g.Remote == "" {
		return g
	}

	if out, err := run(ctx, dir, "status", "--porcelain", "--ignored=traditional"); err == nil {
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
	} else {
		g.Known = false
		return g
	}
	if out, err := run(ctx, dir, "log", "--branches", "--not", "--remotes", "--format=%h %D"); err == nil {
		g.Unpushed, g.Branches = unpushed(out)
	}
	if out, err := run(ctx, dir, "stash", "list"); err == nil && out != "" {
		g.Stashes = len(strings.Split(out, "\n"))
	}
	return g
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
