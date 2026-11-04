package owner

import (
	"path/filepath"
	"slices"
	"strings"

	"github.com/kanywst/chonk/internal/walk"
)

// cand is a directory a rule is asked about. Rel is the path with the home
// directory written as ~.
type cand struct {
	Path string
	Rel  string
	Node *walk.Node
}

func (c cand) parentRel() string { return filepath.Dir(c.Rel) }

// rule claims either one path (at) or every direct child of a path (each).
type rule struct {
	path  string
	each  bool
	owner func(name string) Owner
	skip  func(name string) bool // children an each rule leaves alone
}

func (r rule) match(c cand) *Owner {
	if r.each && r.skip != nil && r.skip(c.Node.Name) {
		return nil
	}
	if r.each && c.parentRel() == r.path || !r.each && c.Rel == r.path {
		o := r.owner(c.Node.Name)
		return &o
	}
	return nil
}

// at claims exactly one path.
func at(rel string, o Owner) rule {
	return rule{path: rel, owner: func(string) Owner { return o }}
}

// each claims every direct child of a path, naming the owner after it.
// Children for which an optional skip returns true are left to later rules.
func each(parent string, f func(name string) Owner, skip ...func(string) bool) rule {
	r := rule{path: parent, each: true, owner: f}
	if len(skip) > 0 {
		r.skip = skip[0]
	}
	return r
}

func withEnv(a Action, env ...string) Action {
	a.Env = env
	return a
}

// trash moves the item to the Trash.
func trash(s Safety) Action {
	return Action{Label: "Move to the Trash", Trash: true, Safety: s}
}

func run(label string, s Safety, cmd ...string) Action {
	return Action{Label: label, Cmd: cmd, Safety: s, Needs: cmd[0]}
}

// cache is an app or tool cache that the app rebuilds by itself.
func cache(id, name, regen string, acts ...Action) Owner {
	return Owner{
		ID: id, Name: name, Kind: KindCache, Safety: Safe,
		Why:     name + " keeps downloaded or computed data here so it does not have to fetch or rebuild it again.",
		Regen:   regen,
		Actions: append(acts, trash(Safe)),
	}
}

// rdDocker points docker at Rancher Desktop's engine, whatever the current
// docker context is.
var rdDocker = []string{"DOCKER_HOST=unix://~/.rd/docker.sock"}

// DockerDesktopEnv points docker at Docker Desktop's engine.
var DockerDesktopEnv = []string{"DOCKER_HOST=unix://~/.docker/run/docker.sock"}

var trimNote = "Space freed inside a VM only reaches macOS after a trim; chonk runs one afterwards when the VM supports it."

// rules are checked in order; the first that returns an owner claims the
// directory and everything below it.
func rules() []rule {
	return []rule{
		// VMs and container engines. These are the largest single items on a
		// developer Mac, and the ones a directory listing explains worst.
		at("~/Library/Application Support/rancher-desktop/lima", Owner{
			ID: "rancher-desktop", Name: "Rancher Desktop VM", Kind: KindVM, Safety: Careful, Inspect: "rancher-desktop",
			Procs: []string{"Rancher Desktop.app/Contents/Resources/resources/darwin/lima/", "rancher-desktop/lima/"},
			Why:   "Rancher Desktop runs Docker and Kubernetes in a Lima Linux VM. Its disk (diffdisk) holds every image, container, volume and cluster you have pulled or built. It grows as you use it and does not shrink by itself when you delete things inside.",
			Regen: "Images are pulled again on next use; volumes and cluster state are gone for good.",
			Actions: []Action{
				{Label: "Return space the VM already freed (fstrim)", Cmd: []string{"rdctl", "shell", "--", "sudo", "fstrim", "-v", "/mnt/data"}, Safety: Safe, Needs: "rdctl", Lossless: true, Note: "Loses nothing. Only hands back blocks the guest no longer uses."},
				{Label: "Remove unused images, stopped containers and build cache", Cmd: []string{"docker", "system", "prune", "-a", "-f"}, Env: rdDocker, Safety: Careful, Needs: "docker", Inside: true, Note: trimNote},
				{Label: "Also remove unused volumes", Cmd: []string{"docker", "system", "prune", "-a", "-f", "--volumes"}, Env: rdDocker, Safety: Danger, Needs: "docker", Inside: true, Note: trimNote},
				{Label: "Factory reset Rancher Desktop", Cmd: []string{"rdctl", "factory-reset"}, Safety: Danger, Needs: "rdctl", Note: "Deletes the VM, every image, volume and cluster, and Rancher Desktop's settings."},
			},
		}),
		at("~/Library/Containers/com.docker.docker", Owner{
			ID: "docker-desktop", Name: "Docker Desktop VM", Kind: KindVM, Safety: Careful, Inspect: "docker-desktop",
			Procs: []string{"com.docker.virtualization", "com.docker.backend", "com.docker.krun"},
			Why:   "Docker Desktop keeps its Linux VM disk (Docker.raw) here. It holds every image, container, volume and build cache. It is sparse, so its length overstates what it takes on disk.",
			Regen: "Images are pulled again on next use; volumes are gone for good.",
			Actions: []Action{
				{Label: "Remove unused images, stopped containers and build cache", Cmd: []string{"docker", "system", "prune", "-a", "-f"}, Env: DockerDesktopEnv, Safety: Careful, Needs: "docker"},
				{Label: "Also remove unused volumes", Cmd: []string{"docker", "system", "prune", "-a", "-f", "--volumes"}, Env: DockerDesktopEnv, Safety: Danger, Needs: "docker"},
			},
		}),
		at("~/.local/share/containers/podman/machine", Owner{
			ID: "podman-machine", Name: "Podman machine", Kind: KindVM, Safety: Careful, Inspect: "podman-machine",
			Procs: []string{"/gvproxy", "/vfkit", "/krunkit"},
			Why:   "On macOS Podman runs containers in a Linux VM (podman machine). Its raw disk image holds every image, container and volume. It is sparse: the file length is the disk's maximum size, not what it takes.",
			Regen: "Removing the machine deletes all its images, containers and volumes; `podman machine init` makes a fresh one.",
			Actions: []Action{
				{Label: "Remove unused images, containers and volumes (machine must be running)", Cmd: []string{"podman", "system", "prune", "-a", "-f", "--volumes"}, Safety: Danger, Needs: "podman", Inside: true, Note: trimNote},
				{Label: "Remove the machine", Cmd: []string{"podman", "machine", "rm", "-f"}, Safety: Danger, Needs: "podman", Note: "Deletes the VM and everything in it."},
			},
		}),
		at("~/.local/share/containers/storage", Owner{
			ID: "podman-storage", Name: "Podman storage", Kind: KindVM, Safety: Careful,
			Why:     "Rootless Podman keeps image layers and container filesystems here.",
			Regen:   "Images are pulled again on next use; volumes are gone for good.",
			Actions: []Action{run("Remove unused images, containers and volumes", Danger, "podman", "system", "prune", "-a", "-f", "--volumes")},
		}),
		at("~/.orbstack", Owner{
			ID: "orbstack", Name: "OrbStack", Kind: KindVM, Safety: Careful,
			Why:     "OrbStack's Linux VM, containers and machines.",
			Regen:   "Images are pulled again; machines and volumes are gone for good.",
			Actions: []Action{withEnv(run("Remove unused images, stopped containers and build cache", Careful, "docker", "system", "prune", "-a", "-f"), "DOCKER_HOST=unix://~/.orbstack/run/docker.sock")},
		}),
		each("~/.lima", func(name string) Owner {
			return Owner{
				ID: "lima:" + name, Name: "Lima VM " + name, Kind: KindVM, Safety: Danger,
				Why:     "A Lima Linux VM. Its diffdisk holds the guest's whole filesystem.",
				Regen:   "Deleting the VM deletes everything inside it; `limactl create` makes a new one.",
				Actions: []Action{run("Delete this VM", Danger, "limactl", "delete", "-f", "{name}")},
			}
		}, func(name string) bool { return strings.HasPrefix(name, "_") }), // _config, _networks, _disks
		at("~/.colima", Owner{
			ID: "colima", Name: "Colima VMs", Kind: KindVM, Safety: Careful,
			Why:     "Colima runs Docker in Lima VMs; their disks live here.",
			Regen:   "Images are pulled again on next use; volumes are gone for good.",
			Actions: []Action{withEnv(run("Remove unused images, stopped containers and build cache (default profile)", Careful, "docker", "system", "prune", "-a", "-f"), "DOCKER_HOST=unix://~/.colima/default/docker.sock")},
		}),
		at("~/Library/Containers/com.utmapp.UTM", Owner{
			ID: "utm", Name: "UTM VMs", Kind: KindVM, Safety: Danger,
			Why: "Virtual machines made in UTM.", Regen: "A deleted VM is gone; delete them from UTM itself.",
		}),
		at("~/Parallels", Owner{
			ID: "parallels", Name: "Parallels VMs", Kind: KindVM, Safety: Danger,
			Why: "Parallels Desktop virtual machines.", Regen: "A deleted VM is gone; remove them from Parallels' Control Center.",
		}),
		at("~/.android/avd", Owner{
			ID: "android-avd", Name: "Android emulators", Kind: KindVM, Safety: Careful,
			Why: "Android Virtual Devices made by Android Studio, each a full disk image.", Regen: "Recreated from Device Manager; app data inside them is lost.",
		}),
		at("~/Library/Developer/CoreSimulator", Owner{
			ID: "ios-simulators", Name: "iOS simulators", Kind: KindVM, Safety: Careful,
			Why:     "Xcode's simulator devices and runtimes. Old runtimes stay after Xcode updates.",
			Regen:   "Unavailable simulators are no longer usable by any installed Xcode.",
			Actions: []Action{run("Delete simulators no installed Xcode can run", Safe, "xcrun", "simctl", "delete", "unavailable")},
		}),

		// Toolchains and package managers.
		at("/nix", Owner{
			ID: "nix", Name: "Nix store", Kind: KindToolchain, Safety: Careful, Inspect: "nix",
			Procs: []string{"/nix-daemon"},
			Why:   "Every package Nix has built or downloaded, including the ones only old generations still use. Old generations are what make rollback possible, and also what keeps the store growing.",
			Regen: "Garbage collection drops the old generations, so you can no longer roll back to them.",
			Actions: []Action{
				run("Delete store paths nothing refers to", Safe, "nix-store", "--gc"),
				{Label: "Delete old generations, then collect", Cmd: []string{"sudo", "-n", "nix-collect-garbage", "-d"}, Safety: Careful, Needs: "sudo", Note: "Needs root. chonk cannot ask for a password inside the view; run `sudo -v` in this terminal first, or run the command yourself."},
			},
		}),
		at("/opt/homebrew", Owner{
			ID: "homebrew", Name: "Homebrew", Kind: KindToolchain, Safety: Careful,
			Why:   "Installed formulae and their dependencies, including dependencies nothing needs any more.",
			Regen: "Old versions and orphaned dependencies are reinstalled only if something asks for them.",
			Actions: []Action{
				run("Remove old versions and the download cache", Safe, "brew", "cleanup", "--prune=all"),
				run("Remove dependencies nothing needs", Careful, "brew", "autoremove"),
			},
		}),
		at("~/Library/Caches/Homebrew", cache("homebrew-cache", "Homebrew downloads", "Bottles are downloaded again on the next install or upgrade.", run("Clean", Safe, "brew", "cleanup", "--prune=all"))),
		at("~/.rustup", Owner{
			ID: "rustup", Name: "Rust toolchains", Kind: KindToolchain, Safety: Careful,
			Why:   "Every Rust toolchain rustup installed. Nightlies and old stable versions stay until removed.",
			Regen: "A removed toolchain is downloaded again by `rustup toolchain install`.",
		}),
		at("~/.cargo/registry", cache("cargo-registry", "Cargo registry", "Crates are downloaded again on the next build.")),
		at("~/.cargo/git", cache("cargo-git", "Cargo git checkouts", "Git dependencies are cloned again on the next build.")),
		at("~/go/pkg/mod", cache("go-mod", "Go module cache", "Modules are downloaded again on the next build.", run("Clean", Safe, "go", "clean", "-modcache"))),
		at("~/Library/Caches/go-build", cache("go-build", "Go build cache", "Packages are compiled again on the next build.", run("Clean", Safe, "go", "clean", "-cache"))),
		at("~/.cache/go-build", cache("go-build", "Go build cache", "Packages are compiled again on the next build.", run("Clean", Safe, "go", "clean", "-cache"))),
		at("~/Library/Caches/golangci-lint", cache("golangci-lint", "golangci-lint cache", "The next lint run is slower.", run("Clean", Safe, "golangci-lint", "cache", "clean"))),
		at("~/.npm", cache("npm", "npm cache", "Packages are downloaded again on the next install.", run("Clean", Safe, "npm", "cache", "clean", "--force"))),
		at("~/.local/share/pnpm", cache("pnpm", "pnpm store", "Packages no project links to are downloaded again if needed.", run("Remove packages no project uses", Safe, "pnpm", "store", "prune"))),
		at("~/Library/pnpm", cache("pnpm", "pnpm store", "Packages no project links to are downloaded again if needed.", run("Remove packages no project uses", Safe, "pnpm", "store", "prune"))),
		at("~/Library/Caches/Yarn", cache("yarn", "Yarn cache", "Packages are downloaded again on the next install.", run("Clean", Safe, "yarn", "cache", "clean"))),
		at("~/.bun/install/cache", cache("bun", "Bun cache", "Packages are downloaded again on the next install.", run("Clean", Safe, "bun", "pm", "cache", "rm"))),
		at("~/.cache/uv", cache("uv", "uv cache", "Packages are downloaded again on the next install.", run("Clean", Safe, "uv", "cache", "clean"))),
		at("~/Library/Caches/pip", cache("pip", "pip cache", "Packages are downloaded again on the next install.", run("Clean", Safe, "pip3", "cache", "purge"))),
		at("~/.gradle/caches", cache("gradle", "Gradle cache", "Dependencies are downloaded and builds rerun.")),
		at("~/.m2/repository", cache("maven", "Maven repository", "Dependencies are downloaded again on the next build.")),
		at("~/.nvm", Owner{ID: "nvm", Name: "nvm Node versions", Kind: KindToolchain, Safety: Careful, Why: "Every Node.js version nvm installed, each with its own global packages.", Regen: "A removed version is downloaded again by `nvm install`."}),
		at("~/.local/share/mise", Owner{
			ID: "mise", Name: "mise tools", Kind: KindToolchain, Safety: Careful,
			Why:     "Every tool version mise installed, including versions no config asks for any more.",
			Regen:   "Pruned versions are installed again if a config asks for them.",
			Actions: []Action{run("Remove versions no config uses", Safe, "mise", "prune", "-y")},
		}),
		at("~/.pyenv", Owner{ID: "pyenv", Name: "pyenv Pythons", Kind: KindToolchain, Safety: Careful, Why: "Every Python version pyenv built.", Regen: "A removed version is built again by `pyenv install`."}),
		at("~/.cache/huggingface", cache("huggingface", "Hugging Face models", "Models are downloaded again on next use.")),
		at("~/.ollama/models", Owner{ID: "ollama", Name: "Ollama models", Kind: KindToolchain, Safety: Careful, Procs: []string{"/ollama"}, Why: "Local LLM weights pulled with `ollama pull`.", Regen: "Pulled again with `ollama pull`; remove single models with `ollama rm`."}),
		at("~/.lmstudio", Owner{ID: "lmstudio", Name: "LM Studio models", Kind: KindToolchain, Safety: Careful, Why: "Local LLM weights downloaded in LM Studio.", Regen: "Downloaded again from LM Studio."}),
		at("~/Library/Developer/Xcode/DerivedData", Owner{
			ID: "xcode-deriveddata", Name: "Xcode DerivedData", Kind: KindBuild, Safety: Safe,
			Why: "Xcode's build products and indexes for every project you opened.", Regen: "Rebuilt on the next build; the first one is slow.",
			Actions: []Action{trash(Safe)},
		}),
		at("~/Library/Developer/Xcode/iOS DeviceSupport", Owner{
			ID: "xcode-devicesupport", Name: "Xcode device support", Kind: KindCache, Safety: Safe,
			Why: "Debug symbols copied from every iOS version of every device you connected.", Regen: "Copied again when that device connects.",
			Actions: []Action{trash(Safe)},
		}),
		at("~/Library/Developer/Xcode/Archives", Owner{ID: "xcode-archives", Name: "Xcode archives", Kind: KindBuild, Safety: Danger, Why: "App archives you built for distribution, with their debug symbols.", Regen: "Needed to symbolicate crash reports from those builds."}),

		// AI coding tools.
		at("~/.claude", Owner{ID: "claude-code", Name: "Claude Code data", Kind: KindApp, Safety: Careful, Why: "Claude Code's settings, session transcripts (projects/), file history and plugins.", Regen: "Transcripts and file history are not recoverable."}),
		at("~/.local/share/claude", Owner{ID: "claude-code-versions", Name: "Claude Code versions", Kind: KindToolchain, Safety: Safe, Why: "Claude Code's native installer keeps several downloaded versions to switch between.", Regen: "Old versions are not needed to run the current one."}),

		// Trash.
		at("~/.Trash", Owner{
			ID: "trash", Name: "Trash", Kind: KindTrash, Safety: Danger,
			Why:     "Things you moved to the Trash. They still take space until it is emptied.",
			Regen:   "Emptying the Trash deletes them for good.",
			Actions: []Action{{Label: "Empty the Trash", Cmd: []string{"osascript", "-e", `tell application "Finder" to empty trash`}, Safety: Danger, Needs: "osascript"}},
		}),

		// Your own files.
		at("~/Downloads", Owner{ID: "downloads", Name: "Downloads", Kind: KindUser, Safety: Danger, Why: "Files you downloaded. Installers and archives tend to stay long after use.", Regen: "Only you know."}),
		at("~/Desktop", Owner{ID: "desktop", Name: "Desktop", Kind: KindUser, Safety: Danger, Why: "Files on your desktop.", Regen: "Only you know."}),
		at("~/Documents", Owner{ID: "documents", Name: "Documents", Kind: KindUser, Safety: Danger, Why: "Your documents.", Regen: "Only you know."}),
		at("~/Movies", Owner{ID: "movies", Name: "Movies", Kind: KindUser, Safety: Danger, Why: "Your videos, including screen recordings.", Regen: "Only you know."}),
		at("~/Pictures", Owner{ID: "pictures", Name: "Pictures", Kind: KindUser, Safety: Danger, Why: "Your pictures and the Photos library.", Regen: "Only you know."}),
		at("~/Music", Owner{ID: "music", Name: "Music", Kind: KindUser, Safety: Danger, Why: "Your music library.", Regen: "Only you know."}),

		// System.
		at("~/Library/Metadata", Owner{ID: "spotlight", Name: "Spotlight metadata", Kind: KindSystem, Safety: Careful, Why: "Spotlight and app indexes macOS keeps for search.", Regen: "macOS rebuilds them; leave them alone."}),
		at("~/Library/Application Support/MobileSync/Backup", Owner{ID: "ios-backups", Name: "iPhone and iPad backups", Kind: KindUser, Safety: Danger, Why: "Local backups made by Finder.", Regen: "A deleted backup is gone; manage them in Finder under the device."}),
		at("~/Library/Mail", Owner{ID: "mail", Name: "Mail", Kind: KindApp, Safety: Danger, Why: "Mail.app's local copy of your mailboxes and attachments.", Regen: "IMAP mail is downloaded again; local mailboxes are not."}),
		at("~/Library/Messages", Owner{ID: "messages", Name: "Messages", Kind: KindApp, Safety: Danger, Why: "Your message history and attachments.", Regen: "Not recoverable unless Messages in iCloud is on."}),

		// Generic app locations, after every specific rule above.
		each("~/Library/Caches", func(name string) Owner {
			return cache("cache:"+name, prettyBundle(name)+" cache", "The app rebuilds it as it runs.")
		}),
		each("~/Library/Application Support", func(name string) Owner {
			return Owner{ID: "appsupport:" + name, Name: prettyBundle(name), Kind: KindApp, Safety: Careful, Why: prettyBundle(name) + " keeps its data and settings here.", Regen: "The app may lose settings, sign-ins or local data."}
		}),
		each("~/Library/Containers", func(name string) Owner {
			return Owner{ID: "container:" + name, Name: prettyBundle(name), Kind: KindApp, Safety: Careful, Why: prettyBundle(name) + " is sandboxed; this is all of its data.", Regen: "The app loses its local data."}
		}),
		each("~/Library/Group Containers", func(name string) Owner {
			return Owner{ID: "group:" + name, Name: prettyBundle(name), Kind: KindApp, Safety: Careful, Why: "Data shared between " + prettyBundle(name) + " and its extensions.", Regen: "The apps lose shared local data."}
		}),
		each("/Applications", func(name string) Owner {
			n := strings.TrimSuffix(name, ".app")
			return Owner{ID: "application:" + n, Name: n, Kind: KindApp, Safety: Careful, Why: "The " + n + " app itself.", Regen: "Reinstalled from where it came from."}
		}),
		at("~/.cursor", Owner{ID: "cursor-ext", Name: "Cursor extensions", Kind: KindApp, Safety: Careful, Why: "Installed Cursor extensions; old versions are kept after updates.", Regen: "Extensions are downloaded again."}),
		at("~/.antigravity", Owner{ID: "antigravity-ext", Name: "Antigravity extensions", Kind: KindApp, Safety: Careful, Why: "Installed Antigravity extensions; old versions are kept after updates.", Regen: "Extensions are downloaded again."}),
		at("~/.gemini", Owner{ID: "gemini-cli", Name: "Gemini CLI data", Kind: KindApp, Safety: Careful, Why: "Gemini CLI's settings, history and downloaded tools.", Regen: "History is not recoverable."}),
		at("~/.copilot", Owner{ID: "copilot-cli", Name: "Copilot CLI data", Kind: KindApp, Safety: Careful, Why: "GitHub Copilot CLI's packages and session state.", Regen: "Packages are downloaded again; sessions are not recoverable."}),
		at("~/.codex", Owner{ID: "codex", Name: "Codex CLI data", Kind: KindApp, Safety: Careful, Why: "Codex CLI's settings and session logs.", Regen: "Sessions are not recoverable."}),
		at("~/.gradle/wrapper", cache("gradle-wrapper", "Gradle wrapper distributions", "Each project's Gradle version is downloaded again on its next build.")),
		at("~/.local/share/uv", Owner{ID: "uv-tools", Name: "uv Pythons and tools", Kind: KindToolchain, Safety: Careful, Why: "Python versions and tools installed with `uv python install` and `uv tool install`.", Regen: "Installed again by uv when asked."}),
		at("~/go/bin", Owner{ID: "go-bin", Name: "Go installed binaries", Kind: KindToolchain, Safety: Careful, Why: "Programs installed with `go install`.", Regen: "Reinstalled with `go install`."}),
		at("~/.kube/cache", cache("kube-cache", "kubectl cache", "kubectl fetches API discovery again.")),
		at("~/.gvm", Owner{ID: "gvm", Name: "gvm Go versions", Kind: KindToolchain, Safety: Careful, Why: "Go versions installed by gvm, and the archive it builds them from.", Regen: "Installed again by `gvm install`."}),
		each("~/.cache", func(name string) Owner {
			return cache("xdg-cache:"+name, name+" cache", "The tool rebuilds it as it runs.")
		}),
		at("~/.vscode", Owner{ID: "vscode-ext", Name: "VS Code extensions", Kind: KindApp, Safety: Careful, Why: "Installed VS Code extensions; old versions are kept after updates.", Regen: "Extensions are downloaded again."}),
		at("~/Library", Owner{ID: "library", Name: "Library (other)", Kind: KindSystem, Safety: Careful, Why: "The rest of ~/Library: preferences, logs and app state.", Regen: "Leave it alone unless you know the app."}),
	}
}

// prettyBundle turns com.spotify.client into "Spotify" and
// VUTU7AKEUR.jp.naver.line.mac into "line". Plain names are kept.
func prettyBundle(name string) string {
	parts := strings.Split(name, ".")
	if len(parts) < 3 {
		return name
	}
	skip := map[string]bool{"com": true, "org": true, "net": true, "io": true, "jp": true, "dev": true, "app": true, "mac": true, "macos": true, "client": true, "desktop": true, "helper": true, "ShipIt": true}
	for i := len(parts) - 1; i >= 1; i-- {
		p := parts[i]
		if skip[p] || len(p) < 2 {
			continue
		}
		return strings.ToUpper(p[:1]) + p[1:]
	}
	return name
}

// artifact is a build output found inside a project.
type artifact struct {
	match func(n *walk.Node, parent *walk.Node) bool
	owner Owner
}

func named(names ...string) func(n, _ *walk.Node) bool {
	return func(n, _ *walk.Node) bool {
		return slices.Contains(names, n.Name)
	}
}

func build(id, name, why, regen string, acts ...Action) Owner {
	return Owner{ID: id, Name: name, Kind: KindBuild, Safety: Safe, Why: why, Regen: regen, Actions: append(acts, trash(Safe))}
}

func artifacts() []artifact {
	return []artifact{
		{
			match: func(n, p *walk.Node) bool { return n.Name == "target" && p.Has(walk.MarkCargo) },
			owner: build("cargo-target", "Cargo target/", "Compiled Rust: every profile, every dependency, incremental caches.", "Rebuilt by the next `cargo build`.",
				Action{Label: "cargo clean", Cmd: []string{"cargo", "clean", "--manifest-path", "{parent}/Cargo.toml", "--target-dir", "{path}"}, Safety: Safe, Needs: "cargo"}),
		},
		{
			// Cargo tags its target dir, but with no Cargo.toml next to it
			// (a custom target-dir, a workspace member) `cargo clean` could
			// clean some other project; only offer the Trash.
			match: func(n, _ *walk.Node) bool { return n.Name == "target" && n.Has(walk.MarkCacheDirTag) },
			owner: build("cargo-target", "Cargo target/", "Compiled Rust: every profile, every dependency, incremental caches.", "Rebuilt by the next `cargo build`."),
		},
		{
			match: func(n, p *walk.Node) bool { return (n.Name == "deps" || n.Name == "_build") && p.Has(walk.MarkMix) },
			owner: build("mix", "Mix deps and _build", "Elixir dependencies and compiled output.", "Fetched and compiled again by `mix deps.get` and `mix compile`."),
		},
		{
			match: named("node_modules"),
			owner: build("node-modules", "node_modules/", "Installed npm packages for this project.", "Installed again by `npm install` / `pnpm install`."),
		},
		{
			match: named(".next", ".nuxt", ".svelte-kit", ".turbo", ".parcel-cache", ".angular", ".expo", ".docusaurus", ".astro", ".vercel"),
			owner: build("web-build", "web build cache", "A JavaScript framework's build output and cache.", "Rebuilt by the next dev or build run."),
		},
		{
			match: func(n, _ *walk.Node) bool { return n.Has(walk.MarkPyvenv) },
			owner: build("venv", "Python virtualenv", "A virtual environment with this project's installed packages.", "Recreated by `uv sync`, `poetry install` or `pip install -r`."),
		},
		{
			match: named(".gradle", ".terraform", ".mypy_cache", ".pytest_cache", ".ruff_cache", ".tox", ".nox", ".zig-cache", "zig-cache", "zig-out", ".stack-work", ".dart_tool", ".build", "DerivedData", ".cache", ".wrangler"),
			owner: build("tool-cache", "tool cache", "A build or test tool's cache for this project.", "Recreated by the tool on its next run."),
		},
		{
			match: func(n, _ *walk.Node) bool { return n.Has(walk.MarkCacheDirTag) },
			owner: build("cachedir", "cache (CACHEDIR.TAG)", "A directory its tool marked as a cache with CACHEDIR.TAG.", "Recreated by the tool that made it."),
		},
	}
}

var projectOwner = Owner{
	ID: "project", Kind: KindProject, Safety: Danger,
	Why:   "A git working tree. Source, data and build outputs the project made.",
	Regen: "Uncommitted work and ignored files are not in git.",
}

var unclaimedOwner = Owner{
	ID: "unclaimed", Name: "Unclaimed", Kind: KindUnclaimed, Safety: Danger,
	Why:     "No rule knows what this is. It is shown so nothing large goes unseen.",
	Regen:   "Unknown. Look inside before removing anything.",
	Actions: []Action{trash(Danger)},
}
