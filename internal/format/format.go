// Package format renders sizes and ages for people.
package format

import (
	"fmt"
	"time"
)

// Bytes renders n with a binary unit: 512B, 12K, 340M, 3.4G, 1.2T.
func Bytes(n int64) string {
	const k = 1024
	neg := ""
	if n < 0 {
		neg, n = "-", -n
	}
	switch {
	case n < k:
		return fmt.Sprintf("%s%dB", neg, n)
	case n < k*k:
		return fmt.Sprintf("%s%dK", neg, n/k)
	case n < k*k*k:
		return fmt.Sprintf("%s%dM", neg, n/(k*k))
	case n < k*k*k*k:
		return fmt.Sprintf("%s%.1fG", neg, float64(n)/(k*k*k))
	default:
		return fmt.Sprintf("%s%.1fT", neg, float64(n)/(k*k*k*k))
	}
}

// Delta renders a change in size with its sign: +2.1G, -300M. Changes under
// 1 MiB are rendered as empty, since they are noise next to the sizes chonk
// shows.
func Delta(n int64) string {
	if n > -1<<20 && n < 1<<20 {
		return ""
	}
	if n > 0 {
		return "+" + Bytes(n)
	}
	return Bytes(n)
}

// Age renders d as its largest unit: 45s, 12m, 5h, 3d, 6w, 14mo, 2y.
func Age(d time.Duration) string {
	const day = 24 * time.Hour
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < day:
		return fmt.Sprintf("%dh", int(d.Hours()))
	case d < 14*day:
		return fmt.Sprintf("%dd", int(d/day))
	case d < 60*day:
		return fmt.Sprintf("%dw", int(d/(7*day)))
	case d < 730*day:
		return fmt.Sprintf("%dmo", int(d/(30*day)))
	default:
		return fmt.Sprintf("%dy", int(d/(365*day)))
	}
}

// Percent renders part/whole as a whole percentage, or "" when whole is 0.
func Percent(part, whole int64) string {
	if whole <= 0 {
		return ""
	}
	return fmt.Sprintf("%d%%", part*100/whole)
}
