// Package cmd provides the CLI interface for gitxplode.
// It parses command-line arguments and orchestrates the extraction process.
package cmd

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"

	"github.com/andpalmier/gitxplode/internal/extractor"
	"github.com/andpalmier/gitxplode/internal/git"
)

// CLI flags
var (
	outputDir    string
	workers      int
	limit        int
	branch       string
	folderFormat string
	quiet        bool
	verbose      bool
	showVersion  bool
	showHelp     bool
)

// Version information (set by main)
var (
	appVersion string
	appCommit  string
	appDate    string
)

const (
	appName = "gitxplode"
	usage   = `gitxplode - Expand git repositories by extracting each commit into a separate folder

Usage:
  gitxplode [flags] <repository-path>

Examples:
  # Extract all commits from current directory
  gitxplode .

  # Extract last 10 commits to custom output directory
  gitxplode -n 10 -o ./versions /path/to/repo

  # Extract with date-prefixed folders using 4 workers
  gitxplode -f date-hash -w 4 /path/to/repo

Flags:
`
)

// init sets up the CLI flags with their defaults and descriptions.
func init() {
	flag.StringVar(&outputDir, "o", "", "Output directory (default: ./<repo-name>-exploded)")
	flag.StringVar(&outputDir, "output", "", "Output directory (default: ./<repo-name>-exploded)")

	flag.IntVar(&workers, "w", runtime.NumCPU(), "Number of parallel workers")
	flag.IntVar(&workers, "workers", runtime.NumCPU(), "Number of parallel workers")

	flag.IntVar(&limit, "n", 0, "Maximum number of commits to extract (0 = all)")
	flag.IntVar(&limit, "limit", 0, "Maximum number of commits to extract (0 = all)")

	flag.StringVar(&branch, "b", "", "Branch to extract from (default: current HEAD)")
	flag.StringVar(&branch, "branch", "", "Branch to extract from (default: current HEAD)")

	flag.StringVar(&folderFormat, "f", "hash", "Output folder naming: hash, date-hash, index-hash")
	flag.StringVar(&folderFormat, "format", "hash", "Output folder naming: hash, date-hash, index-hash")

	flag.BoolVar(&quiet, "q", false, "Suppress progress output")
	flag.BoolVar(&quiet, "quiet", false, "Suppress progress output")

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

// Execute runs the CLI application and returns an exit code.
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

	// Validate folder format
	if !isValidFormat(folderFormat) {
		fmt.Fprintf(os.Stderr, "Error: invalid folder format %q (valid: hash, date-hash, index-hash)\n", folderFormat)
		return 1
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

	// Run extraction
	if err := run(repoPath); err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		return 1
	}

	return 0
}

// run performs the main extraction logic.
func run(repoPath string) error {
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

	// Open repository
	repo, err := git.Open(repoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Determine output directory
	outDir := outputDir
	if outDir == "" {
		baseName := filepath.Base(repo.Path)
		outDir = baseName + "-exploded"
	}

	// Resolve to absolute path
	outDir, err = filepath.Abs(outDir)
	if err != nil {
		return fmt.Errorf("failed to resolve output path: %w", err)
	}

	// Check if output directory already exists
	if _, err := os.Stat(outDir); err == nil {
		return fmt.Errorf("output directory already exists: %s", outDir)
	}

	// Print header
	if !quiet {
		printHeader(repo, outDir)
	}

	// List commits
	commits, err := repo.ListCommits(git.ListOptions{
		Branch:  branch,
		Limit:   limit,
		Reverse: true, // Extract in chronological order (oldest first)
	})
	if err != nil {
		return fmt.Errorf("failed to list commits: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits found")
	}

	if !quiet {
		fmt.Fprintf(os.Stderr, "Found %d commits to extract\n\n", len(commits))
	}

	// Create extractor and run
	ext := extractor.New(repo, extractor.Config{
		OutputDir:    outDir,
		Workers:      workers,
		FolderFormat: folderFormat,
		Quiet:        quiet,
		Verbose:      verbose,
	})

	results, err := ext.Run(ctx, commits)

	// Print summary
	if !quiet {
		printSummary(results, outDir, err)
	}

	return err
}

// printHeader displays the startup banner with configuration.
func printHeader(repo *git.Repository, outDir string) {
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "┌─────────────────────────────────────────┐")
	fmt.Fprintln(os.Stderr, "│           🔮 gitxplode                  │")
	fmt.Fprintln(os.Stderr, "│     Git Repository Expander             │")
	fmt.Fprintln(os.Stderr, "└─────────────────────────────────────────┘")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintf(os.Stderr, "Repository:  %s\n", repo.Path)
	fmt.Fprintf(os.Stderr, "Branch:      %s\n", branchDisplay())
	fmt.Fprintf(os.Stderr, "Output:      %s\n", outDir)
	fmt.Fprintf(os.Stderr, "Workers:     %d\n", workers)
	fmt.Fprintf(os.Stderr, "Format:      %s\n", folderFormat)
	if limit > 0 {
		fmt.Fprintf(os.Stderr, "Limit:       %d commits\n", limit)
	}
	fmt.Fprintln(os.Stderr, "")
}

// printSummary displays the extraction results.
func printSummary(results []extractor.Result, outDir string, err error) {
	fmt.Fprintln(os.Stderr, "")

	// Count successes and failures
	var successes, failures int
	for _, r := range results {
		if r.Error != nil {
			failures++
		} else {
			successes++
		}
	}

	if failures > 0 {
		fmt.Fprintf(os.Stderr, "⚠ Completed with errors: %d succeeded, %d failed\n", successes, failures)
	}

	fmt.Fprintf(os.Stderr, "\n📁 Output: %s\n", outDir)
}

// printVersion displays version information.
func printVersion() {
	fmt.Printf("%s version %s\n", appName, appVersion)
	if appCommit != "none" {
		fmt.Printf("  commit: %s\n", appCommit)
	}
	if appDate != "unknown" {
		fmt.Printf("  built:  %s\n", appDate)
	}
}

// branchDisplay returns the branch name for display.
func branchDisplay() string {
	if branch != "" {
		return branch
	}
	return "(current HEAD)"
}

// isValidFormat checks if the folder format is valid.
func isValidFormat(format string) bool {
	switch strings.ToLower(format) {
	case "hash", "date-hash", "index-hash":
		return true
	default:
		return false
	}
}
