package cmd

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"syscall"

	"github.com/andpalmier/repopsy/v2/internal/app"
)

// build is the version information linked in at build time.
type build struct {
	version string
	commit  string
	date    string
}

// Execute runs the CLI against the process and returns an exit code.
func Execute(version, commit, date string) int {
	return run(os.Args[1:], os.Stdout, os.Stderr, build{version, commit, date})
}

// run is the whole CLI expressed as a function of its inputs, so its exit codes
// and output are reachable from a test without spawning a subprocess.
func run(args []string, stdout, stderr io.Writer, b build) int {
	if len(args) > 0 && args[0] == identitiesCommand {
		return runIdentities(args[1:], stdout, stderr)
	}

	opts, err := parseArgs(args, stderr)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n\n", err)
		printUsage(stderr)
		return 1
	}

	if opts.showHelp {
		printUsage(stdout)
		return 0
	}
	if opts.showVersion {
		printVersion(stdout, b.version, b.commit, b.date)
		return 0
	}

	// Recorded in the extraction manifest so the output states which build
	// produced it.
	opts.cfg.ToolVersion = b.version
	opts.cfg.ToolCommit = b.commit
	opts.cfg.ToolBuilt = b.date

	// Progress and reporting share the caller's error stream.
	opts.cfg.Writer = stderr

	// Cancel on interrupt so in-flight git processes are torn down.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigChan)
	go func() {
		<-sigChan
		_, _ = fmt.Fprintln(stderr, "\n⚠ Interrupted, cleaning up...")
		cancel()
	}()

	if err := app.Run(ctx, opts.cfg); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}
