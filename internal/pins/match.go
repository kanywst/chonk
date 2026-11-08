package pins

import (
	"slices"
	"strconv"
	"strings"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/walk"
)

// manager is a version manager chonk can read a pin story for: where it
// keeps one directory per installed version, and how to remove one.
type manager struct {
	ownerID string   // the owner ID the rules give its directory
	tool    string   // the tool name pins use
	holds   []string // path from the owner's directory to the versions
	remove  []string // command that removes one version, or nil for none
	env     []string // the root the command must act on, not the shell's
	hint    string   // how to remove one, for the regen line
}

var managers = []manager{
	{
		// The command acts on whatever RUSTUP_HOME the shell exports, which
		// need not be the tree this version was found in, so it is pinned
		// to the directory chonk measured.
		ownerID: "rustup", tool: "rust", holds: []string{"toolchains"},
		remove: []string{"rustup", "toolchain", "uninstall", "{name}"},
		env:    []string{"RUSTUP_HOME={grandparent}"},
		hint:   "`rustup toolchain uninstall` removes one",
	},
	{
		// nvm is a shell function, not a program chonk can run, so it only
		// says how to remove a version.
		ownerID: "nvm", tool: "node", holds: []string{"versions", "node"},
		hint: "`nvm uninstall <version>` removes one",
	},
	{
		ownerID: "pyenv", tool: "python", holds: []string{"versions"},
		remove: []string{"pyenv", "uninstall", "-f", "{name}"},
		env:    []string{"PYENV_ROOT={grandparent}"},
		hint:   "`pyenv uninstall` removes one",
	},
}

// unusedOwner is an installed version no project on this disk pins. It is
// careful, not safe: a pin file is not the only way a version gets used, so
// chonk states the fact it checked and leaves the removal to you.
func unusedOwner(m manager) owner.Owner {
	o := owner.Owner{
		ID: m.ownerID + "-unpinned", Kind: owner.KindToolchain, Safety: owner.Careful,
		Why: "An installed version that no pin file chonk read asks for, and not this manager's default either.",
		Regen: "chonk reads the pin files a few levels under your home; a shell, a script or a project it did not " +
			"reach could still want this version. " + m.hint + ".",
	}
	if m.remove != nil {
		o.Actions = append(o.Actions, owner.Action{
			Label: "Uninstall this version", Cmd: m.remove, Env: m.env,
			Safety: owner.Careful, Needs: m.remove[0],
		})
	}
	return o
}

// Apply points each version manager's item at the versions nothing pins.
func Apply(rep *owner.Report, s Set) {
	for _, it := range rep.Items {
		for _, m := range managers {
			if it.Owner.ID != m.ownerID {
				continue
			}
			apply(it, m, s)
		}
	}
}

func apply(it *owner.Item, m manager, s Set) {
	if it.Node == nil || len(it.Parts) > 0 {
		return
	}
	// A pin chonk cannot read as a version (an alias like lts/*, or
	// "system") could mean any installed version, so the whole tool is left
	// alone rather than risk calling a version unused. The manager's own
	// default gets the same treatment: nvm's default is often the alias
	// "node", which stands for whichever version is newest, and calling
	// that one unpinned would point at the version in use.
	pins := s.Wanted[m.tool]
	var def []string
	for _, d := range s.Default[m.tool] {
		def = append(def, version(m.tool, d))
	}
	for _, p := range slices.Concat(pins, def) {
		if !isVersion(p) {
			return
		}
	}
	dir := descend(it.Node, m.holds)
	if dir == nil {
		return
	}
	o := unusedOwner(m)
	for _, ch := range dir.Children {
		// The name becomes an argument to the uninstall command, and it
		// came off the disk: one shaped like a flag, or like anything but
		// a version, is left alone rather than passed along. A pyenv
		// virtualenv living beside the versions lands here too.
		if !installedName(ch.Name) {
			continue
		}
		v := version(m.tool, ch.Name)
		if v == "" || matchesAny(v, pins) || matchesAny(v, def) {
			continue
		}
		part := &owner.Item{
			Owner: &o, Path: ch.Path(), Node: ch, Size: ch.Size, Logical: ch.Logical, Files: ch.Files,
			// The TUI moves a reclaimed part's bytes out of its parent
			// through this, and drops the row once it is gone.
			Project: it,
		}
		it.Parts = append(it.Parts, part)
	}
	if len(it.Parts) == 0 {
		return
	}
	it.PartsLabel = "no project pins"
	// Retell the item itself, so the count is visible without opening it.
	c := *it.Owner
	c.Regen = c.Regen + " No pin file under your home names " + plural(len(it.Parts)) + "."
	it.Owner = &c
}

// plural names how many installed versions nothing asks for.
func plural(n int) string {
	if n == 1 {
		return "1 of the installed versions"
	}
	return strconv.Itoa(n) + " of the installed versions"
}

// descend follows a path of child names under n.
func descend(n *walk.Node, names []string) *walk.Node {
	for _, want := range names {
		var next *walk.Node
		for _, ch := range n.Children {
			if ch.Name == want {
				next = ch
				break
			}
		}
		if next == nil {
			return nil
		}
		n = next
	}
	return n
}

// version reduces a directory name to the part a pin names. A rustup
// toolchain carries its target triple ("1.90.0-aarch64-apple-darwin"), and
// an nvm version its v prefix ("v22.16.0").
func version(tool, name string) string {
	if name == "" {
		return ""
	}
	switch tool {
	case "rust":
		head, _, _ := strings.Cut(name, "-")
		return head
	case "node":
		return strings.TrimPrefix(name, "v")
	}
	return name
}

// isVersion reports whether a pin is a version chonk can compare, rather
// than an alias whose meaning only its manager knows.
func isVersion(pin string) bool {
	if pin == "" {
		return false
	}
	switch pin {
	case "stable", "beta", "nightly":
		return true
	}
	if strings.ContainsAny(pin, "/*<>= ") {
		return false
	}
	// A bare channel word ("node", "lts", "latest", "system") is an alias.
	return pin[0] >= '0' && pin[0] <= '9'
}

// installedName reports whether a directory beside a manager's versions
// looks like one: a plain version, never something git or a shell would
// read as a flag.
func installedName(name string) bool {
	if name == "" || strings.HasPrefix(name, "-") || strings.HasPrefix(name, ".") {
		return false
	}
	for _, r := range name {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case r == '.', r == '_', r == '-', r == '+':
		default:
			return false
		}
	}
	return true
}

// matches reports whether an installed version answers a pin: the same
// version, or a more precise one under it, so the pin "22" covers 22.16.0.
func matches(v, pin string) bool {
	if v == "" || pin == "" {
		return false
	}
	return v == pin || strings.HasPrefix(v, pin+".")
}

func matchesAny(v string, pins []string) bool {
	for _, p := range pins {
		if matches(v, p) {
			return true
		}
	}
	return false
}
