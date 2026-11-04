// Package version reports the build version.
package version

import "runtime/debug"

// Set by -ldflags at release time.
var (
	Version   = ""
	GitCommit = ""
)

// String returns the release version, or the module version when built with
// go install, or "dev".
func String() string {
	if Version != "" {
		return Version
	}
	if bi, ok := debug.ReadBuildInfo(); ok && bi.Main.Version != "" && bi.Main.Version != "(devel)" {
		return bi.Main.Version
	}
	return "dev"
}
