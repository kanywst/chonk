package reclaim

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/kanywst/chonk/internal/owner"
)

func TestTrashRenamesOnClash(t *testing.T) {
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(home, ".Trash"), 0o700); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		p := filepath.Join(home, "target")
		if err := os.Mkdir(p, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := Trash(home, p); err != nil {
			t.Fatal(err)
		}
	}
	for _, n := range []string{"target", "target 2"} {
		if _, err := os.Stat(filepath.Join(home, ".Trash", n)); err != nil {
			t.Errorf("%s not in the Trash: %v", n, err)
		}
	}
}

func TestDo(t *testing.T) {
	home := t.TempDir()
	it := &owner.Item{Owner: &owner.Owner{ID: "x"}, Path: filepath.Join(home, "proj", "target"), Size: 42}
	var got owner.Action
	var gotEnv []string
	run := func(_ context.Context, a owner.Action, env []string) (string, error) {
		got, gotEnv = a, env
		return "line one\nexit status: boom", errors.New("exit 1")
	}
	a := owner.Action{Label: "clean", Cmd: []string{"sh", "-c", "true"}, Dir: "{parent}", Env: []string{"DOCKER_HOST=unix://~/.rd/docker.sock"}}
	res := Do(context.Background(), home, it, a, run)
	if got.Dir != filepath.Join(home, "proj") {
		t.Errorf("dir = %q", got.Dir)
	}
	if res.Error != "exit status: boom" || res.Expected != 42 || res.Command == "" {
		t.Errorf("result = %+v", res)
	}
	want := "DOCKER_HOST=unix://" + home + "/.rd/docker.sock"
	if gotEnv[len(gotEnv)-1] != want {
		t.Errorf("env tail = %q, want %q", gotEnv[len(gotEnv)-1], want)
	}

	res = Do(context.Background(), home, it, owner.Action{Label: "x", Cmd: []string{"no-such-tool-xyz"}, Needs: "no-such-tool-xyz"}, run)
	if res.Error != "no-such-tool-xyz is not installed" {
		t.Errorf("missing tool: %+v", res)
	}
}

func TestLedger(t *testing.T) {
	l := Ledger{Path: filepath.Join(t.TempDir(), "a", "ledger.jsonl")}
	if err := l.Append(Result{Owner: "x", Freed: 5}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(l.Path)
	if err != nil || len(b) == 0 || b[len(b)-1] != '\n' {
		t.Fatalf("ledger = %q %v", b, err)
	}
}

func TestFree(t *testing.T) {
	free, total, err := Free(filepath.Join(t.TempDir(), "does", "not", "exist"))
	if err != nil || free <= 0 || total < free {
		t.Errorf("Free = %d %d %v", free, total, err)
	}
}
