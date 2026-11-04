package tui

import (
	"context"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanywst/chonk/internal/inspect"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/walk"
)

const gib = int64(1) << 30

type fake struct {
	rep      *owner.Report
	reclaims []string
	after    *walk.Node
}

func (f *fake) Scan(context.Context, *walk.Progress) *owner.Report { return f.rep }
func (f *fake) Inspect(_ context.Context, it *owner.Item) inspect.Report {
	if it.Owner.Inspect == "rancher-desktop" {
		return inspect.Report{Trapped: 70 * gib, Lines: []inspect.Line{{Label: "Trapped", Value: "70.0G"}}}
	}
	return inspect.Report{}
}
func (f *fake) Reclaim(_ context.Context, it *owner.Item, a owner.Action) reclaim.Result {
	f.reclaims = append(f.reclaims, a.Label)
	return reclaim.Result{Path: it.Path, Action: a.Label, Freed: 70 * gib}
}
func (f *fake) Measure(string) *walk.Node { return f.after }
func (f *fake) Free() (int64, int64)      { return 100 * gib, 500 * gib }

func report() *owner.Report {
	vm := &owner.Owner{ID: "rancher-desktop", Name: "Rancher Desktop VM", Kind: owner.KindVM, Inspect: "rancher-desktop", Why: "a VM",
		Actions: []owner.Action{{Label: "fstrim", Cmd: []string{"true"}}}}
	cache := &owner.Owner{ID: "c", Name: "Spotify cache", Kind: owner.KindCache, Why: "a cache"}
	items := []*owner.Item{
		{Owner: vm, Path: "/h/vm", Size: 80 * gib, Node: &walk.Node{Name: "/h/vm", Size: 80 * gib}},
		{Owner: cache, Path: "/h/c", Size: 2 * gib, Node: &walk.Node{Name: "/h/c", Size: 2 * gib}},
	}
	return &owner.Report{Items: items, Total: 82 * gib}
}

func step(t *testing.T, m tea.Model, msg tea.Msg) tea.Model {
	t.Helper()
	m, cmd := m.Update(msg)
	// Run commands that produce our own messages, so the flow is synchronous.
	for cmd != nil {
		out := cmd()
		switch out := out.(type) {
		case tea.BatchMsg:
			for _, c := range out {
				if c == nil {
					continue
				}
				switch r := c().(type) {
				case scanDoneMsg, inspectedMsg, reclaimedMsg:
					m = step(t, m, r)
				}
			}
			return m
		case scanDoneMsg, inspectedMsg, reclaimedMsg:
			m, cmd = m.Update(out)
		default:
			return m
		}
	}
	return m
}

func key(s string) tea.KeyPressMsg {
	switch s {
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	}
	return tea.KeyPressMsg{Code: rune(s[0]), Text: s}
}

func view(m tea.Model) string { return ansi.Strip(m.View().Content) }

func TestFlow(t *testing.T) {
	f := &fake{rep: report(), after: &walk.Node{Name: "vm", Size: 10 * gib}}
	var m tea.Model = newModel(context.Background(), Config{Backend: f, NoSave: true})
	m = step(t, m, tea.WindowSizeMsg{Width: 110, Height: 40})
	m = step(t, m, m.(model).scanCmd()())

	v := view(m)
	for _, want := range []string{"82.0G scanned", "Rancher Desktop VM", "70.0G trapped", "Spotify cache", "VMs and containers"} {
		if !strings.Contains(v, want) {
			t.Errorf("list is missing %q:\n%s", want, v)
		}
	}

	m = step(t, m, key("enter"))
	v = view(m)
	if !strings.Contains(v, "a VM") || !strings.Contains(v, "fstrim") {
		t.Fatalf("detail:\n%s", v)
	}
	m = step(t, m, key("enter"))
	if !strings.Contains(view(m), "y run") {
		t.Fatalf("no confirm dialog:\n%s", view(m))
	}
	// Anything but y cancels.
	m = step(t, m, key("enter"))
	if len(f.reclaims) != 0 || strings.Contains(view(m), "y run") {
		t.Fatal("enter must not confirm")
	}
	m = step(t, m, key("1"))
	m = step(t, m, key("y"))
	if len(f.reclaims) != 1 {
		t.Fatalf("reclaims = %v", f.reclaims)
	}
	v = view(m)
	if !strings.Contains(v, "70.0G freed") {
		t.Errorf("no result:\n%s", v)
	}
	mm := m.(model)
	if mm.rep.Total != 12*gib || mm.freed != 70*gib {
		t.Errorf("total = %d freed = %d", mm.rep.Total, mm.freed)
	}

	m = step(t, m, key("esc"))
	m = step(t, m, key("/"))
	for _, r := range "spot" {
		m = step(t, m, key(string(r)))
	}
	v = view(m)
	if strings.Contains(v, "Rancher Desktop VM  ") || !strings.Contains(v, "Spotify cache") {
		t.Errorf("filter:\n%s", v)
	}
}
