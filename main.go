// Command repopsy explodes a git repository, extracting each commit into its own
// folder alongside a forensic record of the commit and of the extraction itself.
package main

import (
	"os"

	"github.com/andpalmier/repopsy/cmd"
)

// Build information, set at link time via -ldflags. The placeholder values are
// what a "go build" without them produces, and printVersion omits them.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Pass version info to command package and run
	os.Exit(cmd.Execute(version, commit, date))
}
