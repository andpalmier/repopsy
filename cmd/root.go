package cmd

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/andpalmier/repopsy/internal/app"
)

const (
	appName = "repopsy"

	usage = `repopsy - Explode git repositories by extracting each commit into a separate folder

A forensic tool for analyzing git repository history (Repository Autopsy) by extracting
each commit's state into a separate folder for comparison and analysis.

Usage:
  repopsy [flags] <repository-path>

Examples:
  # Explode all commits from all branches
  repopsy .

  # Explode the last 5 commits from all branches
  repopsy -n 5 /path/to/repo

  # Explode a specific branch only
  repopsy -b main /path/to/repo

  # Explode with verbose output
  repopsy -v .

Flags:
`
)

// options is the fully-resolved result of parsing a command line.
type options struct {
	cfg         app.Config
	showVersion bool
	showHelp    bool
}

// alias registers the same target under both a short and a long flag name, so
// the help text for a flag lives in exactly one place.
func alias[T any](reg func(*T, string, T, string), p *T, short, long string, def T, help string) {
	reg(p, short, def, help)
	reg(p, long, def, help)
}

// newFlagSet builds the flag set writing into o. Both parseArgs and printUsage
// use it, so flag names and help strings have a single source of truth.
// Output is discarded and Usage suppressed: callers report errors themselves.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	alias(fs.StringVar, &o.cfg.OutputDir, "o", "output", "", "Output directory (default: ./<repo-name>-exploded)")
	alias(fs.IntVar, &o.cfg.Workers, "w", "workers", runtime.NumCPU(), "Number of parallel workers per branch")
	alias(fs.IntVar, &o.cfg.Limit, "n", "limit", 0, "Maximum number of commits to extract (0 = all)")
	alias(fs.StringVar, &o.cfg.Branch, "b", "branch", "", "Branch to extract from (default: all branches)")
	alias(fs.BoolVar, &o.cfg.Verbose, "v", "verbose", false, "Show detailed output per commit")
	alias(fs.BoolVar, &o.showHelp, "h", "help", false, "Show help message")
	fs.BoolVar(&o.showVersion, "version", false, "Show version information")

	return fs
}

// parseArgs turns an argument list into resolved options.
func parseArgs(args []string) (options, error) {
	var o options
	fs := newFlagSet(&o)

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	// Help and version short-circuit before the repository path is required.
	if o.showHelp || o.showVersion {
		return o, nil
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return options{}, errors.New("repository path is required")
	}
	o.cfg.RepoPath = rest[0]

	return o, nil
}

// Execute runs the CLI application and returns an exit code.
func Execute(version, commit, date string) int {
	opts, err := parseArgs(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n\n", err)
		printUsage(os.Stderr)
		return 1
	}

	if opts.showHelp {
		printUsage(os.Stdout)
		return 0
	}
	if opts.showVersion {
		printVersion(os.Stdout, version, commit, date)
		return 0
	}

	// Cancel on interrupt so in-flight git and tar processes are torn down.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "\n⚠ Interrupted, cleaning up...")
		cancel()
	}()

	if err := app.Run(ctx, opts.cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

// printUsage writes the help text followed by the generated flag list.
func printUsage(w io.Writer) {
	fmt.Fprint(w, usage)
	var o options
	fs := newFlagSet(&o)
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// printVersion writes version information, omitting fields that were not set
// at build time.
func printVersion(w io.Writer, version, commit, date string) {
	fmt.Fprintf(w, "%s version %s\n", appName, version)
	if commit != "none" {
		fmt.Fprintf(w, "  commit: %s\n", commit)
	}
	if date != "unknown" {
		fmt.Fprintf(w, "  built:  %s\n", date)
	}
}
