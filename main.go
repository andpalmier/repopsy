// Command repopsy explodes a git repository, extracting each commit into its own
// folder alongside a forensic record of the commit and of the extraction itself.
package main

import (
	"os"
	"runtime/debug"

	"github.com/andpalmier/repopsy/v2/cmd"
)

// Build information, set at link time via -ldflags. The placeholder values are
// what a build without them produces, and init fills in what it can from the
// data the toolchain embeds by itself.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// init recovers the build information that -ldflags did not supply.
//
// "go install module@version" applies no -ldflags at all, so a binary installed
// the way the README describes used to report itself as "dev" with no way to
// tell which release it came from. The toolchain does record the module version
// in that case, and records the revision and commit time instead when building
// inside a checkout, so between the two the binary can always say where it came
// from. Values that -ldflags did set are left alone.
func init() {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return
	}

	// "(devel)" is what a build from a working tree reports, which says less
	// than the revision below.
	if version == "dev" && info.Main.Version != "" && info.Main.Version != "(devel)" {
		version = info.Main.Version
	}

	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			if commit == "none" && len(s.Value) >= shortHashLength {
				commit = s.Value[:shortHashLength]
			}
		case "vcs.time":
			if date == "unknown" {
				date = s.Value
			}
		}
	}
}

// shortHashLength matches the abbreviation git uses by default, so a commit
// recovered from the build info reads the same as one passed by -ldflags.
const shortHashLength = 7

func main() {
	// Pass version info to command package and run
	os.Exit(cmd.Execute(version, commit, date))
}
