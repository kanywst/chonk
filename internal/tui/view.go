package tui

import (
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/kanywst/chonk/internal/format"
	"github.com/kanywst/chonk/internal/owner"
	"github.com/kanywst/chonk/internal/procs"
)

func (m model) View() tea.View {
	var body string
	switch m.screen {
	case screenScan:
		body = m.scanView()
	case screenDetail:
		body = m.detailView()
	default:
		body = m.listView()
	}
	v := tea.NewView(m.st.app.Render(body))
	v.AltScreen = true
	return v
}

func (m model) inner() int { return max(40, m.width-4) }

// listHeight is how many rows of the list fit under the header.
func (m model) listHeight() int { return max(3, m.height-12) }

func (m model) scanView() string {
	st := m.st
	el := time.Since(m.started).Round(time.Second)
	return lipgloss.JoinVertical(lipgloss.Left,
		st.badge.Render("chonk")+"  "+st.dim.Render("weighing your disk"),
		"",
		m.spin.View()+" "+st.bold.Render(format.Bytes(m.prog.Bytes.Load()))+st.dim.Render(fmt.Sprintf(" in %d files, %d directories, %s", m.prog.Files.Load(), m.prog.Dirs.Load(), el)),
		"",
		st.faint.Render("Sizes are space on disk, not file lengths, so sparse VM images count for what they hold."),
		st.faint.Render("q quit"),
	)
}

func (m model) header() string {
	st := m.st
	w := m.inner()
	parts := []string{st.badge.Render("chonk")}
	parts = append(parts, st.bold.Render(format.Bytes(m.rep.Total))+st.dim.Render(" scanned"))
	if m.vol.Total > 0 {
		parts = append(parts, st.bold.Render(format.Bytes(m.vol.Free))+st.dim.Render(" free of "+format.Bytes(m.vol.Total)))
		// What the volume holds that no walked directory explains: swap,
		// logs, other users, /Library, /private/var.
		if out := m.vol.Used - m.rep.Total; out > 1<<30 {
			parts = append(parts, st.text.Render(format.Bytes(out))+st.dim.Render(" outside the scan"))
		}
	}
	if m.hasBase {
		d := format.Delta(m.rep.Total - m.base.Total)
		if d == "" {
			d = "no change"
		}
		parts = append(parts, st.text.Render(d)+st.dim.Render(" since "+format.Age(time.Since(m.base.Time))+" ago"))
	}
	if m.freed > 0 {
		parts = append(parts, st.good.Render("freed "+format.Bytes(m.freed)))
	}
	lines := []string{strings.Join(parts, st.faint.Render("  ·  ")), "", m.kindBar(w)}
	lines = append(lines, m.legend(w))
	if t, it := m.trapped(); t > 0 {
		lines = append(lines, "", ansi.Truncate(st.accent.Render("▲ "+format.Bytes(t)+" trapped")+st.text.Render(" in "+it.Name()+": freed inside the VM, still held on this Mac"), w, "…"))
	}
	return strings.Join(lines, "\n")
}

// trapped is the biggest trapped space any inspector found.
func (m model) trapped() (int64, *owner.Item) {
	var best int64
	var at *owner.Item
	for _, it := range m.rep.Items {
		if r, ok := m.reports[it.Path]; ok && r.Trapped > best {
			best, at = r.Trapped, it
		}
	}
	return best, at
}

// kindBar is the whole scan as one stacked bar, a block of color per kind.
func (m model) kindBar(w int) string {
	if m.rep.Total <= 0 {
		return ""
	}
	var b strings.Builder
	used := 0
	kinds := m.rep.KindsBySize()
	for i, k := range kinds {
		n := int(m.rep.KindTotal(k) * int64(w) / m.rep.Total)
		if i == len(kinds)-1 {
			n = w - used
		}
		if n <= 0 {
			continue
		}
		used += n
		glyph := "█"
		if m.st.noColor {
			glyph = string("#=+%*o.:-~"[k])
		}
		b.WriteString(m.st.kind(k).Render(strings.Repeat(glyph, n)))
	}
	return b.String()
}

func (m model) legend(w int) string {
	var parts []string
	for _, k := range m.rep.KindsBySize() {
		t := m.rep.KindTotal(k)
		if t < 1<<30 {
			continue
		}
		parts = append(parts, m.st.kind(k).Render("■")+" "+m.st.dim.Render(k.String()+" ")+m.st.text.Render(format.Bytes(t)))
	}
	return ansi.Truncate(strings.Join(parts, "   "), w, "…")
}

func (m model) listView() string {
	st := m.st
	w := m.inner()
	var b strings.Builder
	b.WriteString(m.header())
	b.WriteString("\n\n")

	h := m.listHeight()
	biggest := int64(1)
	for _, r := range m.rows {
		if r.item != nil && r.item.Size > biggest {
			biggest = r.item.Size
		}
	}
	end := min(len(m.rows), m.offset+h)
	for i := m.offset; i < end; i++ {
		r := m.rows[i]
		if r.item == nil {
			title := st.kind(r.kind).Render("■ ") + st.heading.Render(r.kind.String())
			tot := st.dim.Render(format.Bytes(m.rep.KindTotal(r.kind)))
			b.WriteString(title + strings.Repeat(" ", max(1, w-lipgloss.Width(title)-lipgloss.Width(tot))) + tot)
		} else {
			b.WriteString(m.itemLine(r.item, i == m.cursor, biggest, w))
		}
		b.WriteByte('\n')
	}
	for i := end - m.offset; i < h; i++ {
		b.WriteByte('\n')
	}
	if len(m.rows) == 0 {
		b.WriteString(st.dim.Render("nothing matches " + m.filter))
	}
	b.WriteByte('\n')
	if m.typing || m.filter != "" {
		b.WriteString(st.accent.Render("/") + st.text.Render(m.filter))
		if m.typing {
			b.WriteString(st.faint.Render("▏  enter keep · esc clear"))
		}
		b.WriteString("\n")
	}
	b.WriteString(m.keys("↑↓", "move", "enter", "why & reclaim", "/", "filter", "r", "rescan", "q", "quit"))
	return b.String()
}

func (m model) keys(pairs ...string) string {
	var parts []string
	for i := 0; i+1 < len(pairs); i += 2 {
		parts = append(parts, m.st.key.Render(pairs[i])+" "+m.st.dim.Render(pairs[i+1]))
	}
	return strings.Join(parts, m.st.faint.Render("  ·  "))
}

func (m model) itemLine(it *owner.Item, cur bool, biggest int64, w int) string {
	st := m.st
	const barW = 14
	size := fmt.Sprintf("%7s", format.Bytes(it.Size))
	if it.SizeUnknown {
		size = fmt.Sprintf("%7s", "?")
	} else if it.Node != nil && it.Node.Denied {
		size = fmt.Sprintf("%7s", "locked")
	}
	bar := m.bar(it.Owner.Kind, it.Size, biggest, barW)

	var tags []string
	if m.hasBase {
		if d := format.Delta(it.Size - m.base.Items[owner.Tilde(it.Path)]); d != "" {
			if strings.HasPrefix(d, "+") {
				tags = append(tags, st.warn.Render(d))
			} else {
				tags = append(tags, st.good.Render(d))
			}
		}
	}
	if r, ok := m.reports[it.Path]; ok && r.Trapped > 0 {
		tags = append(tags, st.accent.Render(format.Bytes(r.Trapped)+" trapped"))
	} else if m.inspecting[it.Path] {
		tags = append(tags, st.faint.Render("looking inside…"))
	}
	if rc := it.Reclaimable(); rc > 0 {
		tags = append(tags, st.dim.Render(format.Bytes(rc)+" build output"))
	}
	if ps := m.procsOf[it]; len(ps) > 0 {
		tags = append(tags, st.warn.Render("▶ "+format.Bytes(procs.Total(ps))+" running"))
	}
	if !it.Touched.IsZero() {
		tags = append(tags, st.faint.Render(format.Age(time.Since(it.Touched))))
	}
	right := strings.Join(tags, "  ")

	mark := "  "
	if cur {
		mark = st.accent.Render("▌ ")
	}
	left := mark + st.bold.Render(size) + " " + bar + " "
	nameW := max(8, w-lipgloss.Width(left)-lipgloss.Width(right)-2)
	name := ansi.Truncate(it.Name(), nameW, "…")
	line := left + st.text.Render(name) + strings.Repeat(" ", max(2, w-lipgloss.Width(left)-lipgloss.Width(name)-lipgloss.Width(right))) + right
	if cur {
		return st.cursor.Render(ansi.Truncate(line, w, ""))
	}
	return ansi.Truncate(line, w, "")
}

// bar draws size/biggest in w cells, with eighth-cell steps so small items
// still show up next to an 80G VM.
func (m model) bar(k owner.Kind, size, biggest int64, w int) string {
	eighths := int(size * int64(w) * 8 / max(biggest, 1))
	if eighths == 0 && size > 0 {
		eighths = 1
	}
	full, part := eighths/8, eighths%8
	s := strings.Repeat("█", full)
	if part > 0 {
		s += string([]rune(" ▏▎▍▌▋▊▉")[part])
	}
	n := lipgloss.Width(s)
	return m.st.kind(k).Render(s) + m.st.faint.Render(strings.Repeat("·", max(0, w-n)))
}

func (m model) detailView() string {
	st := m.st
	it := m.detail
	w := m.inner()
	wrap := func(s string) string { return ansi.Wrap(s, w, " ") }
	var lines []string

	title := st.kind(it.Owner.Kind).Render("■ ") + st.title.Render(it.Name())
	sz := format.Bytes(it.Size)
	if it.SizeUnknown {
		sz = "size unknown"
	}
	right := st.bold.Render(sz) + st.dim.Render(" on disk  ") + st.safety(it.Owner.Safety).Render(it.Owner.Safety.String())
	lines = append(lines, title+strings.Repeat(" ", max(2, w-lipgloss.Width(title)-lipgloss.Width(right)))+right)
	sub := owner.Tilde(it.Path)
	if !it.Touched.IsZero() {
		sub += "  ·  last written " + format.Age(time.Since(it.Touched)) + " ago"
	}
	if it.Project != nil {
		sub += "  ·  in " + it.Project.Name()
	}
	lines = append(lines, st.dim.Render(ansi.Truncate(sub, w, "…")), "")
	lines = append(lines, st.text.Render(wrap(it.Owner.Why)))
	if it.Owner.Regen != "" {
		lines = append(lines, st.dim.Render(wrap("If removed: "+it.Owner.Regen)))
	}
	if it.Hotspot != "" {
		lines = append(lines, st.dim.Render(wrap("Most of it is in "+owner.Tilde(it.Hotspot))))
	}

	// What is inside, from the tool that owns it.
	if r, ok := m.reports[it.Path]; ok && (len(r.Lines) > 0 || r.Note != "") {
		lines = append(lines, "", st.heading.Render("Inside"))
		lw := 0
		for _, l := range r.Lines {
			lw = max(lw, lipgloss.Width(l.Label))
		}
		for _, l := range r.Lines {
			style := st.text
			if l.Label == "Trapped" {
				style = st.accent
			}
			lines = append(lines, "  "+st.dim.Render(fmt.Sprintf("%-*s", lw, l.Label))+"  "+style.Render(ansi.Truncate(l.Value, w-lw-4, "…")))
		}
		if r.Note != "" {
			lines = append(lines, "  "+st.warn.Render(ansi.Wrap(r.Note, w-2, " ")))
		}
	} else if m.inspecting[it.Path] {
		lines = append(lines, "", m.spin.View()+st.dim.Render(" asking the owner what is inside…"))
	}

	// Processes tied to it: a VM's host agent, a dev server in a project.
	if ps := m.procsOf[it]; len(ps) > 0 {
		lines = append(lines, "", st.heading.Render(fmt.Sprintf("Running (%s in memory)", format.Bytes(procs.Total(ps)))))
		for i, p := range ps {
			if i >= 4 {
				lines = append(lines, st.faint.Render(fmt.Sprintf("  and %d more", len(ps)-i)))
				break
			}
			lines = append(lines, fmt.Sprintf("  %s  %s %s", st.bold.Render(fmt.Sprintf("%7s", format.Bytes(p.RSS))), st.text.Render(p.Name()), st.faint.Render(fmt.Sprintf("pid %d", p.PID))))
		}
	}

	// The biggest directories inside, so nothing stays a black box.
	if it.Node != nil && len(it.Node.Children) > 0 && len(it.Parts) == 0 {
		lines = append(lines, "", st.heading.Render("Biggest inside"))
		for i, c := range it.Node.Children {
			if i >= 5 || c.Size < 1<<20 {
				break
			}
			lines = append(lines, fmt.Sprintf("  %s  %s", st.bold.Render(fmt.Sprintf("%7s", format.Bytes(c.Size))), st.text.Render(ansi.Truncate(c.Name, w-12, "…"))))
		}
	}

	lines = append(lines, "", st.heading.Render("Reclaim"))
	if len(m.choices) == 0 {
		lines = append(lines, st.dim.Render(wrap("chonk knows no safe way to reclaim this for you. "+it.Owner.Regen)))
	}
	for i, c := range m.choices {
		a := c.action.Expand(c.item.Path)
		mark := "  "
		if i == m.pick {
			mark = st.accent.Render("▌ ")
		}
		label := a.Label
		if c.item != it {
			label = fmt.Sprintf("%s (%s): %s", owner.Tilde(c.item.Path), format.Bytes(c.item.Size), a.Label)
			if rel, ok := strings.CutPrefix(c.item.Path, it.Path+"/"); ok {
				label = fmt.Sprintf("%s (%s): %s", rel, format.Bytes(c.item.Size), a.Label)
			}
		}
		head := mark + st.key.Render(fmt.Sprintf("%d", i+1)) + " " + st.safety(a.Safety).Render(fmt.Sprintf("%-7s", a.Safety)) + " " + st.text.Render(ansi.Truncate(label, w-14, "…"))
		if i == m.pick {
			head = st.cursor.Render(head)
		}
		lines = append(lines, head)
		if i == m.pick {
			lines = append(lines, "             "+st.faint.Render(ansi.Truncate(a.Command(), w-13, "…")))
		}
	}

	if m.running {
		lines = append(lines, "", m.spin.View()+st.text.Render(" running…"))
	}
	if r := m.result; r != nil {
		lines = append(append(lines, ""), m.resultLines(r)...)
	}

	footer := m.keys("↑↓", "pick", "enter/1-9", "run", "esc", "back", "q", "quit")
	if m.confirm && m.pick < len(m.choices) {
		footer = m.confirmBox(m.choices[m.pick])
	}
	// Keep the footer (or the dialog) on screen: cut the body to fit, from
	// the top, so the reclaim list near the bottom stays visible.
	room := m.height - 3 - lipgloss.Height(footer)
	if room > 4 && len(lines) > room {
		lines = lines[len(lines)-room:]
	}
	return strings.Join(lines, "\n") + "\n\n" + footer
}

func (m model) resultLines(r *reclaimedMsg) []string {
	st := m.st
	var out []string
	one := func(res string, freed int64, errText string, trashed bool) {
		switch {
		case errText != "":
			out = append(out, st.bad.Render("✗ ")+st.text.Render(res+": "+errText))
		case trashed:
			out = append(out, st.good.Render("✓ ")+st.text.Render("Moved to the Trash. It still takes space until you empty the Trash."))
		case freed > 0:
			out = append(out, st.good.Render("✓ "+format.Bytes(freed)+" freed")+st.dim.Render(" (measured on the volume) by "+res))
		default:
			out = append(out, st.warn.Render("✓ ")+st.text.Render(res+" ran, but free space did not grow."))
		}
	}
	one(r.res.Action, r.res.Freed, r.res.Error, r.res.Trashed)
	if r.trimmed != nil {
		one(r.trimmed.Action, r.trimmed.Freed, r.trimmed.Error, false)
	}
	if r.res.Output != "" {
		last := r.res.Output
		if i := strings.LastIndexByte(last, '\n'); i >= 0 {
			last = last[i+1:]
		}
		out = append(out, st.faint.Render("  "+ansi.Truncate(last, m.inner()-2, "…")))
	}
	return out
}

func (m model) confirmBox(c choice) string {
	st := m.st
	a := c.action.Expand(c.item.Path)
	lines := []string{
		st.title.Render(a.Label),
		"",
		st.dim.Render("on   ") + st.text.Render(owner.Tilde(c.item.Path)+" ("+format.Bytes(c.item.Size)+")"),
		st.dim.Render("runs ") + st.text.Render(a.Command()),
		st.dim.Render("risk ") + st.safety(a.Safety).Render(a.Safety.String()),
	}
	if a.Note != "" {
		lines = append(lines, "", st.dim.Render(ansi.Wrap(a.Note, min(70, m.inner()-10), " ")))
	}
	src := c.item
	if src.Project != nil {
		src = src.Project
	}
	if ps := m.procsOf[src]; len(ps) > 0 && !a.Inside {
		lines = append(lines, "", st.warn.Render(ansi.Wrap(fmt.Sprintf("%d process(es) are running from here (%s). Removing files under them can break them.", len(ps), ps[0].Name()), min(70, m.inner()-10), " ")))
	}
	lines = append(lines, "", st.key.Render("y")+st.dim.Render(" run   ")+st.key.Render("any other key")+st.dim.Render(" cancel"))
	return st.modal.Render(strings.Join(lines, "\n"))
}
