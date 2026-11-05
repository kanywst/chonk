// Package schedule installs a launchd agent that records a snapshot every
// day, so `chonk diff` has a baseline without anyone remembering to scan.
package schedule

import (
	"context"
	"errors"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
)

// Label names the launchd job.
const Label = "io.github.kanywst.chonk.snapshot"

// Runner runs a command and returns its output.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// Agent is the job for one user.
type Agent struct {
	Home  string // the user's home; the plist goes under ~/Library/LaunchAgents
	State string // chonk's state directory; the log goes here
	Exe   string // the chonk binary launchd runs
	Hour  int    // local hour of the daily run
	XDG   string // $XDG_STATE_HOME when it is set, so the job writes the same history
	UID   int
	Run   Runner
}

// Path is where the plist lives.
func (a Agent) Path() string {
	return filepath.Join(a.Home, "Library", "LaunchAgents", Label+".plist")
}

// Log is where the job's output goes.
func (a Agent) Log() string { return filepath.Join(a.State, "snapshot.log") }

func (a Agent) target() string { return "gui/" + strconv.Itoa(a.UID) }

// Plist is the launchd job: `chonk snapshot` once a day at Hour. launchd
// runs a calendar job that was missed while the Mac slept when it wakes.
// The scan runs as a background process so it yields disk I/O to you.
func (a Agent) Plist() string {
	x := html.EscapeString
	var env string
	if a.XDG != "" {
		env = "\t<key>EnvironmentVariables</key>\n\t<dict>\n\t\t<key>XDG_STATE_HOME</key>\n\t\t<string>" + x(a.XDG) + "</string>\n\t</dict>\n"
	}
	return `<?xml version="1.0" encoding="UTF-8"?>
<!DOCTYPE plist PUBLIC "-//Apple//DTD PLIST 1.0//EN" "http://www.apple.com/DTDs/PropertyList-1.0.dtd">
<plist version="1.0">
<dict>
	<key>Label</key>
	<string>` + Label + `</string>
	<key>ProgramArguments</key>
	<array>
		<string>` + x(a.Exe) + `</string>
		<string>snapshot</string>
	</array>
	<key>StartCalendarInterval</key>
	<dict>
		<key>Hour</key>
		<integer>` + strconv.Itoa(a.Hour) + `</integer>
		<key>Minute</key>
		<integer>0</integer>
	</dict>
	<key>ProcessType</key>
	<string>Background</string>
	<key>LowPriorityIO</key>
	<true/>
	<key>StandardOutPath</key>
	<string>` + x(a.Log()) + `</string>
	<key>StandardErrorPath</key>
	<string>` + x(a.Log()) + `</string>
` + env + `</dict>
</plist>
`
}

// ErrUnsupported is returned off macOS, which has no launchd.
var ErrUnsupported = errors.New("the daily snapshot uses launchd, which is macOS only; elsewhere run `chonk snapshot` from cron or a systemd timer")

// CheckExe refuses binaries that will not be there tomorrow, such as the
// temporary build `go run` makes.
func CheckExe(exe string) error {
	if !filepath.IsAbs(exe) {
		return fmt.Errorf("%s is not an absolute path", exe)
	}
	if strings.Contains(exe, string(filepath.Separator)+"go-build") {
		return fmt.Errorf("%s is a temporary `go run` build; install chonk first", exe)
	}
	return nil
}

// Install writes the plist and (re)loads it.
func (a Agent) Install(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}
	if err := CheckExe(a.Exe); err != nil {
		return err
	}
	// launchd runs the job from /, where a relative path means something else.
	for _, p := range []string{a.State, a.XDG} {
		if p != "" && !filepath.IsAbs(p) {
			return fmt.Errorf("%s is not an absolute path", p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(a.Path()), 0o700); err != nil {
		return err
	}
	if err := os.MkdirAll(a.State, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(a.Path(), []byte(a.Plist()), 0o644); err != nil { //nolint:gosec // launchd plists are world-readable
		return err
	}
	// A job that is already loaded keeps its old definition until it is
	// booted out, so reinstalling (a new binary path, a new hour) needs this.
	_, _ = a.Run(ctx, "launchctl", "bootout", a.target()+"/"+Label)
	if out, err := a.Run(ctx, "launchctl", "bootstrap", a.target(), a.Path()); err != nil {
		return fmt.Errorf("launchctl bootstrap: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

// Remove unloads the job and deletes the plist. Removing what is not
// installed is not an error.
func (a Agent) Remove(ctx context.Context) error {
	if runtime.GOOS != "darwin" {
		return ErrUnsupported
	}
	_, _ = a.Run(ctx, "launchctl", "bootout", a.target()+"/"+Label)
	if err := os.Remove(a.Path()); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Status says whether the plist exists and whether launchd has it loaded.
func (a Agent) Status(ctx context.Context) (installed, loaded bool) {
	if _, err := os.Stat(a.Path()); err == nil {
		installed = true
	}
	if runtime.GOOS == "darwin" {
		_, err := a.Run(ctx, "launchctl", "print", a.target()+"/"+Label)
		loaded = err == nil
	}
	return installed, loaded
}
