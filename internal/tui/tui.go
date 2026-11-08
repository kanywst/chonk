// Package tui is the interactive view of chonk.
package tui

import (
	"context"
	"strings"
	"time"

	"charm.land/bubbles/v2/spinner"
	tea "charm.land/bubbletea/v2"

	"github.com/kanywst/chonk/internal/history"
	"github.com/kanywst/chonk/internal/inspect"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/procs"
	"github.com/kanywst/chonk/internal/reclaim"
	"github.com/kanywst/chonk/internal/scan"
	"github.com/kanywst/chonk/internal/system"
	"github.com/kanywst/chonk/internal/walk"
)

// Config is what the view needs.
type Config struct {
	Home   string
	Store  history.Store
	Ledger reclaim.Ledger
	NoSave bool
	// Backend replaces the real machine, for tests and the demo.
	Backend Backend
}

// Backend scans, inspects and reclaims. System is the real one.
type Backend interface {
	Scan(ctx context.Context, prog *walk.Progress) *owner.Report
	Inspect(ctx context.Context, it *owner.Item) inspect.Report
	Reclaim(ctx context.Context, it *owner.Item, a owner.Action) reclaim.Result
	// Measure walks one path again; nil when it is gone.
	Measure(path string) *walk.Node
	Volume() system.Volume
	Procs(ctx context.Context) []procs.Proc
}

// System is the Backend for this machine.
type System struct {
	Home string
	in   *inspect.Inspector
}

// NewSystem returns the Backend for this machine.
func NewSystem(home string) *System { return &System{Home: home, in: inspect.New(home)} }

// Scan walks the machine.
func (s *System) Scan(ctx context.Context, prog *walk.Progress) *owner.Report {
	return scan.Run(ctx, s.Home, scan.Roots(s.Home), prog)
}

// Inspect looks inside an item.
func (s *System) Inspect(ctx context.Context, it *owner.Item) inspect.Report {
	return s.in.Inspect(ctx, it)
}

// Reclaim runs an action.
func (s *System) Reclaim(ctx context.Context, it *owner.Item, a owner.Action) reclaim.Result {
	return reclaim.Do(ctx, s.Home, it, a, reclaim.Exec)
}

// Measure walks one path again.
func (s *System) Measure(path string) *walk.Node {
	nodes := walk.Walk(context.Background(), []string{path}, walk.Options{MinKeep: 1 << 20}, nil)
	if len(nodes) == 0 {
		return nil
	}
	return nodes[0]
}

// Volume is the home volume's space.
func (s *System) Volume() system.Volume {
	v, _ := system.VolumeOf(s.Home)
	return v
}

// Procs lists running processes.
func (s *System) Procs(ctx context.Context) []procs.Proc {
	ps, _ := procs.List(ctx, procs.Exec)
	return ps
}

// Run opens the view.
func Run(ctx context.Context, cfg Config) error {
	if cfg.Backend == nil {
		cfg.Backend = NewSystem(cfg.Home)
	}
	_, err := tea.NewProgram(newModel(ctx, cfg), tea.WithContext(ctx)).Run()
	return err
}

type screen int

const (
	screenScan screen = iota
	screenList
	screenDetail
)

type (
	progressMsg struct{}
	scanDoneMsg struct {
		rep   *owner.Report
		base  history.Snapshot
		has   bool
		skip  map[string]bool // see history.Mismatched
		delta int64           // history.TotalDelta
	}
	procsMsg     struct{ procs []procs.Proc }
	inspectedMsg struct {
		path string
		rpt  inspect.Report
	}
	reclaimedMsg struct {
		res     reclaim.Result
		trimmed *reclaim.Result
		node    *walk.Node // the item measured again; nil when it is gone
	}
)

// row is one line of the list: a kind heading or an item.
type row struct {
	kind owner.Kind
	item *owner.Item
}

// choice is one reclaim action offered on the detail screen. For a project
// the actions belong to its build outputs.
type choice struct {
	item   *owner.Item
	action owner.Action
}

type model struct {
	ctx context.Context
	cfg Config
	b   Backend

	screen  screen
	prog    *walk.Progress
	started time.Time
	rep     *owner.Report
	base    history.Snapshot
	hasBase bool
	skip    map[string]bool // items whose change is a permission, not the disk
	baseAdj int64           // the part of the total's change those items make up
	vol     system.Volume
	procsOf map[*owner.Item][]procs.Proc

	rows   []row
	cursor int
	offset int
	filter string
	typing bool

	reports    map[string]inspect.Report
	inspecting map[string]bool

	detail  *owner.Item
	choices []choice
	pick    int
	confirm bool
	running bool
	result  *reclaimedMsg
	freed   int64 // this session

	width, height int
	st            styles
	spin          spinner.Model
}

func newModel(ctx context.Context, cfg Config) model {
	return model{
		ctx: ctx, cfg: cfg, b: cfg.Backend,
		prog:       &walk.Progress{},
		started:    time.Now(),
		reports:    map[string]inspect.Report{},
		inspecting: map[string]bool{},
		st:         newStyles(true),
		spin:       spinner.New(spinner.WithSpinner(spinner.MiniDot)),
		width:      100, height: 30,
	}
}

func (m model) Init() tea.Cmd {
	return tea.Batch(m.scanCmd(), tea.RequestBackgroundColor, m.spin.Tick, progressTick())
}

func progressTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(time.Time) tea.Msg { return progressMsg{} })
}

// scanCmd scans into m.prog. Callers reset the progress first (see rescan),
// since Init runs on a copy of the model.
func (m model) scanCmd() tea.Cmd {
	prog, b, cfg, ctx := m.prog, m.b, m.cfg, m.ctx
	return func() tea.Msg {
		rep := b.Scan(ctx, prog)
		now := time.Now()
		snap := scan.Snapshot(rep, now)
		past, _ := cfg.Store.Load()
		base, has := history.Baseline(past, now, 24*time.Hour)
		if !cfg.NoSave && ctx.Err() == nil && cfg.Store.Path != "" {
			_ = cfg.Store.Add(snap, 400)
		}
		return scanDoneMsg{rep: rep, base: base, has: has, skip: history.Mismatched(base, snap), delta: history.TotalDelta(base, snap)}
	}
}

func (m model) inspectCmd(it *owner.Item) tea.Cmd {
	b, ctx := m.b, m.ctx
	cp := *it // the view may update sizes while this runs
	return func() tea.Msg { return inspectedMsg{path: cp.Path, rpt: b.Inspect(ctx, &cp)} }
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.scrollIntoView()
		return m, nil
	case tea.BackgroundColorMsg:
		m.st = newStyles(msg.IsDark())
		return m, nil
	case spinner.TickMsg:
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case progressMsg:
		if m.screen == screenScan {
			return m, progressTick()
		}
		return m, nil
	case scanDoneMsg:
		m.rep, m.base, m.hasBase = msg.rep, msg.base, msg.has
		m.skip, m.baseAdj = msg.skip, (msg.rep.Total-msg.base.Total)-msg.delta
		m.vol = m.b.Volume()
		m.screen = screenList
		m.rebuild()
		// Look inside every VM right away: trapped space is the one thing
		// a size listing cannot show, so it should not wait for a click.
		var cmds []tea.Cmd
		for _, it := range m.rep.Items {
			if inspect.Has(it.Owner.Inspect) && !m.inspecting[it.Path] {
				m.inspecting[it.Path] = true
				cmds = append(cmds, m.inspectCmd(it))
			}
		}
		b, ctx := m.b, m.ctx
		cmds = append(cmds, func() tea.Msg { return procsMsg{b.Procs(ctx)} })
		return m, tea.Batch(cmds...)
	case procsMsg:
		if m.rep != nil {
			m.procsOf = procs.Match(m.rep.Items, msg.procs, m.cfg.Home)
		}
		return m, nil
	case inspectedMsg:
		m.reports[msg.path] = msg.rpt
		delete(m.inspecting, msg.path)
		if m.detail != nil && m.detail.Path == msg.path {
			m.choices = choicesFor(m.detail, m.reports)
		}
		return m, nil
	case reclaimedMsg:
		m.running = false
		m.result = &msg
		freed := msg.res.Freed
		if msg.trimmed != nil {
			freed += msg.trimmed.Freed
		}
		if freed > 0 {
			m.freed += freed
		}
		m.applyReclaim(msg)
		m.vol = m.b.Volume()
		// The inside of a VM changed; look again.
		if m.detail != nil && inspect.Has(m.detail.Owner.Inspect) {
			m.inspecting[m.detail.Path] = true
			return m, m.inspectCmd(m.detail)
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.key(msg)
	}
	return m, nil
}

func (m model) key(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.screen {
	case screenScan:
		if s == "q" {
			return m, tea.Quit
		}
		return m, nil
	case screenDetail:
		return m.detailKey(s)
	}
	if m.typing {
		switch s {
		case "enter", "esc":
			m.typing = false
			if s == "esc" {
				m.filter = ""
			}
		case "backspace":
			if m.filter != "" {
				r := []rune(m.filter)
				m.filter = string(r[:len(r)-1])
			}
		default:
			if t := k.Text; t != "" {
				m.filter += t
			}
		}
		m.rebuild()
		return m, nil
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "esc":
		if m.filter != "" {
			m.filter = ""
			m.rebuild()
		}
	case "up", "k":
		m.move(-1)
	case "down", "j":
		m.move(1)
	case "pgup", "ctrl+b":
		m.move(-m.listHeight())
	case "pgdown", "ctrl+f", "space":
		m.move(m.listHeight())
	case "home", "g":
		m.cursor = 0
		m.move(0)
	case "end", "G":
		m.cursor = len(m.rows) - 1
		m.move(0)
	case "/":
		m.typing = true
	case "r":
		m.reports = map[string]inspect.Report{}
		m.screen, m.started, m.prog = screenScan, time.Now(), &walk.Progress{}
		return m, tea.Batch(m.scanCmd(), progressTick())
	case "enter", "l", "right", "x":
		if it := m.current(); it != nil {
			return m.open(it)
		}
	}
	return m, nil
}

func (m model) open(it *owner.Item) (tea.Model, tea.Cmd) {
	m.detail = it
	m.screen = screenDetail
	m.pick, m.confirm, m.result = 0, false, nil
	m.choices = choicesFor(it, m.reports)
	if _, ok := m.reports[it.Path]; !ok && !m.inspecting[it.Path] {
		m.inspecting[it.Path] = true
		return m, m.inspectCmd(it)
	}
	return m, nil
}

func (m model) detailKey(s string) (tea.Model, tea.Cmd) {
	if m.running {
		return m, nil
	}
	if m.confirm {
		m.confirm = false
		if s != "y" || m.pick >= len(m.choices) {
			return m, nil
		}
		c := m.choices[m.pick]
		m.running = true
		m.result = nil
		return m, tea.Batch(m.spin.Tick, m.reclaimCmd(c))
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "esc", "h", "left", "backspace":
		m.screen = screenList
		m.detail = nil
		m.result = nil
	case "up", "k":
		if m.pick > 0 {
			m.pick--
		}
	case "down", "j":
		if m.pick < len(m.choices)-1 {
			m.pick++
		}
	case "enter", "x":
		if len(m.choices) > 0 {
			m.confirm = true
		}
	default:
		if len(s) == 1 && s[0] >= '1' && s[0] <= '9' {
			if i := int(s[0] - '1'); i < len(m.choices) {
				m.pick = i
				m.confirm = true
			}
		}
	}
	return m, nil
}

func (m model) reclaimCmd(c choice) tea.Cmd {
	b, ctx, ledger := m.b, m.ctx, m.cfg.Ledger
	rpt := m.reports[c.item.Path]
	return func() tea.Msg {
		res := b.Reclaim(ctx, c.item, c.action)
		_ = ledger.Append(res)
		out := reclaimedMsg{res: res}
		// Pruning inside a VM frees blocks in the guest only. Hand them back
		// to macOS right away, or the "freed" number would be zero and the
		// user would be told nothing happened.
		if c.action.Inside && res.Error == "" && rpt.Trim != nil {
			t := b.Reclaim(ctx, c.item, *rpt.Trim)
			_ = ledger.Append(t)
			out.trimmed = &t
		}
		out.node = b.Measure(c.item.Path)
		return out
	}
}

// applyReclaim updates sizes after an action, without a full rescan.
func (m *model) applyReclaim(r reclaimedMsg) {
	var it *owner.Item
	for _, c := range m.choices {
		if c.item.Path == r.res.Path {
			it = c.item
		}
	}
	if it == nil {
		return
	}
	if it.Node == nil {
		// Not a directory chonk walked (a summed bucket): measuring its path
		// again would count something else.
		return
	}
	gone := r.node == nil
	var size int64
	if !gone {
		size = r.node.Size
		// Keep the node's place in the tree, so paths still resolve.
		if it.Node != nil {
			r.node.Name, r.node.Parent = it.Node.Name, it.Node.Parent
		}
		it.Node, it.Logical, it.Files = r.node, r.node.Logical, r.node.Files
	}
	delta := size - it.Size
	it.Size = size
	if p := it.Project; p != nil {
		p.Size += delta
		if gone {
			kept := p.Parts[:0]
			for _, x := range p.Parts {
				if x != it {
					kept = append(kept, x)
				}
			}
			p.Parts = kept
		}
	}
	m.rep.Total += delta
	if gone && it.Project == nil {
		kept := m.rep.Items[:0]
		for _, x := range m.rep.Items {
			if x != it {
				kept = append(kept, x)
			}
		}
		m.rep.Items = kept
	}
	if m.detail != nil {
		m.choices = choicesFor(m.detail, m.reports)
		if m.pick >= len(m.choices) {
			m.pick = max(0, len(m.choices)-1)
		}
	}
	m.rebuild()
}

// choicesFor lists the actions for it: its own, and for a project those of
// each build output inside it.
func choicesFor(it *owner.Item, _ map[string]inspect.Report) []choice {
	var out []choice
	for _, a := range it.Owner.Actions {
		out = append(out, choice{it, a})
	}
	for _, p := range it.Parts {
		for _, a := range p.Owner.Actions {
			out = append(out, choice{p, a})
		}
	}
	return out
}

func (m *model) rebuild() {
	if m.rep == nil {
		return
	}
	var cur *owner.Item
	if m.cursor < len(m.rows) {
		cur = m.rows[m.cursor].item
	}
	f := strings.ToLower(m.filter)
	m.rows = m.rows[:0]
	for _, k := range m.rep.KindsBySize() {
		items := m.rep.ByKind(k)
		var keep []*owner.Item
		for _, it := range items {
			if it.Size < 1<<20 && !it.SizeUnknown && (it.Node == nil || !it.Node.Denied) {
				continue
			}
			if f != "" && !strings.Contains(strings.ToLower(it.Name()+" "+it.Path), f) {
				continue
			}
			keep = append(keep, it)
		}
		if len(keep) == 0 {
			continue
		}
		m.rows = append(m.rows, row{kind: k})
		for _, it := range keep {
			m.rows = append(m.rows, row{kind: k, item: it})
		}
	}
	m.cursor, m.offset = 0, 0
	for i, r := range m.rows {
		if r.item != nil && r.item == cur {
			m.cursor = i
		}
	}
	m.move(0)
}

func (m *model) current() *owner.Item {
	if m.cursor < len(m.rows) {
		return m.rows[m.cursor].item
	}
	return nil
}

// move steps the cursor, skipping headings.
func (m *model) move(d int) {
	if len(m.rows) == 0 {
		m.cursor = 0
		return
	}
	dir := 1
	if d < 0 {
		dir = -1
	}
	m.cursor = min(max(m.cursor+d, 0), len(m.rows)-1)
	for m.rows[m.cursor].item == nil {
		next := m.cursor + dir
		if next < 0 || next >= len(m.rows) {
			dir = -dir
			next = m.cursor + dir
			if next < 0 || next >= len(m.rows) {
				return
			}
		}
		m.cursor = next
	}
	m.scrollIntoView()
}

func (m *model) scrollIntoView() {
	h := m.listHeight()
	if m.cursor < m.offset {
		m.offset = m.cursor
		if m.offset > 0 && m.rows[m.offset-1].item == nil {
			m.offset-- // keep the heading in view
		}
	}
	if m.cursor >= m.offset+h {
		m.offset = m.cursor - h + 1
	}
	m.offset = max(0, min(m.offset, len(m.rows)-h))
}
