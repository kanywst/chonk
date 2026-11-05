package schedule

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestPlist(t *testing.T) {
	a := Agent{Home: "/h", State: "/h/state & co", Exe: "/opt/homebrew/bin/chonk", Hour: 13}
	p := a.Plist()
	for _, want := range []string{
		"<string>" + Label + "</string>",
		"<string>/opt/homebrew/bin/chonk</string>\n\t\t<string>snapshot</string>",
		"<key>Hour</key>\n\t\t<integer>13</integer>",
		"<string>/h/state &amp; co/snapshot.log</string>",
		"<key>LowPriorityIO</key>",
	} {
		if !strings.Contains(p, want) {
			t.Errorf("plist lacks %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, "EnvironmentVariables") {
		t.Error("no XDG_STATE_HOME was given, so none should be set")
	}
	if plutil, err := exec.LookPath("plutil"); err == nil {
		f := filepath.Join(t.TempDir(), "p.plist")
		a.XDG = "/x"
		if err := os.WriteFile(f, []byte(a.Plist()), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command(plutil, "-lint", f).CombinedOutput(); err != nil {
			t.Errorf("plutil: %v: %s", err, out)
		}
	}
	a.XDG = "/x"
	if !strings.Contains(a.Plist(), "<key>XDG_STATE_HOME</key>\n\t\t<string>/x</string>") {
		t.Error("XDG_STATE_HOME is not passed to the job")
	}
}

func TestCheckExe(t *testing.T) {
	for exe, ok := range map[string]bool{
		"/opt/homebrew/bin/chonk": true,
		"chonk":                   false,
		"/var/folders/x/T/go-build123/b001/exe/ch": false,
	} {
		if err := CheckExe(exe); (err == nil) != ok {
			t.Errorf("CheckExe(%q) = %v", exe, err)
		}
	}
}

func TestInstallRemove(t *testing.T) {
	home := t.TempDir()
	var calls []string
	a := Agent{
		Home: home, State: filepath.Join(home, "state"), Exe: "/usr/local/bin/chonk", Hour: 12, UID: 501,
		Run: func(_ context.Context, name string, args ...string) ([]byte, error) {
			calls = append(calls, name+" "+strings.Join(args, " "))
			if args[0] == "print" {
				return nil, errors.New("not loaded")
			}
			return nil, nil
		},
	}
	ctx := context.Background()
	if runtime.GOOS == "darwin" {
		rel := a
		rel.XDG = "state"
		if err := rel.Install(ctx); err == nil || !strings.Contains(err.Error(), "not an absolute path") {
			t.Errorf("relative XDG_STATE_HOME: %v", err)
		}
	}
	if runtime.GOOS != "darwin" {
		if err := a.Install(ctx); !errors.Is(err, ErrUnsupported) {
			t.Fatalf("Install off macOS = %v", err)
		}
		return
	}
	if err := a.Install(ctx); err != nil {
		t.Fatal(err)
	}
	if b, err := os.ReadFile(a.Path()); err != nil || !strings.Contains(string(b), "snapshot") {
		t.Fatalf("plist not written: %v", err)
	}
	want := []string{
		"launchctl bootout gui/501/" + Label,
		"launchctl bootstrap gui/501 " + a.Path(),
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Errorf("calls = %q", calls)
	}
	if inst, loaded := a.Status(ctx); !inst || loaded {
		t.Errorf("status = %v %v", inst, loaded)
	}
	if err := a.Remove(ctx); err != nil {
		t.Fatal(err)
	}
	if err := a.Remove(ctx); err != nil {
		t.Errorf("second remove: %v", err)
	}
	if inst, _ := a.Status(ctx); inst {
		t.Error("plist still there")
	}
}
