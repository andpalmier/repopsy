package app

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/andpalmier/repopsy/v2/internal/anomaly"
	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/extractor"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// branchRun is one ref's contribution to an explosion. It is the single
// accumulator: the console summary and the extraction manifest are both
// projections of it.
type branchRun struct {
	label    string // the ref as reported to the user
	ref      string // the full ref name, or "" when it is not a ref
	rev      string // what git walks
	dir      string // where its snapshots go
	commits  int    // reachable commits found, before any recovery
	unparsed int    // commits git listed whose metadata could not be parsed
	skipped  string // why the ref produced nothing, if it did not
	results  []extractor.Result

	hashes    []string     // reachable commits, oldest first
	recovered []git.Commit // commits recovered from its reflog
}

// plan is everything decided before any snapshot is written.
type plan struct {
	runs []branchRun

	// commits holds every distinct commit, listed once however many refs
	// reach it. order is the order they were first seen in.
	commits map[string]*git.Commit
	order   []string

	namespaces []snapshot.NamespaceSummary
	pullHint   int // pull request heads left out, worth pointing at

	anomalies anomaly.Result
}

// buildPlan selects the refs, walks each one, and lists every distinct commit
// once.
//
// Refs are walked with rev-list, which is cheap, and full metadata is fetched
// once per distinct commit. Listing every ref with git log instead would parse
// each shared commit once per ref, which a mirror with hundreds of pull request
// refs turns into hours.
func buildPlan(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config) (*plan, error) {
	p := &plan{commits: map[string]*git.Commit{}}

	if cfg.Branch != "" {
		// An explicit branch means a flat layout, so no ref directory.
		p.runs = []branchRun{{label: cfg.Branch, rev: cfg.Branch, dir: outDir}}
		if _, err := repo.RevList(ctx, "refs/heads/"+cfg.Branch, 1); err == nil {
			p.runs[0].ref = "refs/heads/" + cfg.Branch
		}
	} else {
		refs, err := selectRefs(ctx, repo, cfg)
		if err != nil {
			return nil, err
		}
		if len(refs) == 0 {
			if len(cfg.Refs) > 0 || cfg.AllRefs {
				return nil, fmt.Errorf("no refs match %s", scopeText(cfg))
			}
			return nil, fmt.Errorf("no branches found")
		}
		if len(cfg.Refs) > 0 || cfg.AllRefs {
			out.RefsFound(len(refs))
		} else {
			out.BranchesFound(len(refs))
		}
		for _, ref := range refs {
			label := ref.Name
			if branch, ok := ref.Branch(); ok {
				label = branch
			}
			run := branchRun{label: label, ref: ref.Name, rev: ref.Name, dir: snapshot.RefDir(outDir, ref.Name)}
			if ref.Type != "commit" {
				run.skipped = "points at a " + ref.Type + ", not a commit"
			}
			p.runs = append(p.runs, run)
		}
	}

	seen := map[string]bool{}
	for i := range p.runs {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		run := &p.runs[i]
		if cfg.Branch == "" {
			out.BranchStarted(i+1, len(p.runs), run.label)
		}
		if run.skipped != "" {
			out.RefSkipped(run.label, run.skipped)
			continue
		}

		hashes, err := repo.RevList(ctx, run.rev, cfg.Limit)
		if err != nil {
			if cfg.Branch != "" {
				return nil, fmt.Errorf("failed to list commits: %w", err)
			}
			out.BranchListFailed(run.label, err)
			run.skipped = "could not list commits: " + err.Error()
			continue
		}
		if len(hashes) == 0 {
			if cfg.Branch != "" {
				return nil, fmt.Errorf("no commits found")
			}
			out.BranchEmpty()
			run.skipped = "no commits"
			continue
		}
		run.hashes, run.commits = hashes, len(hashes)
		if cfg.Branch != "" {
			out.CommitsToExtract(len(hashes))
		} else {
			out.BranchCommits(len(hashes))
		}
		for _, h := range hashes {
			if !seen[h] {
				seen[h] = true
				p.order = append(p.order, h)
			}
		}
	}

	if len(p.order) > 0 {
		listed, err := repo.ListCommits(ctx, git.ListOptions{Hashes: p.order})
		if err != nil {
			return nil, fmt.Errorf("failed to list commits: %w", err)
		}
		for i := range listed.Commits {
			p.commits[listed.Commits[i].Hash] = &listed.Commits[i]
		}
	}

	// A commit rev-list named but git log did not parse is missing, and each
	// ref that reaches it says so.
	for i := range p.runs {
		for _, h := range p.runs[i].hashes {
			if p.commits[h] == nil {
				p.runs[i].unparsed++
			}
		}
		if p.runs[i].unparsed > 0 {
			out.UnparsedRecords(p.runs[i].unparsed)
		}
	}
	if len(p.runs) > 1 || cfg.Dedupe {
		out.DistinctCommits(len(p.commits))
	}

	if err := p.summariseNamespaces(ctx, repo, cfg); err != nil {
		return nil, err
	}
	return p, nil
}

// selectRefs lists the refs a run covers, in name order.
func selectRefs(ctx context.Context, repo *git.Repository, cfg Config) ([]git.Ref, error) {
	switch {
	case cfg.AllRefs:
		all, err := repo.ListRefs(ctx, nil)
		if err != nil {
			return nil, err
		}
		var refs []git.Ref
		for _, r := range all {
			if !excludedFromAllRefs(r.Name) {
				refs = append(refs, r)
			}
		}
		return refs, nil
	case len(cfg.Refs) > 0:
		return repo.ListRefs(ctx, cfg.Refs)
	default:
		return repo.ListRefs(ctx, []string{branchesPattern})
	}
}

func excludedFromAllRefs(name string) bool {
	for _, prefix := range snapshot.ExcludedFromAllRefs {
		if name == prefix || strings.HasPrefix(name, prefix+"/") {
			return true
		}
	}
	return false
}

// summariseNamespaces records every ref namespace and how much of it the run
// covers, so what was left out is on the record rather than invisible.
func (p *plan) summariseNamespaces(ctx context.Context, repo *git.Repository, cfg Config) error {
	all, err := repo.ListRefs(ctx, nil)
	if err != nil {
		return err
	}
	extracted := map[string]bool{}
	for _, run := range p.runs {
		if run.ref != "" && run.skipped == "" {
			extracted[run.ref] = true
		}
	}

	byName := map[string]*snapshot.NamespaceSummary{}
	var names []string
	pullHeads := 0
	for _, r := range all {
		ns := r.Namespace()
		if byName[ns] == nil {
			byName[ns] = &snapshot.NamespaceSummary{Name: ns}
			names = append(names, ns)
		}
		byName[ns].Refs++
		if extracted[r.Name] {
			byName[ns].Extracted++
		} else if ns == "refs/pull" && strings.HasSuffix(r.Name, "/head") {
			pullHeads++
		}
	}
	sort.Strings(names)
	for _, n := range names {
		ns := *byName[n]
		switch {
		case n == "refs/pull" && pullHeads > 0:
			ns.Hint = fmt.Sprintf("--refs '%s' adds %d", pullHeadPattern, pullHeads)
		case n == "refs/tags" && ns.Extracted == 0:
			ns.Hint = "recorded in TAGS.txt"
		case n == "refs/stash" && ns.Extracted == 0:
			ns.Hint = "recorded in REFLOG.txt"
		}
		p.namespaces = append(p.namespaces, ns)
	}
	p.pullHint = pullHeads
	return nil
}

// recoverRewritten adds to each ref the commits its reflog remembers that no
// selected ref reaches, and in all-refs mode the work abandoned on a detached
// head. Recovery is off unless asked for: the recovered set changes the
// snapshot count, and on most repositories it is empty.
func (p *plan) recoverRewritten(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config) error {
	if !cfg.IncludeRewritten || ctx.Err() != nil {
		return nil
	}

	// Every selected ref is walked before any is recovered from, so a commit
	// another ref reaches is never mistaken for one that was rewritten away.
	scheduled := map[string]bool{}
	for h := range p.commits {
		scheduled[h] = true
	}
	recover := func(rev string) ([]git.Commit, error) {
		found, err := repo.RewrittenCommits(ctx, rev, scheduled, cfg.Limit)
		for _, c := range found {
			scheduled[c.Hash] = true
			p.commits[c.Hash] = &c
			p.order = append(p.order, c.Hash)
		}
		return found, err
	}

	var firstErr error
	for i := range p.runs {
		run := &p.runs[i]
		if run.skipped != "" {
			continue
		}
		found, err := recover(run.rev)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(found) > 0 {
			out.BranchStarted(i+1, len(p.runs), run.label)
			out.RewrittenFound(len(found))
			run.recovered = found
		}
	}

	// Work abandoned on a detached head belongs to no branch, so no branch
	// reflog recovers it. Only without -b: naming one branch means that
	// branch's history.
	if cfg.Branch == "" {
		abandoned, err := recover(detachedHeadDir)
		if err != nil && firstErr == nil {
			firstErr = err
		}
		if len(abandoned) > 0 {
			out.DetachedHeadFound(len(abandoned))
			p.runs = append(p.runs, branchRun{
				label: detachedHeadDir + " (detached)", rev: detachedHeadDir,
				dir: snapshot.RefDir(outDir, detachedHeadDir), recovered: abandoned,
			})
		}
	}
	return firstErr
}

// detectAnomalies runs the history-wide checks and attaches each finding to
// its commit, so every COMMIT_INFO.txt carries its own.
func (p *plan) detectAnomalies(shallow bool, t anomaly.Thresholds) anomaly.Result {
	commits := p.distinct()
	var refs []anomaly.Ref
	for _, run := range p.runs {
		if run.ref != "" && len(run.hashes) > 0 {
			refs = append(refs, anomaly.Ref{Name: run.ref, Hashes: run.hashes})
		}
	}
	res := anomaly.Detect(commits, refs, shallow, t)
	for h, found := range res.ByCommit {
		if c := p.commits[h]; c != nil {
			c.Anomalies = found
		}
	}
	p.anomalies = res
	return res
}

// distinct returns every distinct commit, in the order first seen.
func (p *plan) distinct() []git.Commit {
	out := make([]git.Commit, 0, len(p.order))
	for _, h := range p.order {
		if c := p.commits[h]; c != nil {
			out = append(out, *c)
		}
	}
	return out
}

// runCommits returns a ref's commits, reachable ones oldest first and then any
// recovered from its reflog.
func (p *plan) runCommits(run branchRun) []git.Commit {
	out := make([]git.Commit, 0, len(run.hashes)+len(run.recovered))
	for _, h := range run.hashes {
		if c := p.commits[h]; c != nil {
			out = append(out, *c)
		}
	}
	for _, c := range run.recovered {
		out = append(out, *p.commits[c.Hash])
	}
	return out
}
