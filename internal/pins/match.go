package pins

import (
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
	hint    string   // how to remove one, for the regen line
}

var managers = []manager{
	{
		ownerID: "rustup", tool: "rust", holds: []string{"toolchains"},
		remove: []string{"rustup", "toolchain", "uninstall", "{name}"},
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
		hint:   "`pyenv uninstall` removes one",
	},
}

// unusedOwner is an installed version no project on this disk pins. It is
// careful, not safe: a pin file is not the only way a version gets used, so
// chonk states the fact it checked and leaves the removal to you.
func unusedOwner(m manager) owner.Owner {
	o := owner.Owner{
		ID: m.ownerID + "-unpinned", Kind: owner.KindToolchain, Safety: owner.Careful,
		Why:   "An installed version no project on this disk pins, and not the default either.",
		Regen: "Nothing here asks for it, though a shell or a script outside these projects still might. " + m.hint + ".",
	}
	if m.remove != nil {
		o.Actions = append(o.Actions, owner.Action{
			Label: "Uninstall this version", Cmd: m.remove, Safety: owner.Careful, Needs: m.remove[0],
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
	def := version(m.tool, s.Default[m.tool])
	if def != "" && !isVersion(def) {
		return
	}
	for _, p := range pins {
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
		v := version(m.tool, ch.Name)
		if v == "" || matchesAny(v, pins) || matches(v, def) {
			continue
		}
		part := &owner.Item{
			Owner: &o, Path: ch.Path(), Node: ch, Size: ch.Size, Logical: ch.Logical, Files: ch.Files,
		}
		it.Parts = append(it.Parts, part)
	}
	if len(it.Parts) == 0 {
		return
	}
	it.PartsLabel = "no project pins"
	// Retell the item itself, so the count is visible without opening it.
	c := *it.Owner
	c.Regen = c.Regen + " No project on this disk pins " + plural(len(it.Parts)) + "."
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
