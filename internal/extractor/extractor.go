// Package extractor provides concurrent commit extraction functionality.
// It manages a pool of workers that extract commit contents in parallel.
package extractor

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/andpalmier/gitxplode/internal/git"
	"github.com/andpalmier/gitxplode/internal/progress"
)

// Config configures the extraction process.
type Config struct {
	// OutputDir is the directory where extracted commits will be stored
	OutputDir string

	// Workers is the number of parallel extraction workers.
	// If 0, defaults to runtime.NumCPU().
	Workers int

	// FolderFormat specifies the naming format for output folders.
	// Valid values: "hash", "date-hash", "index-hash"
	FolderFormat string

	// Quiet suppresses progress output when true
	Quiet bool

	// Verbose enables detailed per-commit output when true
	Verbose bool
}

// Result represents the outcome of extracting a single commit.
type Result struct {
	// Commit is the commit that was extracted
	Commit git.Commit

	// Index is the position of this commit in the extraction queue
	Index int

	// OutputPath is the path where the commit was extracted
	OutputPath string

	// Error is non-nil if extraction failed
	Error error
}

// Extractor coordinates the extraction of multiple commits using a worker pool.
type Extractor struct {
	repo   *git.Repository
	config Config
}

// New creates a new Extractor with the given configuration.
func New(repo *git.Repository, cfg Config) *Extractor {
	// Apply defaults
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.NumCPU()
	}
	if cfg.FolderFormat == "" {
		cfg.FolderFormat = "hash"
	}

	return &Extractor{
		repo:   repo,
		config: cfg,
	}
}

// job represents a single extraction task sent to workers.
type job struct {
	commit git.Commit
	index  int
}

// Run extracts all provided commits concurrently.
// It returns a slice of results (one per commit) and an overall error if any extractions failed.
func (e *Extractor) Run(ctx context.Context, commits []git.Commit) ([]Result, error) {
	if len(commits) == 0 {
		return nil, nil
	}

	// Initialize progress reporter
	reporter := progress.New(progress.Config{
		Total:   len(commits),
		Quiet:   e.config.Quiet,
		Verbose: e.config.Verbose,
	})
	reporter.Start()

	// Create channels for job distribution and result collection
	jobs := make(chan job, len(commits))
	results := make(chan Result, len(commits))

	// Start worker pool
	var wg sync.WaitGroup
	for i := 0; i < e.config.Workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			e.worker(ctx, jobs, results, reporter)
		}()
	}

	// Send jobs to workers
	for i, commit := range commits {
		jobs <- job{commit: commit, index: i}
	}
	close(jobs)

	// Wait for all workers to complete, then close results channel
	go func() {
		wg.Wait()
		close(results)
	}()

	// Collect results
	allResults := make([]Result, 0, len(commits))
	var errors []error

	for result := range results {
		allResults = append(allResults, result)
		if result.Error != nil {
			errors = append(errors, result.Error)
		}
	}

	reporter.Finish()

	// Return combined error if any extractions failed
	if len(errors) > 0 {
		return allResults, fmt.Errorf("%d of %d extractions failed", len(errors), len(commits))
	}

	return allResults, nil
}

// worker processes jobs from the jobs channel and sends results to the results channel.
func (e *Extractor) worker(ctx context.Context, jobs <-chan job, results chan<- Result, reporter *progress.Reporter) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-jobs:
			if !ok {
				return // Channel closed, no more jobs
			}

			result := e.extractOne(j.commit, j.index)
			results <- result

			// Report progress
			if result.Error != nil {
				reporter.Increment(fmt.Sprintf("✗ %s: %v", j.commit.ShortHash, result.Error))
			} else {
				reporter.Increment(fmt.Sprintf("✓ %s → %s", j.commit.ShortHash, filepath.Base(result.OutputPath)))
			}
		}
	}
}

// extractOne extracts a single commit and returns the result.
func (e *Extractor) extractOne(commit git.Commit, index int) Result {
	// Generate output path
	folderName := commit.FolderName(e.config.FolderFormat, index+1) // 1-indexed for display
	outputPath := filepath.Join(e.config.OutputDir, folderName)

	// Perform extraction
	err := e.repo.ExtractCommit(commit.Hash, outputPath)

	return Result{
		Commit:     commit,
		Index:      index,
		OutputPath: outputPath,
		Error:      err,
	}
}
