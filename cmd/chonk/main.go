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

	"github.com/kanywst/chonk/internal/demo"
	"github.com/kanywst/chonk/internal/format"
	"github.com/kanywst/chonk/internal/history"
	"github.com/kanywst/chonk/internal/inspect"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/procs"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/scan"
	"github.com/kanywst/chonk/internal/schedule"
	"github.com/kanywst/chonk/internal/system"
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
  chonk snapshot     scan and record the totals, printing one line
  chonk schedule on  record a snapshot every day (macOS launchd; also off,
                     or no argument for the status)
  chonk version      print the version (also -v, --version)

Flags:
  --json             print JSON (ls, why, diff)
  --all              ls: list items under 100M too
  --no-save          do not record this scan in the history
  --hour N           schedule on: the local hour of the daily run (default 12)

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
	hour              int
}

func run(ctx context.Context, args []string, out, errOut io.Writer) error {
	fs := flag.NewFlagSet("chonk", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var f flags
	fs.BoolVar(&f.json, "json", false, "")
	fs.BoolVar(&f.all, "all", false, "")
	fs.BoolVar(&f.noSave, "no-save", false, "")
	fs.IntVar(&f.hour, "hour", 12, "")
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
		if os.Getenv("CHONK_DEMO") != "" {
			// Canned data for the README recording: nothing real is shown,
			// saved or run.
			return tui.Run(ctx, tui.Config{Home: demo.Home, Backend: demo.New(), NoSave: true})
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
	case "snapshot":
		return app.snapshot(ctx, out, errOut)
	case "schedule":
		if len(words) > 2 {
			return errors.New("usage: chonk schedule [on|off]")
		}
		return app.schedule(ctx, words[1:], state, out)
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
	SizeUnknown bool            `json:"size_unknown,omitempty"`
	Processes   []procs.Proc    `json:"processes,omitempty"`
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
		SizeUnknown: it.SizeUnknown,
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
	ps, _ := procs.List(ctx, procs.Exec)
	running := procs.Match(rep.Items, ps, a.home)
	vol, _ := system.VolumeOf(a.home)
	outside := max(0, vol.Used-rep.Total)
	if a.f.json {
		items := make([]jsonItem, 0, len(rep.Items))
		for _, it := range rep.Items {
			j := toJSON(it)
			j.Processes = running[it]
			items = append(items, j)
		}
		return writeJSON(out, map[string]any{
			"total_bytes": rep.Total, "volume_used_bytes": vol.Used, "volume_free_bytes": vol.Free,
			"outside_scan_bytes": outside, "items": items,
		})
	}
	base, hasBase := history.Baseline(past, snap.Time, 24*time.Hour)
	minSize := int64(100 << 20)
	if a.f.all {
		minSize = 0
	}
	fmt.Fprintf(out, "%s scanned, %s free of %s", format.Bytes(rep.Total), format.Bytes(vol.Free), format.Bytes(vol.Total))
	if outside > 1<<30 {
		fmt.Fprintf(out, ", %s on the volume outside the scan (swap, logs, other users, /Library)", format.Bytes(outside))
	}
	fmt.Fprintln(out)
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
			if it.Size < minSize && !it.SizeUnknown && (it.Node == nil || !it.Node.Denied) {
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
			if it.Node != nil && it.Node.Denied {
				extra = "unreadable (give the terminal Full Disk Access)"
			}
			if p := running[it]; len(p) > 0 {
				extra = strings.TrimSpace(extra + "  " + format.Bytes(procs.Total(p)) + " running (" + p[0].Name() + ")")
			}
			size := format.Bytes(it.Size)
			if it.SizeUnknown {
				size = "?"
			}
			fmt.Fprintf(tw, "  %s\t%7s\t%s\t%s\t\n", it.Name(), size, delta, extra)
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
	if fi, err := os.Stat(claimed); err == nil && !fi.IsDir() {
		claimed = filepath.Dir(claimed) // a file: measure the directory it is in
	}
	nodes := walk.Walk(ctx, []string{claimed}, walk.Options{MinKeep: 1 << 20}, nil)
	if err := ctx.Err(); err != nil {
		return err
	}
	if len(nodes) == 0 {
		return fmt.Errorf("cannot read %s", claimed)
	}
	n := nodes[0]
	target := &owner.Item{Owner: o, Path: claimed, Node: n, Size: n.Size, Logical: n.Logical, Files: n.Files}
	// Classify what was walked, so the size leaves out what other rules or
	// nested projects claim inside it, the same as `chonk ls`.
	for _, it := range owner.Classify(nodes, owner.Options{}).Items {
		if it.Path == claimed && it.Owner.ID == o.ID {
			target.Size, target.Parts = it.Size, it.Parts
		}
	}
	if n.Touched > 0 {
		target.Touched = time.Unix(n.Touched, 0)
	}
	rpt := inspect.New(a.home).Inspect(ctx, target)
	ps, _ := procs.List(ctx, procs.Exec)
	running := procs.Match([]*owner.Item{target}, ps, a.home)[target]
	if a.f.json {
		j := toJSON(target)
		j.Inspect = &rpt
		j.Processes = running
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
	if len(running) > 0 {
		fmt.Fprintf(out, "\nrunning (%s in memory):\n", format.Bytes(procs.Total(running)))
		for i, p := range running {
			if i >= 5 {
				break
			}
			fmt.Fprintf(out, "  %7s  %s (pid %d)\n", format.Bytes(p.RSS), p.Name(), p.PID)
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

// snapshot is what the daily job runs: a full scan, recorded, and one line
// for the log.
func (a *app) snapshot(ctx context.Context, out, errOut io.Writer) error {
	a.f.noSave = false
	rep, snap, _ := a.scan(ctx, errOut)
	if err := ctx.Err(); err != nil {
		return err
	}
	_, err := fmt.Fprintf(out, "%s recorded %s in %d items\n", snap.Time.Format(time.RFC3339), format.Bytes(snap.Total), len(rep.Items))
	return err
}

func (a *app) schedule(ctx context.Context, words []string, state string, out io.Writer) error {
	if a.f.hour < 0 || a.f.hour > 23 {
		return fmt.Errorf("--hour %d: want 0-23", a.f.hour)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	// Not resolved through symlinks: Homebrew's bin/chonk link survives an
	// upgrade, the versioned path it points to does not.
	ag := schedule.Agent{
		Home: a.home, State: state, Exe: exe, Hour: a.f.hour, UID: os.Getuid(),
		XDG: os.Getenv("XDG_STATE_HOME"), Run: schedule.Runner(procs.Exec),
	}
	cmd := ""
	if len(words) > 0 {
		cmd = words[0]
	}
	switch cmd {
	case "on":
		if err := ag.Install(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintf(out, "chonk snapshot will run every day at %02d:00 (or when the Mac wakes, if it slept through it).\n  job: %s\n  log: %s\nTo measure protected folders, give %s Full Disk Access too; without it they are left out of the daily totals.\n", ag.Hour, owner.Tilde(ag.Path()), owner.Tilde(ag.Log()), exe)
		return err
	case "off":
		if err := ag.Remove(ctx); err != nil {
			return err
		}
		_, err := fmt.Fprintln(out, "the daily snapshot is off")
		return err
	case "":
		installed, loaded := ag.Status(ctx)
		switch {
		case installed && loaded:
			_, err = fmt.Fprintf(out, "on: %s\nlog: %s\n", owner.Tilde(ag.Path()), owner.Tilde(ag.Log()))
		case installed:
			_, err = fmt.Fprintf(out, "installed but not loaded: %s (run `chonk schedule on` again)\n", owner.Tilde(ag.Path()))
		default:
			_, err = fmt.Fprintln(out, "off (turn it on with `chonk schedule on`)")
		}
		return err
	default:
		return fmt.Errorf("unknown schedule command %q; want on or off", cmd)
	}
}
