// repopsy expands a git repository by extracting each commit into
// a separate folder for easy comparison and analysis
package main

import (
	"os"

	"github.com/andpalmier/repopsy/cmd"
)

// Version information (set at build time via -ldflags)
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	// Pass version info to command package and run
	os.Exit(cmd.Execute(version, commit, date))
}
