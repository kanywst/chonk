package format

import (
	"testing"
	"time"
)

func TestBytes(t *testing.T) {
	for n, want := range map[int64]string{
		0:                  "0B",
		512:                "512B",
		2048:               "2K",
		5 << 20:            "5M",
		3 << 29:            "1.5G",
		81 << 30:           "81.0G",
		3 << 39:            "1.5T",
		-(300 << 20):       "-300M",
		(1 << 40) * 2:      "2.0T",
		(1 << 30) - 1<<20:  "1023M",
		(1 << 20) - 1<<10:  "1023K",
		(1 << 10) - 1:      "1023B",
		-(1 << 30):         "-1.0G",
		-(1 << 10):         "-1K",
		-(1):               "-1B",
		(77 << 30) + 1<<28: "77.2G",
	} {
		if got := Bytes(n); got != want {
			t.Errorf("Bytes(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestDelta(t *testing.T) {
	for n, want := range map[int64]string{
		0:            "",
		1 << 19:      "",
		-(1 << 19):   "",
		2 << 30:      "+2.0G",
		-(300 << 20): "-300M",
	} {
		if got := Delta(n); got != want {
			t.Errorf("Delta(%d) = %q, want %q", n, got, want)
		}
	}
}

func TestAge(t *testing.T) {
	day := 24 * time.Hour
	for d, want := range map[time.Duration]string{
		30 * time.Second: "30s",
		5 * time.Minute:  "5m",
		3 * time.Hour:    "3h",
		3 * day:          "3d",
		20 * day:         "2w",
		90 * day:         "3mo",
		800 * day:        "2y",
	} {
		if got := Age(d); got != want {
			t.Errorf("Age(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestPercent(t *testing.T) {
	if got := Percent(1, 0); got != "" {
		t.Errorf("Percent(1, 0) = %q", got)
	}
	if got := Percent(77, 81); got != "95%" {
		t.Errorf("Percent(77, 81) = %q", got)
	}
}
