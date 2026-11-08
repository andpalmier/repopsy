package app

import (
	"context"
	"sync"

	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/git"
)

// dryRun reports what an explosion would produce, writing nothing. Tree sizes
// are computed once per distinct tree, which commits often share.
func dryRun(ctx context.Context, out *console.Console, repo *git.Repository, cfg Config, filter *git.PathFilter, p *plan) error {
	report := console.DryRun{Unique: len(p.commits), NoTree: cfg.NoTree, Truncated: len(repo.Shallow) > 0}

	sizes := map[string]int64{}
	if !cfg.NoTree {
		var mu sync.Mutex
		var wg sync.WaitGroup
		var firstErr error
		slots := make(chan struct{}, max(cfg.Workers, 1))
		trees := map[string]string{}
		for _, c := range p.commits {
			trees[c.TreeHash] = c.Hash
		}
		for tree, commit := range trees {
			slots <- struct{}{}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer func() { <-slots }()
				n, _, err := repo.TreeSize(ctx, commit, filter)
				mu.Lock()
				defer mu.Unlock()
				sizes[tree] = n
				if err != nil && firstErr == nil {
					firstErr = err
				}
			}()
		}
		wg.Wait()
		if firstErr != nil {
			return firstErr
		}
		for _, c := range p.commits {
			report.DedupeBytes += sizes[c.TreeHash]
		}
	}

	for _, run := range p.runs {
		commits := p.runCommits(run)
		report.Refs = append(report.Refs, console.DryRunRef{Name: run.label, Commits: len(commits), Skipped: run.skipped})
		report.PerRef += len(commits)
		for _, c := range commits {
			report.Bytes += sizes[c.TreeHash]
		}
	}
	out.DryRunReport(report)
	return nil
}
