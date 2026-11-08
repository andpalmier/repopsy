package app

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/extractor"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// extractPerRef writes each ref's snapshots into its own directory, so every
// ref directory is a complete account of that ref's history.
func (p *plan) extractPerRef(ctx context.Context, out *console.Console, repo *git.Repository, cfg Config, opts git.ExtractOptions) error {
	var firstErr error
	for i := range p.runs {
		run := &p.runs[i]
		if ctx.Err() != nil {
			return ctx.Err()
		}
		commits := p.runCommits(*run)
		if len(commits) == 0 {
			continue
		}
		if len(p.runs) > 1 {
			out.BranchStarted(i+1, len(p.runs), run.label)
		}
		results, err := extract(ctx, repo, run.dir, cfg, opts, commits)
		run.results = results
		p.record(results)
		if err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// extractDeduplicated writes each distinct commit once under commits/, then
// gives every ref directory a relative symlink per commit, so the familiar
// per-ref layout still resolves without the duplicate data.
func (p *plan) extractDeduplicated(ctx context.Context, repo *git.Repository, outDir string, cfg Config, opts git.ExtractOptions) error {
	commitsDir := filepath.Join(outDir, snapshot.CommitsDir)
	results, err := extract(ctx, repo, commitsDir, cfg, opts, p.distinct())
	p.record(results)

	stored := make(map[string]extractor.Result, len(results))
	for _, r := range results {
		stored[r.Commit.Hash] = r
	}

	var linkErr error
	for i := range p.runs {
		run := &p.runs[i]
		for _, c := range p.runCommits(*run) {
			r, ok := stored[c.Hash]
			if !ok {
				continue // never dispatched: the run was cancelled
			}
			link := snapshot.SnapshotIn(run.dir, c)
			if r.Error == nil {
				r.Error = linkSnapshot(r.OutputPath, link)
				if r.Error != nil && linkErr == nil {
					linkErr = r.Error
				}
			}
			r.OutputPath = link
			run.results = append(run.results, r)
		}
	}
	if err != nil {
		return err
	}
	return linkErr
}

// linkSnapshot points link at a stored snapshot by a relative path, so the
// output directory can be moved or archived as a whole.
func linkSnapshot(target, link string) error {
	if err := os.MkdirAll(filepath.Dir(link), outputDirPerms); err != nil {
		return fmt.Errorf("failed to create %s: %w", filepath.Dir(link), err)
	}
	rel, err := filepath.Rel(filepath.Dir(link), target)
	if err != nil {
		return err
	}
	if err := os.Symlink(rel, link); err != nil {
		return fmt.Errorf("failed to link snapshot: %w", err)
	}
	return nil
}

// record keeps what extraction learned about each commit's tree, which the
// path issue summary needs.
func (p *plan) record(results []extractor.Result) {
	for _, r := range results {
		if c := p.commits[r.Commit.Hash]; c != nil && r.Error == nil {
			c.Tree, c.Submodules = r.Commit.Tree, r.Commit.Submodules
		}
	}
}

// extract runs the extractor over commits, writing snapshots into dir.
func extract(ctx context.Context, repo *git.Repository, dir string, cfg Config, opts git.ExtractOptions, commits []git.Commit) ([]extractor.Result, error) {
	ext := extractor.New(repo, extractor.Config{
		Dir:     dir,
		Workers: cfg.Workers,
		Verbose: cfg.Verbose,
		Writer:  cfg.Writer,
		Extract: opts,
		JSON:    cfg.JSON,
	})
	return ext.Run(ctx, commits)
}
