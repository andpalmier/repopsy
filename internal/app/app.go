// Package app orchestrates a run: open the repository, decide what to explode,
// and drive the extractor. Everything it reports goes through the console.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/andpalmier/repopsy/internal/console"
	"github.com/andpalmier/repopsy/internal/extractor"
	"github.com/andpalmier/repopsy/internal/git"
)

// outputSuffix is appended to the repository name to name the output directory.
const outputSuffix = "-exploded"

// Config holds the application configuration
type Config struct {
	RepoPath  string
	OutputDir string
	Workers   int
	Limit     int
	Branch    string // If empty, extract all local branches
	Verbose   bool

	// Writer receives all terminal output. Defaults to standard error.
	Writer io.Writer
}

// Run executes the repopsy application logic
func Run(ctx context.Context, cfg Config) error {
	out := console.New(cfg.Writer)

	repo, err := git.Open(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}

	outDir, err := resolveOutputDir(cfg, repo)
	if err != nil {
		return err
	}

	out.Banner(console.Header{
		RepoPath:  repo.Path,
		Branch:    cfg.Branch,
		OutputDir: outDir,
		Workers:   cfg.Workers,
		Limit:     cfg.Limit,
	})

	if cfg.Branch != "" {
		return runSingleBranch(ctx, out, repo, outDir, cfg)
	}
	return runAllBranches(ctx, out, repo, outDir, cfg)
}

// resolveOutputDir picks the output directory and refuses to reuse an existing
// one, so a previous explosion is never mixed with a new one.
func resolveOutputDir(cfg Config, repo *git.Repository) (string, error) {
	outDir := cfg.OutputDir
	if outDir == "" {
		outDir = filepath.Base(repo.Path) + outputSuffix
	}

	outDir, err := filepath.Abs(outDir)
	if err != nil {
		return "", fmt.Errorf("failed to resolve output path: %w", err)
	}

	if info, err := os.Stat(outDir); err == nil && info.IsDir() {
		return "", fmt.Errorf("output directory already exists: %s", outDir)
	}
	return outDir, nil
}

// runAllBranches explodes every local branch into its own directory tree.
func runAllBranches(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config) error {
	branches, err := repo.ListBranches(ctx)
	if err != nil {
		return fmt.Errorf("failed to list branches: %w", err)
	}
	if len(branches) == 0 {
		return fmt.Errorf("no branches found")
	}

	out.BranchesFound(len(branches))

	var outcomes []console.Outcome
	var extractionErr error

	for i, branch := range branches {
		if ctx.Err() != nil {
			return ctx.Err()
		}

		out.BranchStarted(i+1, len(branches), branch)

		commits, err := repo.ListCommits(ctx, git.ListOptions{
			Branch:  branch,
			Limit:   cfg.Limit,
			Reverse: true,
		})
		if err != nil {
			out.BranchListFailed(branch, err)
			continue
		}
		if len(commits) == 0 {
			out.BranchEmpty()
			continue
		}
		out.BranchCommits(len(commits))

		results, err := extract(ctx, repo, outDir, branch, cfg, commits)
		outcomes = append(outcomes, toOutcomes(results)...)
		if err != nil && extractionErr == nil {
			extractionErr = err
		}
	}

	out.Summary(outDir, outcomes, cfg.Verbose)
	return extractionErr
}

// runSingleBranch explodes one branch directly into the output directory.
func runSingleBranch(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config) error {
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

	out.CommitsToExtract(len(commits))

	// An explicit branch means a flat layout, so no branch is passed through.
	results, err := extract(ctx, repo, outDir, "", cfg, commits)

	out.Summary(outDir, toOutcomes(results), cfg.Verbose)
	return err
}

// extract runs the extractor over one branch's commits.
func extract(ctx context.Context, repo *git.Repository, outDir, branch string, cfg Config, commits []git.Commit) ([]extractor.Result, error) {
	ext := extractor.New(repo, extractor.Config{
		OutputDir: outDir,
		Branch:    branch,
		Workers:   cfg.Workers,
		Verbose:   cfg.Verbose,
		Writer:    cfg.Writer,
	})
	return ext.Run(ctx, commits)
}

// toOutcomes reduces extraction results to what the console needs to report.
func toOutcomes(results []extractor.Result) []console.Outcome {
	outcomes := make([]console.Outcome, len(results))
	for i, r := range results {
		outcomes[i] = console.Outcome{ShortHash: r.Commit.ShortHash, Err: r.Error}
	}
	return outcomes
}
