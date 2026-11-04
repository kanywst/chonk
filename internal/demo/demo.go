// Package demo is a canned machine for the README recording, so the demo
// never shows a real disk.
package demo

import (
	"context"
	"path"
	"strings"
	"sync"
	"time"

	"github.com/kanywst/chonk/internal/inspect"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/procs"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/system"
	"github.com/kanywst/chonk/internal/walk"
)

// Home is the demo user's home.
const Home = "/Users/you"

const (
	mib = int64(1) << 20
	gib = int64(1) << 30
)

// leaves are directory sizes on the canned disk.
var leaves = map[string]int64{
	"Library/Application Support/rancher-desktop/lima/0":   81 * gib,
	"Library/Application Support/rancher-desktop/electron": 127 * mib,
	"Library/Application Support/Code/User":                6 * gib,
	"Library/Application Support/discord/Cache":            2500 * mib,
	"Library/Caches/ms-playwright":                         4800 * mib,
	"Library/Caches/com.spotify.client":                    2600 * mib,
	"Library/Caches/go-build":                              2100 * mib,
	"Library/Caches/Homebrew":                              900 * mib,
	"Library/Developer/Xcode/DerivedData":                  3200 * mib,
	".local/share/pnpm/store":                              4300 * mib,
	".npm/_cacache":                                        2500 * mib,
	".rustup/toolchains":                                   5900 * mib,
	".cargo/registry":                                      1900 * mib,
	"go/pkg/mod":                                           1200 * mib,
	"work/api/target/debug":                                18 * gib,
	"work/api/src":                                         300 * mib,
	"work/web/node_modules":                                1600 * mib,
	"work/web/.next/cache":                                 2100 * mib,
	"work/web/src":                                         120 * mib,
	"work/ml/.venv/lib":                                    3400 * mib,
	"work/ml/data":                                         6 * gib,
	"Downloads":                                            7 * gib,
	"Movies":                                               3 * gib,
	"old-vm-images/ubuntu":                                 12 * gib,
	".Trash":                                               1500 * mib,
}

var markers = map[string]walk.Marker{
	"work/api":      walk.MarkGit | walk.MarkCargo,
	"work/web":      walk.MarkGit | walk.MarkPackageJSON | walk.MarkNextConfig,
	"work/ml":       walk.MarkGit | walk.MarkPyproject,
	"work/ml/.venv": walk.MarkPyvenv,
}

func tree() *walk.Node {
	r := &walk.Node{Name: Home}
	day := time.Now().Add(-26 * time.Hour).Unix()
	for p, size := range leaves {
		n := r
		for part := range strings.SplitSeq(p, "/") {
			c := n.Child(part)
			if c == nil {
				c = &walk.Node{Name: part, Parent: n}
				n.Children = append(n.Children, c)
			}
			n = c
		}
		n.Own, n.Files, n.Touched = size, size/(2*mib)+1, day
	}
	for p, m := range markers {
		n := r
		for part := range strings.SplitSeq(p, "/") {
			n = n.Child(part)
		}
		n.Markers |= m
	}
	var sum func(n *walk.Node)
	sum = func(n *walk.Node) {
		n.Size, n.Logical = n.Own, n.Own
		for _, c := range n.Children {
			sum(c)
			n.Size += c.Size
			n.Logical += c.Logical
			n.Files += c.Files
			n.Touched = max(n.Touched, c.Touched)
		}
		for i := 1; i < len(n.Children); i++ {
			for j := i; j > 0 && n.Children[j].Size > n.Children[j-1].Size; j-- {
				n.Children[j], n.Children[j-1] = n.Children[j-1], n.Children[j]
			}
		}
	}
	sum(r)
	// The VM disk is sparse: 100G long, 81G on disk.
	if vm := walk.Find([]*walk.Node{r}, Home+"/Library/Application Support/rancher-desktop/lima"); vm != nil {
		vm.Logical = 100 * gib
	}
	return r
}

// Machine is the canned Backend.
type Machine struct {
	mu      sync.Mutex
	trimmed bool
	trashed map[string]bool
	free    int64
}

// New returns the demo machine.
func New() *Machine {
	owner.SetHome(Home)
	return &Machine{trashed: map[string]bool{}, free: 13 * gib}
}

// Scan pretends to walk for a moment.
func (m *Machine) Scan(ctx context.Context, prog *walk.Progress) *owner.Report {
	r := tree()
	for i := range 20 {
		select {
		case <-ctx.Done():
			return owner.Classify([]*walk.Node{r}, owner.Options{})
		case <-time.After(60 * time.Millisecond):
		}
		prog.Files.Store(int64(i) * 61_000)
		prog.Dirs.Store(int64(i) * 9_300)
		prog.Bytes.Store(r.Size / 20 * int64(i))
	}
	return owner.Classify([]*walk.Node{r}, owner.Options{})
}

// Inspect reports a Rancher Desktop VM with trapped space until it is trimmed.
func (m *Machine) Inspect(_ context.Context, it *owner.Item) inspect.Report {
	if it.Owner.Inspect != "rancher-desktop" {
		return inspect.Report{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	disk, trapped := "81.0G on disk (100.0G maximum)", 77*gib
	if m.trimmed {
		disk, trapped = "4.7G on disk (100.0G maximum)", 0
	}
	r := inspect.Report{
		Lines: []inspect.Line{
			{Label: "VM disk", Value: disk},
			{Label: "Used inside the VM", Value: "3.6G"},
			{Label: "Docker images", Value: "1.3GB in 4 (2 in use), 640MB reclaimable"},
			{Label: "Docker local volumes", Value: "2.1GB in 7 (1 in use)"},
		},
		Trapped: trapped,
		Trim:    &owner.Action{Label: "Return trapped space (fstrim)", Cmd: []string{"true"}},
	}
	if trapped > 0 {
		r.Lines = append(r.Lines[:2], append([]inspect.Line{{Label: "Trapped", Value: "77.0G freed inside the VM but still held on this Mac"}}, r.Lines[2:]...)...)
	}
	return r
}

// Reclaim pretends to run the action.
func (m *Machine) Reclaim(_ context.Context, it *owner.Item, a owner.Action) reclaim.Result {
	time.Sleep(1200 * time.Millisecond)
	m.mu.Lock()
	defer m.mu.Unlock()
	res := reclaim.Result{Path: it.Path, Owner: it.Owner.ID, Action: a.Label, Command: a.Expand(it.Path).Command(), Expected: it.Size}
	switch {
	case a.Trash:
		res.Trashed = true
		m.trashed[it.Path] = true
	case strings.Contains(a.Label, "fstrim"):
		m.trimmed = true
		res.Freed = 76*gib + 512*mib
		res.Output = "/mnt/data: 99059355648 bytes trimmed"
	default:
		res.Freed = it.Size
		m.trashed[it.Path] = true
	}
	m.free += res.Freed
	return res
}

// Measure reports what is left after a reclaim.
func (m *Machine) Measure(p string) *walk.Node {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.trashed[p] {
		return nil
	}
	if m.trimmed && strings.HasSuffix(p, "/lima") {
		return &walk.Node{Name: path.Base(p), Size: 4700 * mib, Logical: 100 * gib}
	}
	return walk.Find([]*walk.Node{tree()}, p)
}

// Volume is a nearly full disk.
func (m *Machine) Volume() system.Volume {
	m.mu.Lock()
	defer m.mu.Unlock()
	const total = 200 * gib
	return system.Volume{Total: total, Used: total - m.free, Free: m.free}
}

// Procs are a VM and a dev server.
func (m *Machine) Procs(context.Context) []procs.Proc {
	return []procs.Proc{
		{PID: 812, RSS: 42 * mib, Exe: "/Applications/Rancher Desktop.app/Contents/Resources/resources/darwin/lima/bin/limactl"},
		{PID: 830, RSS: 1700 * mib, Exe: "/System/Library/Frameworks/Virtualization.framework/Versions/A/XPCServices/com.apple.Virtualization.VirtualMachine.xpc/Contents/MacOS/com.apple.Virtualization.VirtualMachine"},
		{PID: 4410, RSS: 610 * mib, Exe: "/opt/homebrew/Cellar/node/24.1.0/bin/node", Cwd: Home + "/work/web"},
	}
}
