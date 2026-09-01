package extractor

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/andpalmier/repopsy/internal/git"
	"github.com/andpalmier/repopsy/internal/progress"
	"github.com/andpalmier/repopsy/internal/snapshot"
)

// Config configures the extraction process.
type Config struct {
	// OutputDir is the root of the exploded repository.
	OutputDir string

	// Branch names the branch being extracted. When set, snapshots nest under
	// directories mirroring its ref path; when empty they sit directly under
	// OutputDir.
	Branch string

	Workers int
	Verbose bool
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

// New creates a new Extractor with the given configuration.
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

			result := e.extractOne(ctx, j.commit, j.index)
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
func (e *Extractor) extractOne(ctx context.Context, commit git.Commit, index int) Result {
	snapshotPath := snapshot.Path(e.config.OutputDir, e.config.Branch, commit)

	// Extract commit contents. The commit already carries its message body and
	// change statistics from ListCommits, so no further git calls are needed.
	err := e.repo.ExtractCommit(ctx, commit.Hash, snapshotPath)

	if err == nil {
		if metaErr := writeMetadataFile(snapshotPath, commit); metaErr != nil {
			err = fmt.Errorf("extraction succeeded but metadata write failed: %w", metaErr)
		}
	}

	return Result{
		Commit:     commit,
		Index:      index,
		OutputPath: snapshotPath,
		Error:      err,
	}
}

// writeMetadataFile creates the metadata file inside a snapshot directory. The
// snapshot module renders the contents; creating and closing the file is the
// caller's job, which keeps the rendering reachable in tests without a
// filesystem.
func writeMetadataFile(snapshotPath string, c git.Commit) (err error) {
	f, err := os.Create(filepath.Join(snapshotPath, snapshot.MetadataFilename))
	if err != nil {
		return fmt.Errorf("failed to create metadata file: %w", err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close metadata file: %w", closeErr)
		}
	}()

	return snapshot.WriteMetadata(f, c)
}
