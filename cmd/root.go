package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"runtime"
	"syscall"

	"github.com/andpalmier/repopsy/internal/app"
)

// CLI flags
var (
	outputDir   string
	workers     int
	limit       int
	branch      string
	verbose     bool
	showVersion bool
	showHelp    bool
)

// Version information (set by main)
var (
	appVersion string
	appCommit  string
	appDate    string
)

const (
	appName = "repopsy"
	usage   = `repopsy - Expand git repositories by extracting each commit into a separate folder

A forensic tool for analyzing git repository history (Repository Autopsy) by extracting
each commit's state into a separate folder for comparison and analysis.

Usage:
  repopsy [flags] <repository-path>

Examples:
  # Extract all commits from all branches
  repopsy .

  # Extract last 5 commits from all branches
  repopsy -n 5 /path/to/repo

  # Extract from a specific branch only
  repopsy -b main /path/to/repo

  # Extract with verbose output
  repopsy -v .

Flags:
`
)

// init sets up the CLI flags with their defaults and descriptions
func init() {
	flag.StringVar(&outputDir, "o", "", "Output directory (default: ./<repo-name>-exploded)")
	flag.StringVar(&outputDir, "output", "", "Output directory (default: ./<repo-name>-exploded)")

	flag.IntVar(&workers, "w", runtime.NumCPU(), "Number of parallel workers")
	flag.IntVar(&workers, "workers", runtime.NumCPU(), "Number of parallel workers")

	flag.IntVar(&limit, "n", 0, "Maximum number of commits to extract (0 = all)")
	flag.IntVar(&limit, "limit", 0, "Maximum number of commits to extract (0 = all)")

	flag.StringVar(&branch, "b", "", "Branch to extract from (default: all branches)")
	flag.StringVar(&branch, "branch", "", "Branch to extract from (default: all branches)")

	flag.BoolVar(&verbose, "v", false, "Show detailed output per commit")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed output per commit")

	flag.BoolVar(&showVersion, "version", false, "Show version information")
	flag.BoolVar(&showHelp, "h", false, "Show help message")
	flag.BoolVar(&showHelp, "help", false, "Show help message")

	// Customize usage output
	flag.Usage = func() {
		fmt.Fprint(os.Stderr, usage)
		flag.PrintDefaults()
	}
}

// Execute runs the CLI application and returns an exit code
func Execute(version, commit, date string) int {
	appVersion = version
	appCommit = commit
	appDate = date

	// Parse flags
	flag.Parse()

	// Handle help and version flags
	if showHelp {
		flag.Usage()
		return 0
	}

	if showVersion {
		printVersion()
		return 0
	}

	// Get repository path from positional arguments
	args := flag.Args()
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Error: repository path is required")
		fmt.Fprintln(os.Stderr, "")
		flag.Usage()
		return 1
	}
	repoPath := args[0]

	// Run application
	cfg := app.Config{
		RepoPath:  repoPath,
		OutputDir: outputDir,
		Workers:   workers,
		Limit:     limit,
		Branch:    branch,
		Verbose:   verbose,
	}

	// Set up context with cancellation for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)
	go func() {
		<-sigChan
		fmt.Fprintln(os.Stderr, "\n⚠ Interrupted, cleaning up...")
		cancel()
	}()

	if err := app.Run(ctx, cfg); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

// printVersion displays version information
func printVersion() {
	fmt.Printf("%s version %s\n", appName, appVersion)
	if appCommit != "none" {
		fmt.Printf("  commit: %s\n", appCommit)
	}
	if appDate != "unknown" {
		fmt.Printf("  built:  %s\n", appDate)
	}
}
