// Package pins reads the tool versions projects ask for, so chonk can point
// at the installed versions nothing on this disk asks for any more.
package pins

import (
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Set is every version of a tool that a pin file asks for, plus the versions
// its manager is set to use by default. A version in neither is one nothing
// chonk found asks for.
type Set struct {
	// Wanted maps a tool ("node", "rust", "python") to the pins found.
	Wanted map[string][]string
	// Default maps a tool to the versions its manager would pick with no
	// project asking, which are never reported as unused. It is a list
	// because `pyenv global 3.12 3.11` makes several versions the default
	// at once, one per line.
	Default map[string][]string
}

func newSet() Set {
	return Set{Wanted: map[string][]string{}, Default: map[string][]string{}}
}

func (s Set) add(tool, v string) { addTo(s.Wanted, tool, v) }

func (s Set) addDefault(tool, v string) { addTo(s.Default, tool, v) }

func addTo(m map[string][]string, tool, v string) {
	v = strings.TrimSpace(v)
	if v == "" || slices.Contains(m[tool], v) {
		return
	}
	m[tool] = append(m[tool], v)
}

// pinFiles are the files a project states a tool version in, with the tool
// each one speaks for and how to read a version out of it.
var pinFiles = []struct {
	name string
	tool string
	read func(string) []string
}{
	{".nvmrc", "node", lines},
	{".node-version", "node", lines},
	{"rust-toolchain", "rust", lines},
	{"rust-toolchain.toml", "rust", channel},
	{".python-version", "python", lines},
	{".go-version", "go", lines},
	{".ruby-version", "ruby", lines},
}

// Collect reads the pins in every directory given, and what each version
// manager is set to use by default. Unreadable files are skipped: a pin
// chonk cannot read keeps its version out of the unused list, never the
// other way round.
func Collect(home string, dirs []string) Set {
	s := newSet()
	for _, dir := range dirs {
		for _, pf := range pinFiles {
			b, err := os.ReadFile(filepath.Join(dir, pf.name))
			if err != nil {
				continue
			}
			for _, v := range pf.read(string(b)) {
				s.add(pf.tool, v)
			}
		}
		for tool, vs := range toolVersions(filepath.Join(dir, ".tool-versions")) {
			for _, v := range vs {
				s.add(tool, v)
			}
		}
	}
	s.defaults(home)
	return s
}

// defaults asks each version manager what it uses when no project says.
func (s Set) defaults(home string) {
	for _, v := range contentLines(read(filepath.Join(home, ".nvm/alias/default"))) {
		s.addDefault("node", v)
	}
	// `pyenv global` can set several versions at once, one per line, and
	// every one of them is in use.
	for _, v := range contentLines(read(filepath.Join(home, ".pyenv/version"))) {
		s.addDefault("python", v)
	}
	// rustup keeps its default in settings.toml as default_toolchain, and
	// the toolchain pinned for a particular directory under [overrides].
	// An override is a pin in the strict sense, so it counts as wanted.
	if b := read(filepath.Join(home, ".rustup/settings.toml")); b != "" {
		overrides := false
		for line := range strings.SplitSeq(b, "\n") {
			line = strings.TrimSpace(line)
			if strings.HasPrefix(line, "[") {
				overrides = line == "[overrides]"
				continue
			}
			k, v, ok := strings.Cut(line, "=")
			if !ok {
				continue
			}
			v = strings.Trim(strings.TrimSpace(v), `"'`)
			switch {
			case strings.TrimSpace(k) == "default_toolchain":
				s.addDefault("rust", v)
			case overrides:
				s.add("rust", v)
			}
		}
	}
}

// lines reads a pin file as versions, one per line. Most hold exactly one,
// but .python-version can list several and all of them are in use.
func lines(s string) []string { return contentLines(s) }

// channel reads the channel out of a rust-toolchain.toml.
func channel(s string) []string {
	for line := range strings.SplitSeq(s, "\n") {
		if k, v, ok := strings.Cut(line, "="); ok && strings.TrimSpace(k) == "channel" {
			return []string{strings.Trim(strings.TrimSpace(v), `"'`)}
		}
	}
	return nil
}

// toolVersions reads an asdf or mise .tool-versions: one "tool version" per
// line.
func toolVersions(path string) map[string][]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	out := map[string][]string{}
	for line := range strings.SplitSeq(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 2 {
			continue
		}
		tool := f[0]
		if tool == "nodejs" {
			tool = "node"
		}
		out[tool] = append(out[tool], f[1:]...)
	}
	return out
}

func read(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

// contentLines are the lines that say something: blank ones and comments
// carry no version.
func contentLines(s string) []string {
	var out []string
	for line := range strings.SplitSeq(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" && !strings.HasPrefix(line, "#") {
			out = append(out, line)
		}
	}
	return out
}

// Discover finds the directories under home that state a tool version,
// without a full walk of the disk: `chonk why` needs the same pins as a
// scan but reads one directory, so it cannot be handed a project list.
// It descends a few levels and leaves out the places projects do not live.
func Discover(home string) []string {
	// The home directory itself: a .tool-versions or .nvmrc there is the
	// usual global pin, and missing it would call the version it names
	// unpinned.
	out := []string{home}
	var walk func(dir string, depth int)
	walk = func(dir string, depth int) {
		ents, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range ents {
			if !e.IsDir() {
				if pinned(e.Name()) {
					out = append(out, dir)
				}
				continue
			}
			if depth < 4 && !skipDir(e.Name()) {
				walk(filepath.Join(dir, e.Name()), depth+1)
			}
		}
	}
	walk(home, 0)
	return slices.Compact(out)
}

func pinned(name string) bool {
	if name == ".tool-versions" {
		return true
	}
	for _, pf := range pinFiles {
		if pf.name == name {
			return true
		}
	}
	return false
}

// skipDir leaves out the trees that hold no projects but plenty of files:
// dependency and build directories, and the Library the toolchains live in.
func skipDir(name string) bool {
	switch name {
	case "Library", "node_modules", "vendor", "target", ".git", ".venv", "venv",
		"Applications", "Music", "Movies", "Pictures", "dist", "build":
		return true
	}
	// Dotted directories hold tool state, not projects, with the version
	// managers themselves among them.
	return strings.HasPrefix(name, ".")
}
