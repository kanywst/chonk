package tui

import (
	"image/color"
	"os"

	"charm.land/lipgloss/v2"

	"github.com/kanywst/chonk/internal/owner"
)

// palette: warm and a little plump. Each kind has its own hue so the
// stacked bar at the top reads as a map, and status never relies on color
// alone (safety is always spelled out).
type palette struct {
	fg, muted, faint, bg, cursorBg color.Color
	accent, good, warn, bad        color.Color
	kinds                          [owner.KindUnclaimed + 1]color.Color
}

func newPalette(dark bool) palette {
	ld := lipgloss.LightDark(dark)
	c := lipgloss.Color
	return palette{
		fg:       ld(c("#24211E"), c("#EDE6DD")),
		muted:    ld(c("#6B635B"), c("#A39A90")),
		faint:    ld(c("#B7AEA4"), c("#57504A")),
		bg:       ld(c("#FFFFFF"), c("#161412")),
		cursorBg: ld(c("#FBEFE3"), c("#2A221B")),
		accent:   ld(c("#C2410C"), c("#FDBA74")),
		good:     ld(c("#15803D"), c("#86EFAC")),
		warn:     ld(c("#B45309"), c("#FCD34D")),
		bad:      ld(c("#B91C1C"), c("#FCA5A5")),
		kinds: [...]color.Color{
			owner.KindVM:        ld(c("#C2410C"), c("#FB923C")),
			owner.KindToolchain: ld(c("#7C3AED"), c("#C4B5FD")),
			owner.KindCache:     ld(c("#0E7490"), c("#67E8F9")),
			owner.KindBuild:     ld(c("#0F766E"), c("#5EEAD4")),
			owner.KindProject:   ld(c("#1D4ED8"), c("#93C5FD")),
			owner.KindApp:       ld(c("#BE185D"), c("#F9A8D4")),
			owner.KindUser:      ld(c("#4D7C0F"), c("#BEF264")),
			owner.KindTrash:     ld(c("#78716C"), c("#D6D3D1")),
			owner.KindSystem:    ld(c("#57534E"), c("#A8A29E")),
			owner.KindUnclaimed: ld(c("#A16207"), c("#FDE68A")),
		},
	}
}

type styles struct {
	p                                  palette
	app, badge, title, text, bold, dim lipgloss.Style
	faint, cursor, heading, accent     lipgloss.Style
	good, warn, bad, modal, key        lipgloss.Style
	noColor                            bool
}

func newStyles(dark bool) styles {
	p := newPalette(dark)
	s := lipgloss.NewStyle()
	return styles{
		p:       p,
		noColor: os.Getenv("NO_COLOR") != "",
		app:     s.Padding(1, 2),
		badge:   s.Foreground(p.bg).Background(p.accent).Bold(true).Padding(0, 1),
		title:   s.Foreground(p.fg).Bold(true),
		text:    s.Foreground(p.fg),
		bold:    s.Foreground(p.fg).Bold(true),
		dim:     s.Foreground(p.muted),
		faint:   s.Foreground(p.faint),
		cursor:  s.Background(p.cursorBg),
		heading: s.Foreground(p.muted).Bold(true),
		accent:  s.Foreground(p.accent).Bold(true),
		good:    s.Foreground(p.good).Bold(true),
		warn:    s.Foreground(p.warn),
		bad:     s.Foreground(p.bad).Bold(true),
		modal:   s.Border(lipgloss.RoundedBorder()).BorderForeground(p.accent).Padding(1, 3),
		key:     s.Foreground(p.fg).Bold(true),
	}
}

func (s styles) kind(k owner.Kind) lipgloss.Style {
	return lipgloss.NewStyle().Foreground(s.p.kinds[k])
}

func (s styles) safety(v owner.Safety) lipgloss.Style {
	switch v {
	case owner.Safe:
		return s.good
	case owner.Careful:
		return s.warn
	default:
		return s.bad
	}
}
