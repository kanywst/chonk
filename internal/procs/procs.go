// Package procs lists running processes and ties them to the owners of
// disk space: the VM whose disk is 80G is also the process holding 4G of
// memory, and the project whose build output is 10G may have a dev server
// running in it.
package procs

import (
	"bufio"
	"bytes"
	"context"
	"os/exec"
	"slices"
	"strconv"
	"strings"

	"github.com/kanywst/chonk/internal/owner"
)

// Proc is one running process.
type Proc struct {
	PID int    `json:"pid"`
	RSS int64  `json:"rss_bytes"`
	Exe string `json:"exe"`
	Cwd string `json:"cwd,omitempty"`
}

// Name is the executable's base name.
func (p Proc) Name() string {
	if strings.Contains(p.Exe, vzService) {
		return "VM guest (Virtualization.framework)"
	}
	if i := strings.LastIndexByte(p.Exe, '/'); i >= 0 {
		return p.Exe[i+1:]
	}
	return p.Exe
}

// Runner runs a command and returns its output. Tests replace it.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Exec runs programs from the system directories only, never through PATH.
func Exec(ctx context.Context, name string, args ...string) ([]byte, error) {
	for _, dir := range []string{"/bin", "/usr/bin", "/usr/sbin", "/sbin"} {
		p := dir + "/" + name
		if _, err := exec.LookPath(p); err == nil {
			return exec.CommandContext(ctx, p, args...).Output()
		}
	}
	return nil, exec.ErrNotFound
}

// List returns every process with its resident memory, executable and
// working directory. Working directories come from one lsof call; a
// process lsof cannot see keeps an empty Cwd.
func List(ctx context.Context, run Runner) ([]Proc, error) {
	out, err := run(ctx, "ps", "-axww", "-o", "pid=,rss=,comm=")
	if err != nil {
		return nil, err
	}
	procs := parsePS(out)
	if cwd, err := run(ctx, "lsof", "-nP", "-a", "-d", "cwd", "-F", "pn"); err == nil || len(cwd) > 0 {
		dirs := parseLsof(cwd)
		for i := range procs {
			procs[i].Cwd = dirs[procs[i].PID]
		}
	}
	return procs, nil
}

func parsePS(out []byte) []Proc {
	var procs []Proc
	sc := bufio.NewScanner(bytes.NewReader(out))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) < 3 {
			continue
		}
		pid, err1 := strconv.Atoi(f[0])
		rss, err2 := strconv.ParseInt(f[1], 10, 64)
		if err1 != nil || err2 != nil {
			continue
		}
		// comm is the executable path and may contain spaces.
		line := strings.TrimSpace(sc.Text())
		exe := strings.TrimSpace(line[strings.Index(line, f[1])+len(f[1]):])
		procs = append(procs, Proc{PID: pid, RSS: rss * 1024, Exe: exe})
	}
	return procs
}

// parseLsof reads `lsof -F pn` output: p<pid> lines, then n<path> lines.
func parseLsof(out []byte) map[int]string {
	dirs := map[int]string{}
	pid := 0
	for line := range strings.SplitSeq(string(out), "\n") {
		if line == "" {
			continue
		}
		switch line[0] {
		case 'p':
			pid, _ = strconv.Atoi(line[1:])
		case 'n':
			if pid != 0 {
				dirs[pid] = line[1:]
			}
		}
	}
	return dirs
}

// Match ties processes to items. A process belongs to the item whose owner
// names it (a VM's helper processes), else to the deepest item its
// executable is under, else to the deepest item its working directory is
// under (outside the home's top level, which every shell starts in).
// A build output's processes go to its project.
func Match(items []*owner.Item, procs []Proc, home string) map[*owner.Item][]Proc {
	out := map[*owner.Item][]Proc{}
	owners := map[int]*owner.Item{}
	var vms []Proc
	for _, p := range procs {
		if strings.Contains(p.Exe, vzService) {
			vms = append(vms, p)
			continue
		}
		if it := match(items, p, home); it != nil {
			out[it] = append(out[it], p)
			owners[p.PID] = it
		}
	}
	// A Virtualization.framework guest runs in an XPC service whose parent
	// is launchd, so nothing in the process table says whose VM it is. The
	// host agent starts it, so give it to the VM owner of the newest
	// process started before it.
	for _, vm := range vms {
		best := 0
		for pid, it := range owners {
			if it.Owner.Kind == owner.KindVM && pid < vm.PID && pid > best {
				best = pid
			}
		}
		if best != 0 {
			it := owners[best]
			out[it] = append(out[it], vm)
		}
	}
	for it := range out {
		slices.SortFunc(out[it], func(a, b Proc) int { return int(b.RSS - a.RSS) })
	}
	return out
}

func match(items []*owner.Item, p Proc, home string) *owner.Item {
	for _, it := range items {
		for _, pat := range it.Owner.Procs {
			if strings.Contains(p.Exe, pat) {
				return it
			}
		}
	}
	if it := deepest(items, p.Exe); it != nil {
		return it
	}
	if p.Cwd == "" || p.Cwd == "/" || p.Cwd == home {
		return nil
	}
	return deepest(items, p.Cwd)
}

func deepest(items []*owner.Item, path string) *owner.Item {
	var best *owner.Item
	for _, it := range items {
		if it.Owner.Kind == owner.KindUnclaimed && it.Node == nil {
			continue // a summed bucket has no single place
		}
		if within(path, it.Path) && (best == nil || len(it.Path) > len(best.Path)) {
			best = it
		}
	}
	return best
}

func within(path, dir string) bool {
	return path == dir || strings.HasPrefix(path, strings.TrimSuffix(dir, "/")+"/")
}

// vzService is the process a Virtualization.framework VM runs in.
const vzService = "com.apple.Virtualization.VirtualMachine"

// Total sums resident memory.
func Total(ps []Proc) int64 {
	var n int64
	for _, p := range ps {
		n += p.RSS
	}
	return n
}
