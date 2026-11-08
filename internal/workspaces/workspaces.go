// Package workspaces finds the per-workspace state a VS Code-family editor
// keeps for folders that are no longer on this disk.
package workspaces

import (
	"encoding/json/v2"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/walk"
)

// editors are the VS Code family. Each keeps one directory per workspace
// under User/workspaceStorage, holding that workspace's open editors, undo
// history and extension state, and names the folder it belongs to in a
// workspace.json beside it. Nothing removes those when the folder goes.
var editors = []string{
	"Code", "Code - Insiders", "Cursor", "VSCodium", "Antigravity", "Windsurf", "Trae", "Positron",
}

// staleOwner is state for a workspace whose folder is gone. It is safe: the
// folder it describes no longer exists, so there is nothing left to restore
// it for.
var staleOwner = owner.Owner{
	ID: "workspace-storage-stale", Name: "workspace state", Kind: owner.KindApp, Safety: owner.Safe,
	Why:     "An editor's state for a workspace whose folder is no longer on this disk.",
	Regen:   "Nothing asks for it again: the folder it belongs to is gone.",
	Actions: []owner.Action{{Label: "Move to the Trash", Trash: true, Safety: owner.Safe}},
}

// Apply points each editor's item at the workspace state left behind.
func Apply(rep *owner.Report) {
	for _, it := range rep.Items {
		name, ok := strings.CutPrefix(it.Owner.ID, "appsupport:")
		if !ok || !isEditor(name) {
			continue
		}
		apply(it)
	}
}

func isEditor(name string) bool {
	for _, e := range editors {
		if name == e {
			return true
		}
	}
	return false
}

func apply(it *owner.Item) {
	if it.Node == nil || len(it.Parts) > 0 {
		return
	}
	dir := descend(it.Node, []string{"User", "workspaceStorage"})
	if dir == nil {
		return
	}
	for _, ch := range dir.Children {
		if !stale(filepath.Join(ch.Path(), "workspace.json")) {
			continue
		}
		it.Parts = append(it.Parts, &owner.Item{
			Owner: &staleOwner, Path: ch.Path(), Node: ch,
			Size: ch.Size, Logical: ch.Logical, Files: ch.Files,
		})
	}
	if len(it.Parts) == 0 {
		return
	}
	it.PartsLabel = "for folders that are gone"
	c := *it.Owner
	c.Regen = c.Regen + " " + stored(len(it.Parts)) + " a folder that is no longer here."
	it.Owner = &c
}

// stored names how many of an editor's stored workspaces point nowhere.
func stored(n int) string {
	if n == 1 {
		return "1 stored workspace names"
	}
	return strconv.Itoa(n) + " stored workspaces name"
}

// stale reports whether a workspace.json names a local folder that is not
// there any more. Anything it cannot check — an unreadable or absent file, a
// workspace with no folder, a path chonk cannot verify — is not stale, so a
// workspace is only ever listed when its folder is known to be gone.
func stale(path string) bool {
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	var ws struct {
		Folder    string `json:"folder"`
		Workspace string `json:"workspace"`
	}
	if json.Unmarshal(b, &ws) != nil {
		return false
	}
	uri := ws.Folder
	if uri == "" {
		uri = ws.Workspace
	}
	p := localPath(uri)
	if p == "" {
		return false
	}
	_, err = os.Lstat(p)
	return os.IsNotExist(err)
}

// localPath is the path a workspace URI names, when it is one on this disk
// that chonk can check. A remote workspace (vscode-remote://, ssh) lives on
// another machine, and one on a volume that is not mounted is not gone, only
// away, so both come back empty.
func localPath(uri string) string {
	rest, ok := strings.CutPrefix(uri, "file://")
	if !ok {
		return ""
	}
	p, err := url.PathUnescape(rest)
	if err != nil || !strings.HasPrefix(p, "/") {
		return ""
	}
	if vol, _, _ := strings.Cut(strings.TrimPrefix(p, "/"), "/"); vol == "Volumes" || vol == "net" || vol == "mnt" {
		return ""
	}
	return p
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
