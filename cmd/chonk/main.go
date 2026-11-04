// Command chonk shows what is eating your disk, why it exists, and the
// right way to get the space back.
package main

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"text/tabwriter"
	"time"

	"golang.org/x/term"

	"github.com/kanywst/chonk/internal/format"
	"github.com/kanywst/chonk/internal/history"
	"github.com/kanywst/chonk/internal/inspect"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/scan"
	"github.com/kanywst/chonk/internal/tui"
	"github.com/kanywst/chonk/internal/version"
	"github.com/kanywst/chonk/internal/walk"
)

const usage = `chonk shows what is eating your disk, why it exists, and the right way to
get the space back.

Usage:
  chonk              open the interactive view
  chonk ls           everything, grouped by who put it there, biggest first
  chonk why PATH     who owns PATH, why it exists, and how to reclaim it
  chonk diff         what grew or shrank since the previous scan
  chonk version      print the version (also -v, --version)

Flags:
  --json             print JSON (ls, why, diff)
  --all              ls: list items under 100M too
  --no-save          do not record this scan in the history

chonk looks under your home, /Applications, /opt/homebrew, /usr/local and
/nix. Sizes are space on disk, so sparse VM images count for what they hold.
`

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "chonk:", err)
		os.Exit(1)
	}
}

type flags struct {
	json, all, noSave bool
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("chonk", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f flags
	fs.BoolVar(&f.json, "json", false, "")
	fs.BoolVar(&f.all, "all", false, "")
	fs.BoolVar(&f.noSave, "no-save", false, "")
	help := fs.Bool("h", false, "")
	fs.BoolVar(help, "help", false, "")
	ver := fs.Bool("v", false, "")
	fs.BoolVar(ver, "version", false, "")

	// Flags may come before or after the command.
	var words []string
	rest := args
	for {
		if err := fs.Parse(rest); err != nil {
			return fmt.Errorf("%w\n\n%s", err, usage)
		}
		if fs.NArg() == 0 {
			break
		}
		words = append(words, fs.Arg(0))
		rest = fs.Args()[1:]
	}
	if *help {
		_, err := io.WriteString(out, usage)
		return err
	}
	if *ver {
		words = []string{"version"}
	}

	home, err := scan.Home()
	if err != nil {
		return err
	}
	state := history.StateDir(home)
	app := &app{home: home, store: history.Store{Path: filepath.Join(state, "snapshots.jsonl")}, ledger: reclaim.Ledger{Path: filepath.Join(state, "ledger.jsonl")}, f: f}

	cmd := ""
	if len(words) > 0 {
		cmd = words[0]
	}
	switch cmd {
	case "":
		if !term.IsTerminal(int(os.Stdout.Fd())) {
			return errors.New("the interactive view needs a terminal; use `chonk ls`")
		}
		return tui.Run(ctx, tui.Config{Home: home, Store: app.store, Ledger: app.ledger, NoSave: f.noSave})
	case "ls":
		return app.ls(ctx, out, errOut)
	case "why":
		if len(words) != 2 {
			return errors.New("usage: chonk why PATH")
		}
		return app.why(ctx, words[1], out)
	case "diff":
		return app.diff(ctx, out, errOut)
	case "version":
		_, err := fmt.Fprintln(out, "chonk", version.String())
		return err
	default:
		return fmt.Errorf("unknown command %q\n\n%s", cmd, usage)
	}
}

type app struct {
	home   string
	store  history.Store
	ledger reclaim.Ledger
	f      flags
}

// scan walks the machine, printing progress to errOut when it is a
// terminal, and records the result in the history.
func (a *app) scan(ctx context.Context, errOut io.Writer) (*owner.Report, history.Snapshot, []history.Snapshot) {
	var prog walk.Progress
	done := make(chan struct{})
	if f, ok := errOut.(*os.File); ok && term.IsTerminal(int(f.Fd())) {
		go func() {
			t := time.NewTicker(150 * time.Millisecond)
			defer t.Stop()
			for {
				select {
				case <-done:
					fmt.Fprint(errOut, "\r\033[K")
					return
				case <-t.C:
					fmt.Fprintf(errOut, "\r\033[Kscanning: %s in %d files", format.Bytes(prog.Bytes.Load()), prog.Files.Load())
				}
			}
		}()
	}
	rep := scan.Run(ctx, scan.Roots(a.home), &prog)
	close(done)
	time.Sleep(10 * time.Millisecond)
	now := time.Now()
	snap := scan.Snapshot(rep, now)
	past, _ := a.store.Load()
	if !a.f.noSave && ctx.Err() == nil {
		_ = a.store.Add(snap, 400)
	}
	return rep, snap, past
}

type jsonItem struct {
	Kind        string          `json:"kind"`
	Owner       string          `json:"owner"`
	Name        string          `json:"name"`
	Path        string          `json:"path"`
	Size        int64           `json:"size_bytes"`
	Logical     int64           `json:"logical_bytes"`
	Touched     time.Time       `json:"touched,omitzero"`
	Safety      string          `json:"safety"`
	Why         string          `json:"why"`
	Regen       string          `json:"regen,omitempty"`
	Hotspot     string          `json:"hotspot,omitempty"`
	Actions     []jsonAction    `json:"actions,omitempty"`
	Parts       []jsonItem      `json:"parts,omitempty"`
	Reclaimable int64           `json:"reclaimable_bytes,omitempty"`
	Inspect     *inspect.Report `json:"inspect,omitempty"`
}

type jsonAction struct {
	Label   string `json:"label"`
	Command string `json:"command"`
	Safety  string `json:"safety"`
	Note    string `json:"note,omitempty"`
}

func toJSON(it *owner.Item) jsonItem {
	j := jsonItem{
		Kind: it.Owner.Kind.Slug(), Owner: it.Owner.ID, Name: it.Name(), Path: it.Path,
		Size: it.Size, Logical: it.Logical, Touched: it.Touched, Safety: it.Owner.Safety.String(),
		Why: it.Owner.Why, Regen: it.Owner.Regen, Hotspot: it.Hotspot, Reclaimable: it.Reclaimable(),
	}
	for _, ac := range it.Owner.Actions {
		e := ac.Expand(it.Path)
		j.Actions = append(j.Actions, jsonAction{Label: e.Label, Command: e.Command(), Safety: e.Safety.String(), Note: e.Note})
	}
	for _, p := range it.Parts {
		j.Parts = append(j.Parts, toJSON(p))
	}
	return j
}

func writeJSON(out io.Writer, v any) error {
	return json.MarshalWrite(out, v, jsontext.WithIndent("  "))
}

func (a *app) ls(ctx context.Context, out, errOut io.Writer) error {
	rep, snap, past := a.scan(ctx, errOut)
	if err := ctx.Err(); err != nil {
		return err
	}
	if a.f.json {
		items := make([]jsonItem, 0, len(rep.Items))
		for _, it := range rep.Items {
			items = append(items, toJSON(it))
		}
		return writeJSON(out, map[string]any{"total_bytes": rep.Total, "items": items})
	}
	base, hasBase := history.Baseline(past, snap.Time, 24*time.Hour)
	minSize := int64(100 << 20)
	if a.f.all {
		minSize = 0
	}
	free, total, _ := reclaim.Free(a.home)
	fmt.Fprintf(out, "%s scanned, %s free of %s\n", format.Bytes(rep.Total), format.Bytes(free), format.Bytes(total))
	if hasBase {
		fmt.Fprintf(out, "change since %s: %s\n", base.Time.Format("2006-01-02 15:04"), orZero(format.Delta(snap.Total-base.Total)))
	}
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	for _, k := range rep.KindsBySize() {
		items := rep.ByKind(k)
		if len(items) == 0 {
			continue
		}
		fmt.Fprintf(tw, "\n%s\t%7s\t\t\t\n", k, format.Bytes(rep.KindTotal(k)))
		for _, it := range items {
			if it.Size < minSize {
				continue
			}
			delta := ""
			if hasBase {
				delta = format.Delta(it.Size - base.Items[owner.Tilde(it.Path)])
			}
			extra := ""
			if r := it.Reclaimable(); r > 0 {
				extra = format.Bytes(r) + " build output"
			}
			fmt.Fprintf(tw, "  %s\t%7s\t%s\t%s\t\n", it.Name(), format.Bytes(it.Size), delta, extra)
		}
	}
	return tw.Flush()
}

func orZero(s string) string {
	if s == "" {
		return "no change"
	}
	return s
}

func (a *app) why(ctx context.Context, path string, out io.Writer) error {
	abs, err := filepath.Abs(owner.Untilde(path))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(abs); err != nil {
		return err
	}
	o, claimed, project := owner.Explain(abs)
	nodes := walk.Walk(ctx, []string{claimed}, walk.Options{MinKeep: 1 << 20}, nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("cannot read %s", claimed)
	}
	n := nodes[0]
	target := &owner.Item{Owner: o, Path: claimed, Node: n, Size: n.Size, Logical: n.Logical, Files: n.Files}
	if n.Touched > 0 {
		target.Touched = time.Unix(n.Touched, 0)
	}
	rpt := inspect.New(a.home).Inspect(ctx, target)
	if a.f.json {
		j := toJSON(target)
		j.Inspect = &rpt
		return writeJSON(out, j)
	}
	fmt.Fprintf(out, "%s\n  %s, %s on disk", owner.Tilde(abs), target.Name(), format.Bytes(target.Size))
	if abs != claimed {
		if sub := walk.Find(nodes, abs); sub != nil {
			fmt.Fprintf(out, " (this path: %s)", format.Bytes(sub.Size))
		}
		fmt.Fprintf(out, "\n  claimed at %s", owner.Tilde(claimed))
	}
	fmt.Fprintln(out)
	if project != "" {
		fmt.Fprintf(out, "  build output of the project %s\n", owner.Tilde(project))
	}
	fmt.Fprintf(out, "\nwhy:     %s\nregen:   %s\nsafety:  %s\n", o.Why, o.Regen, o.Safety)
	if !target.Touched.IsZero() {
		fmt.Fprintf(out, "written: %s ago\n", format.Age(time.Since(target.Touched)))
	}
	if len(rpt.Lines) > 0 || rpt.Note != "" {
		fmt.Fprintln(out, "\ninside:")
		for _, l := range rpt.Lines {
			fmt.Fprintf(out, "  %s: %s\n", l.Label, l.Value)
		}
		if rpt.Note != "" {
			fmt.Fprintf(out, "  %s\n", rpt.Note)
		}
	}
	if len(n.Children) > 0 {
		fmt.Fprintln(out, "\nbiggest inside:")
		for i, c := range n.Children {
			if i >= 5 {
				break
			}
			fmt.Fprintf(out, "  %7s  %s\n", format.Bytes(c.Size), c.Name)
		}
	}
	if len(o.Actions) > 0 {
		fmt.Fprintln(out, "\nreclaim:")
		for _, ac := range o.Actions {
			e := ac.Expand(claimed)
			fmt.Fprintf(out, "  [%s] %s\n      %s\n", e.Safety, e.Label, e.Command())
		}
	}
	return nil
}

func (a *app) diff(ctx context.Context, out, errOut io.Writer) error {
	past, err := a.store.Load()
	if err != nil {
		return err
	}
	if len(past) == 0 {
		return errors.New("no earlier scan to compare with; run `chonk ls` once, then again later")
	}
	_, snap, _ := a.scan(ctx, errOut)
	if err := ctx.Err(); err != nil {
		return err
	}
	base, _ := history.Baseline(past, snap.Time, 24*time.Hour)
	changes := history.Diff(base, snap)
	if a.f.json {
		return writeJSON(out, map[string]any{"since": base.Time, "total_delta_bytes": snap.Total - base.Total, "changes": changes})
	}
	fmt.Fprintf(out, "since %s (%s ago): %s\n\n", base.Time.Format("2006-01-02 15:04"), format.Age(snap.Time.Sub(base.Time)), orZero(format.Delta(snap.Total-base.Total)))
	tw := tabwriter.NewWriter(out, 0, 2, 2, ' ', 0)
	shown := 0
	for _, c := range changes {
		d := format.Delta(c.Delta())
		if d == "" || shown >= 30 {
			continue
		}
		shown++
		fmt.Fprintf(tw, "%8s\t%7s\t%s\t\n", d, format.Bytes(c.Now), strings.TrimSpace(c.Key))
	}
	if shown == 0 {
		fmt.Fprintln(tw, "nothing changed by more than 1M")
	}
	return tw.Flush()
}
