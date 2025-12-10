// Package extractor provides concurrent commit extraction functionality.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/andpalmier/repopsy/internal/git"
	"github.com/andpalmier/repopsy/internal/progress"
)

// Config configures the extraction process
type Config struct {
	OutputDir string
	Workers   int
	Verbose   bool
}

// Result represents the outcome of a single commit
type Result struct {
	Commit     git.Commit
	Index      int
	OutputPath string
	Error      error
}

// Extractor coordinates the extraction of multiple commits using a worker pool
type Extractor struct {
	repo   *git.Repository
	config Config
}

// New creates a new Extractor with the given configuration
func New(repo *git.Repository, cfg Config) *Extractor {
	if cfg.Workers <= 0 {
		cfg.Workers = runtime.NumCPU()
	}
	return &Extractor{repo: repo, config: cfg}
}

// job represents a single extraction task sent to workers
type job struct {
	commit git.Commit
	index  int
}

// Run extracts all provided commits concurrently
func (e *Extractor) Run(ctx context.Context, commits []git.Commit) ([]Result, error) {
	if len(commits) == 0 {
		return nil, nil
	}

	// Initialize progress reporter
	reporter := progress.New(progress.Config{
		Total:   len(commits),
		Verbose: e.config.Verbose,
	})
	reporter.Start()

	// jobs channel receives tasks (commits to connect)
	// results channel collects the extractions
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
	var extractionErrs []error

	for result := range results {
		allResults = append(allResults, result)
		if result.Error != nil {
			extractionErrs = append(extractionErrs, result.Error)
		}
	}

	reporter.Finish()

	if len(extractionErrs) > 0 {
		return allResults, fmt.Errorf("%d of %d extractions failed: %w",
			len(extractionErrs), len(commits), errors.Join(extractionErrs...))
	}

	return allResults, nil
}

// worker processes jobs from the jobs channel
func (e *Extractor) worker(ctx context.Context, jobs <-chan job, results chan<- Result, reporter *progress.Reporter) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-jobs:
			if !ok {
				return
			}

			result := e.extractOne(j.commit, j.index)
			results <- result

			if result.Error != nil {
				reporter.Increment(fmt.Sprintf("✗ %s: %v", j.commit.ShortHash, result.Error))
			} else {
				reporter.Increment(fmt.Sprintf("✓ %s → %s", j.commit.ShortHash, filepath.Base(result.OutputPath)))
			}
		}
	}
}

// extractOne extracts a single commit and returns the result
func (e *Extractor) extractOne(commit git.Commit, index int) Result {
	// Format: YYYYMMDD_HHMMSS_hash (e.g., 20231205_143022_abc1234)
	timestamp := commit.AuthorDate.Format("20060102_150405")
	folderName := fmt.Sprintf("%s_%s", timestamp, commit.ShortHash)
	outputPath := filepath.Join(e.config.OutputDir, folderName)

	// Extract commit contents
	err := e.repo.ExtractCommit(commit.Hash, outputPath)

	// Always write metadata if extraction succeeded
	if err == nil {
		if fullMsg, msgErr := e.repo.GetCommitFullMessage(commit.Hash); msgErr == nil {
			commit.FullMessage = fullMsg
		}

		if stats, statsErr := e.repo.GetCommitStats(commit.Hash); statsErr == nil {
			commit.FilesChanged = stats.FilesChanged
			commit.Insertions = stats.Insertions
			commit.Deletions = stats.Deletions
		}

		if metaErr := commit.WriteMetadataFile(outputPath); metaErr != nil {
			err = fmt.Errorf("extraction succeeded but metadata write failed: %w", metaErr)
		}
	}

	return Result{
		Commit:     commit,
		Index:      index,
		OutputPath: outputPath,
		Error:      err,
	}
}
