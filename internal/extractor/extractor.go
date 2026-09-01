// Package extractor runs the extraction of many commits concurrently.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/andpalmier/repopsy/internal/console"
	"github.com/andpalmier/repopsy/internal/git"
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

	// Workers bounds how many commits are extracted at once.
	Workers int

	// Verbose prints a line per completed commit alongside the progress bar.
	Verbose bool

	// Writer receives progress output. Defaults to standard error.
	Writer io.Writer
}

// Result represents the outcome of a single commit
type Result struct {
	Commit     git.Commit
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

// Run extracts all provided commits concurrently, returning one Result per
// commit attempted, in commit order.
func (e *Extractor) Run(ctx context.Context, commits []git.Commit) ([]Result, error) {
	if len(commits) == 0 {
		return nil, nil
	}

	progress := console.NewProgress(e.config.Writer, len(commits), e.config.Verbose)

	// Results are addressed by index, so they stay in commit order however the
	// workers interleave, and no channel is needed to collect them.
	results := make([]Result, len(commits))

	// A slot is taken before each goroutine is spawned, so at most Workers
	// extractions are ever in flight.
	slots := make(chan struct{}, e.config.Workers)
	var wg sync.WaitGroup

	dispatched := 0
	for i, commit := range commits {
		if ctx.Err() != nil {
			break
		}

		slots <- struct{}{}
		dispatched++
		wg.Add(1)

		go func() {
			defer wg.Done()
			defer func() { <-slots }()

			result := e.extractOne(ctx, commit)
			results[i] = result

			if result.Error != nil {
				progress.Failed(commit.ShortHash, result.Error)
			} else {
				progress.Done(commit.ShortHash, result.OutputPath)
			}
		}()
	}

	wg.Wait()
	progress.Finish()

	// Commits never dispatched — the context was cancelled — have no result.
	results = results[:dispatched]

	var failures []error
	for _, r := range results {
		if r.Error != nil {
			failures = append(failures, r.Error)
		}
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("%d of %d extractions failed: %w",
			len(failures), len(commits), errors.Join(failures...))
	}

	return results, nil
}

// extractOne extracts a single commit and returns the result
func (e *Extractor) extractOne(ctx context.Context, commit git.Commit) Result {
	snapshotPath := snapshot.Path(e.config.OutputDir, e.config.Branch, commit)

	// The commit already carries its message body and change statistics from
	// ListCommits, so no further git calls are needed here.
	extracted, err := e.repo.ExtractCommit(ctx, commit.Hash, snapshotPath)
	if err == nil {
		// Only the tree listing knows which gitlinks the commit contains.
		commit.Submodules = extracted.Submodules

		if metaErr := writeSnapshotFile(snapshotPath, snapshot.MetadataFilename, func(w io.Writer) error {
			return snapshot.WriteMetadata(w, commit)
		}); metaErr != nil {
			err = fmt.Errorf("extraction succeeded but metadata write failed: %w", metaErr)
		}
		if sumErr := writeSnapshotFile(snapshotPath, snapshot.ChecksumFilename, func(w io.Writer) error {
			return snapshot.WriteChecksums(w, extracted.Digests)
		}); sumErr != nil && err == nil {
			err = fmt.Errorf("extraction succeeded but checksum write failed: %w", sumErr)
		}
	}

	return Result{
		Commit:     commit,
		OutputPath: snapshotPath,
		Error:      err,
	}
}

// writeSnapshotFile creates one file inside a snapshot directory. The snapshot
// module renders the contents; creating and closing the file is the caller's
// job, which keeps the rendering reachable in tests without a filesystem.
func writeSnapshotFile(snapshotPath, name string, render func(io.Writer) error) (err error) {
	f, err := os.Create(filepath.Join(snapshotPath, name))
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", name, err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close %s: %w", name, closeErr)
		}
	}()

	return render(f)
}
