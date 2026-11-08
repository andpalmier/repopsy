// Package extractor extracts many commits concurrently, bounded by a worker
// count, and reports one result per commit in commit order however the workers
// interleave.
package extractor

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"runtime"
	"sync"

	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// Config configures the extraction process.
type Config struct {
	// Dir is the directory snapshots go in. See snapshot.RefDir.
	Dir string

	// Extract controls how each tree is written.
	Extract git.ExtractOptions

	// JSON also writes each snapshot's record as COMMIT_INFO.json.
	JSON bool

	// Workers bounds how many commits are extracted at once.
	Workers int

	// Verbose prints a line per completed commit alongside the progress bar.
	Verbose bool

	// Writer receives progress output. Defaults to standard error.
	Writer io.Writer
}

// dirPerms is the mode for snapshot directories.
const dirPerms = 0o755

// Result is the outcome of extracting one commit.
type Result struct {
	Commit     git.Commit
	OutputPath string
	Error      error

	// Stored says the snapshot exists on disk even if a later step, such as
	// creating a deduplicated ref link, failed.
	Stored bool
}

// Extractor extracts many commits concurrently, bounded by a worker count.
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
	if ctx.Err() != nil {
		if len(failures) > 0 {
			return results, errors.Join(fmt.Errorf("%d of %d extractions failed: %w",
				len(failures), len(commits), errors.Join(failures...)), ctx.Err())
		}
		return results, ctx.Err()
	}
	if len(failures) > 0 {
		return results, fmt.Errorf("%d of %d extractions failed: %w",
			len(failures), len(commits), errors.Join(failures...))
	}

	return results, nil
}

// extractOne extracts one commit and writes its snapshot's records.
func (e *Extractor) extractOne(ctx context.Context, commit git.Commit) Result {
	snapshotPath := snapshot.SnapshotIn(e.config.Dir, commit)

	// Content goes one level below the snapshot's own records, so a file in the
	// commit can never overwrite one of them.
	//
	// The commit already carries its message body and change statistics from
	// ListCommits, so no further git calls are needed here.
	extracted, err := e.repo.Extract(ctx, commit.Hash, snapshot.TreePath(snapshotPath), e.config.Extract)
	if err == nil {
		// With --no-tree nothing else creates the snapshot directory.
		err = os.MkdirAll(snapshotPath, dirPerms)
	}
	if err == nil {
		// Only the tree listing knows which gitlinks the commit contains, and
		// what extraction had to change.
		commit.Submodules = extracted.Submodules
		commit.Tree = extracted.Tree

		records := []record{
			{snapshot.MetadataFilename, func(w io.Writer) error { return snapshot.WriteMetadata(w, commit) }},
			{snapshot.ChecksumFilename, func(w io.Writer) error { return snapshot.WriteChecksums(w, extracted.Digests) }},
		}
		if e.config.JSON {
			records = append(records, record{snapshot.MetadataJSONFilename, func(w io.Writer) error { return snapshot.WriteMetadataJSON(w, commit) }})
		}
		for _, r := range records {
			if writeErr := snapshot.WriteFile(snapshotPath, r.name, r.render); writeErr != nil && err == nil {
				err = fmt.Errorf("extraction succeeded but writing %s failed: %w", r.name, writeErr)
			}
		}
	}

	return Result{
		Commit:     commit,
		OutputPath: snapshotPath,
		Error:      err,
		Stored:     err == nil,
	}
}

// record is one file written beside a snapshot's tree.
type record struct {
	name   string
	render func(io.Writer) error
}
