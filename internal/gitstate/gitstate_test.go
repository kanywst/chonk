package gitstate

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
)

// fake answers git commands from a table keyed by the first two arguments.
func fake(t *testing.T, answers map[string]string) Run {
	t.Helper()
	return func(_ context.Context, _ string, args ...string) (string, error) {
		for n := 2; n >= 1; n-- {
			if len(args) >= n {
				if out, ok := answers[strings.Join(args[:n], " ")]; ok {
					return out, nil
				}
			}
		}
		return "", errors.New("no answer for " + strings.Join(args, " "))
	}
}

func TestOfCleanAndPushed(t *testing.T) {
	g := of(context.Background(), fake(t, map[string]string{
		"remote":         "origin",
		"remote get-url": "https://github.com/o/r.git",
		"status":         "",
		"log":            "",
		"stash":          "",
	}), "/p")
	if !g.Known {
		t.Fatal("state came back unknown")
	}
	if g.Remote != "https://github.com/o/r.git" {
		t.Errorf("remote = %q", g.Remote)
	}
	if g.Dirty != 0 || g.Unpushed != 0 || g.Stashes != 0 {
		t.Errorf("a clean tree looked dirty: %+v", g)
	}
}

func TestOfCountsAndSeparatesIgnored(t *testing.T) {
	g := of(context.Background(), fake(t, map[string]string{
		"remote":         "origin",
		"remote get-url": "git@h:o/r.git",
		// Ignored entries must not be counted as uncommitted work.
		"status": " M a.go\n?? b.go\n!! target/\n!! .env",
		"log":    "aaa111 HEAD -> feat/x, feat/x",
		"stash":  "stash@{0}: WIP\nstash@{1}: WIP",
	}), "/p")
	if g.Dirty != 2 {
		t.Errorf("dirty = %d, want 2", g.Dirty)
	}
	if !slices.Equal(g.Ignored, []string{"target/", ".env"}) {
		t.Errorf("ignored = %q", g.Ignored)
	}
	if g.Unpushed != 1 || !slices.Equal(g.Branches, []string{"feat/x"}) {
		t.Errorf("unpushed = %d on %q", g.Unpushed, g.Branches)
	}
	if g.Stashes != 2 {
		t.Errorf("stashes = %d, want 2", g.Stashes)
	}
}

// Without a remote nothing can bring the repository back, so chonk stops
// asking: the answer cannot change.
func TestOfNoRemote(t *testing.T) {
	g := of(context.Background(), fake(t, map[string]string{"remote": ""}), "/p")
	if !g.Known || g.Remote != "" {
		t.Fatalf("got %+v", g)
	}
}

// A directory git refuses to answer for stays unknown, so the project keeps
// its cautious description rather than being called safe.
func TestOfUnreadable(t *testing.T) {
	g := of(context.Background(), func(context.Context, string, ...string) (string, error) {
		return "", errors.New("not a git repository")
	}, "/p")
	if g.Known {
		t.Error("an unreadable tree was reported as known")
	}
}

// A failing status must not leave a tree looking clean.
func TestOfStatusFailureIsUnknown(t *testing.T) {
	g := of(context.Background(), fake(t, map[string]string{
		"remote":         "origin",
		"remote get-url": "git@h:o/r.git",
	}), "/p")
	if g.Known {
		t.Error("a tree whose status failed was reported as known")
	}
}

func TestUnpushed(t *testing.T) {
	// Only a branch tip is decorated; the undecorated commits behind it
	// must still be counted.
	n, names := unpushed("aaa111 HEAD -> main, main\nbbb222 \nccc333 tag: v1, feat/y\n")
	if n != 3 {
		t.Errorf("n = %d, want 3", n)
	}
	// A tag is not a branch; a branch name may contain a slash.
	if !slices.Equal(names, []string{"main", "feat/y"}) {
		t.Errorf("names = %q", names)
	}
	if n, _ := unpushed(""); n != 0 {
		t.Errorf("empty output counted %d commits", n)
	}
}

func TestCollectIsKeyedByDirAndSkipsWithoutGit(t *testing.T) {
	run := fake(t, map[string]string{"remote": ""})
	got := Collect(context.Background(), run, []string{"/a", "/b"})
	if len(got) != 2 {
		t.Fatalf("got %d states, want 2", len(got))
	}
	if !got["/a"].Known || !got["/b"].Known {
		t.Error("a dir came back unknown")
	}
	if n := len(Collect(context.Background(), nil, []string{"/a"})); n != 0 {
		t.Errorf("without git, got %d states", n)
	}
}
