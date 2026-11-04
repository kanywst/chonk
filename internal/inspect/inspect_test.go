package inspect

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kanywst/chonk/internal/owner"
)

func TestGuestUsed(t *testing.T) {
	out := "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 102625208 3781480 93584516 4% /mnt/data\n"
	if n, ok := guestUsed(out); !ok || n != 3781480*1024 {
		t.Errorf("guestUsed = %d %v", n, ok)
	}
	if _, ok := guestUsed("garbage"); ok {
		t.Error("garbage parsed")
	}
}

func TestDockerDF(t *testing.T) {
	out := `{"Active":"1","Reclaimable":"1.276GB (100%)","Size":"1.276GB","TotalCount":"1","Type":"Images"}
{"Active":"0","Reclaimable":"0B","Size":"0B","TotalCount":"0","Type":"Build Cache"}`
	lines := dockerDF(out)
	if len(lines) != 2 || lines[0].Label != "Docker images" || !strings.Contains(lines[0].Value, "reclaimable") || strings.Contains(lines[1].Value, "reclaimable") {
		t.Errorf("lines = %+v", lines)
	}
}

func TestRancherTrapped(t *testing.T) {
	dir := t.TempDir()
	disk := filepath.Join(dir, "0", "diffdisk")
	if err := os.MkdirAll(filepath.Dir(disk), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(disk, make([]byte, 4<<20), 0o644); err != nil {
		t.Fatal(err)
	}
	in := &Inspector{Home: dir, Run: func(_ context.Context, _ []string, name string, _ ...string) (string, error) {
		if name == "rdctl" {
			return "Filesystem 1024-blocks Used Available Capacity Mounted on\n/dev/vda1 100000 1024 0 1% /mnt/data", nil
		}
		return "", errors.New("docker down")
	}}
	it := &owner.Item{Owner: &owner.Owner{Inspect: "rancher-desktop"}, Path: dir}
	r := in.Inspect(context.Background(), it)
	if r.Trapped < 2<<20 || r.Trim == nil {
		t.Errorf("trapped = %d trim = %v lines = %+v", r.Trapped, r.Trim, r.Lines)
	}

	in.Run = func(context.Context, []string, string, ...string) (string, error) {
		return "", errors.New("not running")
	}
	r = in.Inspect(context.Background(), it)
	if r.Trapped != 0 || !strings.Contains(r.Note, "not running") {
		t.Errorf("stopped VM: %+v", r)
	}
}

func TestPodmanWithoutMachine(t *testing.T) {
	in := &Inspector{Run: func(context.Context, []string, string, ...string) (string, error) { return "[]", nil }}
	r := in.Inspect(context.Background(), &owner.Item{Owner: &owner.Owner{Inspect: "podman-machine"}, Path: t.TempDir()})
	if !strings.Contains(r.Note, "no machine") {
		t.Errorf("note = %q", r.Note)
	}
}
