// Package cmd is repopsy's command line: it parses arguments, reports failures,
// and hands a resolved configuration to the app package.
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

	"github.com/andpalmier/repopsy/v2/internal/app"
)

const (
	appName = "repopsy"

	// maxWorkers is the safety cap on parallel workers per branch. Each worker
	// runs a git archive and a tar process, so this bounds the process count.
	maxWorkers = 32

	usage = `repopsy - Explode git repositories by extracting each commit into a separate folder

A forensic tool for analyzing git repository history (Repository Autopsy) by extracting
each commit's state into a separate folder for comparison and analysis.

Any path inside a repository names the repository itself. Requires git 2.31 or
newer, and nothing else at runtime.

Acquire with "git clone --mirror" so every branch lands in refs/heads and none
are missed; a plain clone leaves them under refs/remotes, where repopsy does not
look. Reflogs and repository state do not survive any clone, so --include-rewritten
and REPOSITORY.txt need the original repository.

Usage:
  repopsy [flags] <repository-path>

Output, at the root:
  EXTRACTION.txt   provenance: tool build, times, scope, per-branch counts, failures
  REFLOG.txt       every recorded ref movement - the record of rewritten history
  TAGS.txt         tags, with the tagger identity and signature of annotated ones
  IDENTITIES.txt   distinct name/email pairs, and collisions between them
  REPOSITORY.txt   local config and installed hooks, neither of them versioned

Output, in each snapshot under refs/<ref>/<timestamp>_<hash>/:
  COMMIT_INFO.txt  the commit's forensic record: identity and tree hash, refs,
                   dates with the offset git recorded, signature and signer,
                   lineage, changed files with blob hashes, modes and status,
                   submodule pointers, anomalies, message and notes
  SHA256SUMS       SHA-256 of every file in tree/, for "sha256sum -c"
  tree/            that commit's complete working tree

  Content sits under tree/ and refs under refs/ so that repository names can
  never collide with repopsy's own records: a commit may contain a file called
  COMMIT_INFO.txt and a branch may be called EXTRACTION.txt.

  Snapshot directories are named <timestamp>_<short-hash>, timestamped in the
  offset the commit records, so output is identical on any host. Branch names
  containing "/" nest as directories, and a commit on several branches is
  extracted under each of them.

  With --include-rewritten and no -b, a refs/HEAD/ directory holds commits HEAD's
  reflog remembers that no branch reaches - work abandoned on a detached head.

Examples:
  # Explode all commits from all local branches
  repopsy .

  # Explode the last 5 commits
  repopsy -n 5 /path/to/repo

  # Explode a specific branch only
  repopsy -b main /path/to/repo

  # Explode with verbose output
  repopsy -v .

  # Also recover commits a reset or force-push left unreachable
  repopsy --include-rewritten .

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

// defaultWorkers is the worker count used when none is given, or when the given
// one is unusable. Never above maxWorkers, so the cap warning can only be
// triggered by an explicit request.
func defaultWorkers() int {
	return min(runtime.NumCPU(), maxWorkers)
}

// newFlagSet builds the flag set writing into o. Both parseArgs and printUsage
// use it, so flag names and help strings have a single source of truth.
// Output is discarded and Usage suppressed: callers report errors themselves.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	alias(fs.StringVar, &o.cfg.OutputDir, "o", "output", "", "Output directory (default: ./<repo-name>-exploded)")
	alias(fs.IntVar, &o.cfg.Workers, "w", "workers", defaultWorkers(), fmt.Sprintf("Number of parallel workers per branch (max %d)", maxWorkers))
	alias(fs.IntVar, &o.cfg.Limit, "n", "limit", 0, "Maximum number of commits to extract (0 = all)")
	alias(fs.StringVar, &o.cfg.Branch, "b", "branch", "", "Branch to extract from (default: all local branches)")
	alias(fs.BoolVar, &o.cfg.Verbose, "v", "verbose", false, "Show detailed output per commit")
	fs.BoolVar(&o.cfg.IncludeRewritten, "include-rewritten", false,
		"Also extract commits recovered from the reflog that no branch reaches")
	alias(fs.BoolVar, &o.showHelp, "h", "help", false, "Show help message")
	fs.BoolVar(&o.showVersion, "version", false, "Show version information")

	return fs
}

// parseArgs turns an argument list into resolved options. Warnings about
// adjusted values are written to warn; nothing else is printed.
func parseArgs(args []string, warn io.Writer) (options, error) {
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

	// Resolve the worker count here rather than downstream, so the value the
	// header reports is the value that is actually used.
	switch {
	case o.cfg.Workers > maxWorkers:
		_, _ = fmt.Fprintf(warn, "⚠ Workers capped at %d (requested %d)\n", maxWorkers, o.cfg.Workers)
		o.cfg.Workers = maxWorkers
	case o.cfg.Workers < 1:
		_, _ = fmt.Fprintf(warn, "⚠ Workers must be at least 1, using %d\n", defaultWorkers())
		o.cfg.Workers = defaultWorkers()
	}

	return o, nil
}

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

// printUsage writes the help text followed by the generated flag list.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, usage)
	var o options
	fs := newFlagSet(&o)
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// printVersion writes version information, omitting fields that were not set
// at build time.
func printVersion(w io.Writer, version, commit, date string) {
	_, _ = fmt.Fprintf(w, "%s version %s\n", appName, version)
	if commit != "none" {
		_, _ = fmt.Fprintf(w, "  commit: %s\n", commit)
	}
	if date != "unknown" {
		_, _ = fmt.Fprintf(w, "  built:  %s\n", date)
	}
}
