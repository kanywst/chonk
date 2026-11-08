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
		"config":         "",
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
		"config":         "",
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
	g := of(context.Background(), fake(t, map[string]string{"remote": "", "config": ""}), "/p")
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

// A question git does not answer must leave the tree unknown, never
// looking clean: a clean tree is offered for removal.
func TestOfFailuresAreUnknownNotClean(t *testing.T) {
	base := map[string]string{
		"remote":         "origin",
		"remote get-url": "git@h:o/r.git",
		"config":         "",
		"status":         "",
		"log":            "",
		"stash":          "",
	}
	for _, missing := range []string{"status", "log", "stash"} {
		answers := map[string]string{}
		for k, v := range base {
			if k != missing {
				answers[k] = v
			}
		}
		g := of(context.Background(), fake(t, answers), "/p")
		if g.Known {
			t.Errorf("a tree whose %q failed was reported as known, so it reads as clean", missing)
		}
	}
}

// Commits on a detached HEAD or under a tag alone are on this disk and
// nowhere else, so the walk must include them.
func TestOfAsksAboutHeadAndTags(t *testing.T) {
	var got []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "log" {
			got = args
		}
		switch args[0] {
		case "remote":
			if len(args) > 1 {
				return "git@h:o/r.git", nil
			}
			return "origin", nil
		}
		return "", nil
	}
	of(context.Background(), run, "/p")
	for _, want := range []string{"HEAD", "--branches", "--tags", "--not", "--remotes"} {
		if !slices.Contains(got, want) {
			t.Errorf("log args %q are missing %q", got, want)
		}
	}
}

func TestElsewhere(t *testing.T) {
	cases := []struct{ in, want string }{
		{"git@github.com:o/r.git", "git@github.com:o/r.git"},
		{"https://github.com/o/r.git", "https://github.com/o/r.git"},
		{"ssh://git@h/o/r.git", "ssh://git@h/o/r.git"},
		// A credential in the URL is shown in the view and in --json.
		{"https://user:token@h/o/r.git", "https://user@h/o/r.git"},
		{"https://:token@h/o/r.git", "https://h/o/r.git"},
		// Another directory on this disk is not somewhere the history is
		// safe: the same scan may be about to clean it out.
		{"/srv/bare.git", ""},
		{"../bare.git", ""},
		{"./bare.git", ""},
		{"file:///srv/bare.git", ""},
		{"", ""},
	}
	for _, tc := range cases {
		if got := elsewhere(tc.in); got != tc.want {
			t.Errorf("elsewhere(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// A remote whose name did not come from chonk is not passed back to git.
func TestOfSkipsStrangeRemoteNames(t *testing.T) {
	var asked []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "remote" && len(args) > 1 {
			asked = append(asked, args[len(args)-1])
			return "git@h:o/r.git", nil
		}
		if args[0] == "remote" {
			return "--upload-pack=touch\norigin", nil
		}
		return "", nil
	}
	of(context.Background(), run, "/p")
	if slices.Contains(asked, "--upload-pack=touch") {
		t.Error("a remote name shaped like a flag was passed to git")
	}
	if !slices.Contains(asked, "origin") {
		t.Errorf("the real remote was not asked about: %q", asked)
	}
}

// Every call carries the flags that stop a repository's own config naming a
// program for git to run.
func TestRunnerPinsConfigOff(t *testing.T) {
	for _, want := range []string{"--no-optional-locks", "core.fsmonitor=false", "core.hooksPath=/dev/null"} {
		if !slices.Contains(safeFlags, want) {
			t.Errorf("safeFlags %q is missing %q", safeFlags, want)
		}
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
	run := fake(t, map[string]string{"remote": "", "config": ""})
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

// A repository can hide its own untracked files with
// status.showUntrackedFiles=no, so the flag has to override the config.
func TestOfOverridesUntrackedFilesConfig(t *testing.T) {
	var got []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		if args[0] == "status" {
			got = args
		}
		if args[0] == "remote" && len(args) > 1 {
			return "git@h:o/r.git", nil
		}
		if args[0] == "remote" {
			return "origin", nil
		}
		return "", nil
	}
	of(context.Background(), run, "/p")
	if !slices.Contains(got, "--untracked-files=normal") {
		t.Errorf("status args %q do not override showUntrackedFiles", got)
	}
}

// `git status` runs a clean filter to see whether a file changed, and a -c
// cannot clear a key with a name in the middle. A repository that configures
// one is left unknown rather than run in.
func TestNamesAProgram(t *testing.T) {
	bad := []string{
		"core.fsmonitor=/tmp/evil",
		"filter.x.clean=/tmp/evil",
		"filter.x.process=sh -c id",
		"diff.x.textconv=/tmp/evil",
		"core.hookspath=/tmp/hooks",
		"credential.helper=/tmp/evil",
		"alias.st=!/tmp/evil",
		"core.sshcommand=/tmp/evil",
	}
	for _, line := range bad {
		if !namesAProgram(line) {
			t.Errorf("%q was not recognised as naming a program", line)
		}
	}
	// git-lfs is on nearly every large repository, and ordinary settings
	// must not make a tree unknown.
	ok := []string{
		"filter.lfs.clean=git-lfs clean -- %f",
		"filter.lfs.process=git-lfs filter-process",
		"filter.lfs.smudge=/opt/homebrew/bin/git-lfs smudge -- %f",
		"remote.origin.url=git@h:o/r.git",
		"core.bare=false",
		"core.fsmonitor=false",
		"",
	}
	for _, line := range ok {
		if namesAProgram(line) {
			t.Errorf("%q was wrongly recognised as naming a program", line)
		}
	}
}

// A repository whose config names a program is never run in: not status,
// not log, nothing.
func TestOfSkipsRepositoriesThatNameAProgram(t *testing.T) {
	var ran []string
	run := func(_ context.Context, _ string, args ...string) (string, error) {
		ran = append(ran, args[0])
		switch args[0] {
		case "remote":
			if len(args) > 1 {
				return "git@h:o/r.git", nil
			}
			return "origin", nil
		case "config":
			return "filter.evil.clean=/tmp/evil", nil
		}
		return "", nil
	}
	g := of(context.Background(), run, "/p")
	if g.Known {
		t.Error("a repository that names a program was reported as known")
	}
	if slices.Contains(ran, "status") || slices.Contains(ran, "log") {
		t.Errorf("chonk ran %q inside it anyway", ran)
	}
}
