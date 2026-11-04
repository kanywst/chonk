// Package owner says who put each part of the disk there, why it exists, and
// how to get the space back the way its owner expects.
package owner

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/kanywst/chonk/internal/walk"
)

// Kind groups owners in the view.
type Kind int

// Kinds, in the order the view lists them.
const (
	KindVM Kind = iota
	KindToolchain
	KindCache
	KindBuild
	KindProject
	KindApp
	KindUser
	KindTrash
	KindSystem
	KindUnclaimed
)

var kindNames = [...]string{
	KindVM:        "VMs and containers",
	KindToolchain: "Toolchains",
	KindCache:     "Caches",
	KindBuild:     "Build output",
	KindProject:   "Projects",
	KindApp:       "App data",
	KindUser:      "Your files",
	KindTrash:     "Trash",
	KindSystem:    "System",
	KindUnclaimed: "Unclaimed",
}

func (k Kind) String() string { return kindNames[k] }

// Slug is the kind's name in JSON.
func (k Kind) Slug() string {
	return [...]string{"vm", "toolchain", "cache", "build", "project", "app", "user", "trash", "system", "unclaimed"}[k]
}

// Safety is what removing something costs you.
type Safety int

// Safety levels.
const (
	Safe    Safety = iota // regenerates by itself
	Careful               // loses state you may want back
	Danger                // loses data
)

func (s Safety) String() string { return [...]string{"safe", "careful", "danger"}[s] }

// Action is one way to reclaim an item. Cmd and Dir may use {path},
// {parent} and {name}, which expand to the item's path, its parent and its
// base name.
type Action struct {
	Label  string   `json:"label"`
	Cmd    []string `json:"cmd,omitempty"`
	Dir    string   `json:"dir,omitempty"`
	Env    []string `json:"-"`               // extra environment, such as DOCKER_HOST
	Trash  bool     `json:"trash,omitempty"` // move the item to the Trash instead of running Cmd
	Safety Safety   `json:"-"`
	Needs  string   `json:"-"` // a program that must be installed
	Note   string   `json:"note,omitempty"`
	// Inside is true when the action frees space inside a VM disk, which
	// only reaches macOS after a trim.
	Inside bool `json:"-"`
}

// Expand fills in the placeholders for path.
func (a Action) Expand(path string) Action {
	r := strings.NewReplacer("{path}", path, "{parent}", parentOf(path), "{name}", baseOf(path))
	out := a
	out.Cmd = make([]string, len(a.Cmd))
	for i, c := range a.Cmd {
		out.Cmd[i] = r.Replace(c)
	}
	out.Dir = r.Replace(a.Dir)
	out.Env = make([]string, len(a.Env))
	for i, e := range a.Env {
		out.Env[i] = r.Replace(e)
	}
	return out
}

// Command is the action as one line, for display.
func (a Action) Command() string {
	if a.Trash {
		return "move to the Trash"
	}
	var b strings.Builder
	for _, e := range a.Env {
		b.WriteString(e + " ")
	}
	if a.Dir != "" {
		b.WriteString("cd " + quote(a.Dir) + " && ")
	}
	for i, c := range a.Cmd {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(quote(c))
	}
	return b.String()
}

func quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t'\"$`\\|&;<>()*?[]{}!#~") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// Owner is who put a directory there.
type Owner struct {
	ID      string
	Name    string
	Kind    Kind
	Why     string // what it is and why it exists
	Regen   string // what removing it costs
	Safety  Safety
	Actions []Action
	Inspect string // the inspector that can look inside, if any
	// Procs are substrings of the executable paths of processes that
	// belong to this owner wherever they are installed, such as a VM's
	// host agent.
	Procs []string
}

// Item is one directory claimed by an owner.
type Item struct {
	Owner   *Owner
	Path    string
	Node    *walk.Node
	Size    int64 // allocated bytes this item accounts for
	Logical int64
	Files   int64
	Touched time.Time
	// Hotspot is where most of an unclaimed item's size sits, when that is
	// deeper than Path.
	Hotspot string
	// Parts are the build outputs found inside a project.
	Parts []*Item
	// Project is the project an artifact belongs to.
	Project *Item
	// SizeUnknown is set when nothing can say how big the item is until it
	// is gone, such as APFS snapshots.
	SizeUnknown bool
}

// Reclaimable is how much of a project its build outputs take.
func (it *Item) Reclaimable() int64 {
	var n int64
	for _, p := range it.Parts {
		n += p.Size
	}
	return n
}

// Name is how the view labels the item.
func (it *Item) Name() string {
	if it.Owner.ID == "unclaimed" && it.Owner.Name == "small files" {
		return "small files under " + Tilde(it.Path)
	}
	if it.Owner.Kind == KindProject || it.Owner.Kind == KindUnclaimed || it.Owner.Kind == KindUser {
		return Tilde(it.Path)
	}
	return it.Owner.Name
}

func parentOf(p string) string { return filepath.Dir(p) }

func baseOf(p string) string { return filepath.Base(p) }

var home string

// SetHome sets the directory that ~ stands for in rules and display.
func SetHome(h string) { home = strings.TrimSuffix(h, "/") }

// Tilde shortens a path under the home directory to ~/...
func Tilde(p string) string {
	if home != "" && (p == home || strings.HasPrefix(p, home+"/")) {
		return "~" + p[len(home):]
	}
	return p
}

// Untilde expands a leading ~.
func Untilde(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		return home + p[1:]
	}
	return p
}
