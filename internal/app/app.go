package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/andpalmier/repopsy/internal/extractor"
	"github.com/andpalmier/repopsy/internal/git"
	"github.com/fatih/color"
)

// Config holds the application configuration
type Config struct {
	RepoPath  string
	OutputDir string
	Workers   int
	Limit     int
	Branch    string // If empty, extract all branches
	Verbose   bool
}

// Run executes the repopsy application logic
func Run(ctx context.Context, cfg Config) error {
	// Open repository
	repo, err := git.Open(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	// Determine output directory
	outDir := cfg.OutputDir
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
	if info, err := os.Stat(outDir); err == nil && info.IsDir() {
		return fmt.Errorf("output directory already exists: %s", outDir)
	}

	// Print header
	printHeader(repo, outDir, cfg)

	// If branch specified, extract single branch; otherwise extract all branches
	if cfg.Branch != "" {
		return runSingleBranch(ctx, repo, outDir, cfg)
	}
	return runAllBranches(ctx, repo, outDir, cfg)
}

// runAllBranches extracts commits from all branches into separate subdirectories
func runAllBranches(ctx context.Context, repo *git.Repository, outDir string, cfg Config) error {
	yellow := color.New(color.FgYellow, color.Bold).SprintFunc()

	// List all branches
	branches, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}

	if len(branches) == 0 {
		return fmt.Errorf("no branches found")
	}

	// Display warning about time and memory
	fmt.Fprintf(os.Stderr, "%s Extracting from %d branches - this may take some time and memory!\n\n", yellow("⚠"), len(branches))

	var allResults []extractor.Result
	var extractionErr error

	for i, branch := range branches {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		// Create branch-specific output directory
		branchDir := filepath.Join(outDir, sanitizeBranchName(branch))

		fmt.Fprintf(os.Stderr, "Branch [%d/%d]: %s\n", i+1, len(branches), branch)

		// List commits for this branch
		commits, err := repo.ListCommits(ctx, git.ListOptions{
			Branch:  branch,
			Limit:   cfg.Limit,
			Reverse: true,
		})
		if err != nil {
			fmt.Fprintf(os.Stderr, "  ⚠ Failed to list commits: %v\n", err)
			continue
		}

		if len(commits) == 0 {
			fmt.Fprintf(os.Stderr, "  (no commits)\n")
			continue
		}

		fmt.Fprintf(os.Stderr, "  Found %d commits\n", len(commits))

		// Create extractor and run
		ext := extractor.New(repo, extractor.Config{
			OutputDir: branchDir,
			Workers:   cfg.Workers,
			Verbose:   cfg.Verbose,
		})

		results, err := ext.Run(ctx, commits)
		allResults = append(allResults, results...)
		if err != nil && extractionErr == nil {
			extractionErr = err
		}
	}

	// Print summary
	printSummary(allResults, outDir, cfg)

	return extractionErr
}

// runSingleBranch extracts commits from a single branch
func runSingleBranch(ctx context.Context, repo *git.Repository, outDir string, cfg Config) error {
	// List commits
	commits, err := repo.ListCommits(ctx, git.ListOptions{
		Branch:  cfg.Branch,
		Limit:   cfg.Limit,
		Reverse: true,
	})
	if err != nil {
		return fmt.Errorf("failed to list commits: %w", err)
	}

	if len(commits) == 0 {
		return fmt.Errorf("no commits found")
	}

	fmt.Fprintf(os.Stderr, "Found %d commits to extract\n\n", len(commits))

	// Create extractor and run
	ext := extractor.New(repo, extractor.Config{
		OutputDir: outDir,
		Workers:   cfg.Workers,
		Verbose:   cfg.Verbose,
	})

	results, err := ext.Run(ctx, commits)

	// Print summary
	printSummary(results, outDir, cfg)

	return err
}

// sanitizeBranchName converts a branch name to a safe directory name
func sanitizeBranchName(branch string) string {
	return strings.ReplaceAll(branch, "/", "_")
}

// printHeader displays the startup banner with configuration
func printHeader(repo *git.Repository, outDir string, cfg Config) {
	cyan := color.New(color.FgCyan, color.Bold).SprintFunc()
	magenta := color.New(color.FgMagenta, color.Bold).SprintFunc()

	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, cyan("┌─────────────────────────────────────────┐"))
	fmt.Fprintln(os.Stderr, cyan("│                 repopsy                 │"))
	fmt.Fprintln(os.Stderr, cyan("│ Repository Autopsy tool by @andpalmier  │"))
	fmt.Fprintln(os.Stderr, cyan("└─────────────────────────────────────────┘"))
	fmt.Fprintln(os.Stderr, "")

	fmt.Fprintf(os.Stderr, "Repository:  %s\n", magenta(repo.Path))
	if cfg.Branch != "" {
		fmt.Fprintf(os.Stderr, "Branch:      %s\n", cfg.Branch)
	} else {
		fmt.Fprintf(os.Stderr, "Branches:    all\n")
	}
	fmt.Fprintf(os.Stderr, "Output:      %s\n", outDir)
	fmt.Fprintf(os.Stderr, "Workers:     %d\n", cfg.Workers)
	if cfg.Limit > 0 {
		fmt.Fprintf(os.Stderr, "Limit:       %d commits\n", cfg.Limit)
	}
	fmt.Fprintln(os.Stderr, "")
}

// printSummary displays the extraction results
func printSummary(results []extractor.Result, outDir string, cfg Config) {
	fmt.Fprintln(os.Stderr, "")

	// Count successes and failures
	var successes, failures int
	var failedCommits []string
	for _, r := range results {
		if r.Error != nil {
			failures++
			failedCommits = append(failedCommits, fmt.Sprintf("  - %s: %v", r.Commit.ShortHash, r.Error))
		} else {
			successes++
		}
	}

	if failures > 0 {
		red := color.New(color.FgRed, color.Bold).SprintFunc()
		fmt.Fprintf(os.Stderr, "%s Completed with errors: %d succeeded, %d failed\n", red("⚠"), successes, failures)
		if cfg.Verbose && len(failedCommits) > 0 {
			fmt.Fprintln(os.Stderr, "Failed commits:")
			for _, fc := range failedCommits {
				fmt.Fprintln(os.Stderr, fc)
			}
		}
	}

	green := color.New(color.FgGreen, color.Bold).SprintFunc()
	fmt.Fprintf(os.Stderr, "\n%s Output: %s\n", green("📁"), outDir)
}
