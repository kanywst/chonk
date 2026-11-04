// Package inspect looks inside the opaque things on a disk (VM disk images,
// container stores, the Nix store) by asking the tool that owns them.
package inspect

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/kanywst/chonk/internal/format"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/walk"
)

// Line is one fact in a report.
type Line struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Report is what an inspector found.
type Report struct {
	Lines []Line `json:"lines"`
	// Trapped is space the host gives a VM disk that the guest no longer
	// uses: freed inside, never handed back. 0 when unknown or none.
	Trapped int64  `json:"trapped_bytes,omitempty"`
	Note    string `json:"note,omitempty"`
	// Trim returns trapped space to the host, when the VM supports it.
	Trim *owner.Action `json:"-"`
}

func (r *Report) add(label, f string, args ...any) {
	r.Lines = append(r.Lines, Line{label, fmt.Sprintf(f, args...)})
}

// Has reports whether chonk can look inside items of this owner.
func Has(id string) bool {
	_, ok := inspectors[id]
	return ok
}

type inspector func(ctx context.Context, in *Inspector, it *owner.Item) Report

var inspectors = map[string]inspector{
	"rancher-desktop": rancher,
	"docker-desktop":  dockerDesktop,
	"podman-machine":  podman,
	"nix":             nix,
}

// Inspector runs commands for inspectors. Tests replace Run.
type Inspector struct {
	Home string
	Run  func(ctx context.Context, env []string, name string, args ...string) (string, error)
}

// New returns an Inspector that runs real commands.
func New(home string) *Inspector {
	return &Inspector{Home: home, Run: func(ctx context.Context, env []string, name string, args ...string) (string, error) {
		p, err := reclaim.LookPath(home, name)
		if err != nil {
			return "", err
		}
		cmd := exec.CommandContext(ctx, p, args...)
		cmd.Env = env
		out, err := cmd.Output()
		return strings.TrimSpace(string(out)), err
	}}
}

// Inspect looks inside it. Items without an inspector get the generic
// report: how sparse they are.
func (in *Inspector) Inspect(ctx context.Context, it *owner.Item) Report {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	var r Report
	if f, ok := inspectors[it.Owner.Inspect]; ok {
		r = f(ctx, in, it)
	}
	if it.Logical > it.Size*3/2 && it.Size > 0 {
		r.add("Sparse", "%s of file lengths take %s on disk; tools that add up lengths overstate this", format.Bytes(it.Logical), format.Bytes(it.Size))
	}
	return r
}

func (in *Inspector) run(ctx context.Context, extra []string, name string, args ...string) (string, error) {
	return in.Run(ctx, reclaim.Env(in.Home, extra), name, args...)
}

// guestUsed parses `df -kP <mount>` and returns the used bytes.
func guestUsed(out string) (int64, bool) {
	lines := strings.Split(strings.TrimSpace(out), "\n")
	if len(lines) < 2 {
		return 0, false
	}
	f := strings.Fields(lines[len(lines)-1])
	if len(f) < 4 {
		return 0, false
	}
	used, err := strconv.ParseInt(f[2], 10, 64)
	if err != nil {
		return 0, false
	}
	return used * 1024, true
}

// dockerDF summarizes `docker system df --format '{{json .}}'`.
func dockerDF(out string) []Line {
	var lines []Line
	for l := range strings.SplitSeq(out, "\n") {
		var row struct{ Type, Size, Reclaimable, TotalCount, Active string }
		if json.Unmarshal([]byte(l), &row) != nil || row.Type == "" {
			continue
		}
		v := row.Size
		if row.TotalCount != "" {
			v += fmt.Sprintf(" in %s (%s in use)", row.TotalCount, row.Active)
		}
		if row.Reclaimable != "" && !strings.HasPrefix(row.Reclaimable, "0B") {
			v += ", " + row.Reclaimable + " reclaimable"
		}
		lines = append(lines, Line{"Docker " + strings.ToLower(row.Type), v})
	}
	return lines
}

func rancher(ctx context.Context, in *Inspector, it *owner.Item) Report {
	var r Report
	disk := filepath.Join(it.Path, "0", "diffdisk")
	alloc, logical, err := walk.Allocated(disk)
	if err != nil {
		r.Note = "No VM disk found at " + owner.Tilde(disk) + "."
		return r
	}
	r.add("VM disk", "%s on disk (%s maximum), %s", format.Bytes(alloc), format.Bytes(logical), owner.Tilde(disk))
	out, err := in.run(ctx, nil, "rdctl", "shell", "--", "df", "-kP", "/mnt/data")
	used, ok := guestUsed(out)
	if err != nil || !ok {
		r.Note = "The VM is not running, so chonk cannot see inside it. Start Rancher Desktop to see what is in use and how much is trapped."
		return r
	}
	r.add("Used inside the VM", "%s", format.Bytes(used))
	if alloc > used {
		r.Trapped = alloc - used
		r.add("Trapped", "%s freed inside the VM but still held on this Mac", format.Bytes(r.Trapped))
	}
	r.Trim = &owner.Action{Label: "Return trapped space (fstrim)", Cmd: []string{"rdctl", "shell", "--", "sudo", "fstrim", "-v", "/mnt/data"}, Safety: owner.Safe, Needs: "rdctl"}
	if df, err := in.run(ctx, []string{"DOCKER_HOST=unix://~/.rd/docker.sock"}, "docker", "system", "df", "--format", "{{json .}}"); err == nil {
		r.Lines = append(r.Lines, dockerDF(df)...)
	}
	return r
}

func dockerDesktop(ctx context.Context, in *Inspector, it *owner.Item) Report {
	var r Report
	disk := filepath.Join(it.Path, "Data", "vms", "0", "data", "Docker.raw")
	if alloc, logical, err := walk.Allocated(disk); err == nil {
		r.add("VM disk", "%s on disk (%s maximum)", format.Bytes(alloc), format.Bytes(logical))
	}
	if df, err := in.run(ctx, nil, "docker", "system", "df", "--format", "{{json .}}"); err == nil {
		r.Lines = append(r.Lines, dockerDF(df)...)
	} else {
		r.Note = "Docker is not running, so chonk cannot see inside the VM."
	}
	return r
}

func podman(ctx context.Context, in *Inspector, it *owner.Item) Report {
	var r Report
	images, _ := filepath.Glob(filepath.Join(it.Path, "*", "*.raw"))
	more, _ := filepath.Glob(filepath.Join(it.Path, "*", "*.qcow2"))
	for _, img := range append(images, more...) {
		if alloc, logical, err := walk.Allocated(img); err == nil {
			r.add("Disk image", "%s on disk (%s maximum), %s", format.Bytes(alloc), format.Bytes(logical), filepath.Base(img))
		}
	}
	out, err := in.run(ctx, nil, "podman", "machine", "list", "--format", "json")
	if err != nil {
		r.Note = "podman is not installed or not working, so chonk cannot ask about the machine."
		return r
	}
	var machines []struct {
		Name    string
		Running bool
		LastUp  string
	}
	_ = json.Unmarshal([]byte(out), &machines)
	if len(machines) == 0 {
		r.Note = "Podman has no machine any more. What is left here is leftover files; moving them to the Trash is safe."
		return r
	}
	for _, m := range machines {
		state := "stopped"
		if m.Running {
			state = "running"
		}
		r.add("Machine", "%s, %s, last up %s", m.Name, state, m.LastUp)
		if !m.Running {
			r.Note = "The machine is stopped, so prune cannot run. Start it with `podman machine start`, or remove the machine if you do not use it."
		}
	}
	return r
}

func nix(_ context.Context, _ *Inspector, _ *owner.Item) Report {
	var r Report
	gens := func(glob string) int {
		m, _ := filepath.Glob(glob)
		return len(m)
	}
	if n := gens("/nix/var/nix/profiles/system-*-link"); n > 0 {
		r.add("System generations", "%d (each keeps its packages alive)", n)
	}
	home, _ := os.UserHomeDir()
	if n := gens(filepath.Join(home, ".local", "state", "nix", "profiles", "profile-*-link")) + gens("/nix/var/nix/profiles/per-user/*/profile-*-link"); n > 0 {
		r.add("Profile generations", "%d", n)
	}
	r.Note = "The store is shared by every generation. `nix-store --gc` removes only what no generation uses; deleting old generations first frees more."
	return r
}
