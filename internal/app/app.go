// Package app orchestrates a run: open the repository, decide what to explode,
// and drive the extractor. Everything it reports goes through the console.
package app

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/andpalmier/repopsy/internal/console"
	"github.com/andpalmier/repopsy/internal/extractor"
	"github.com/andpalmier/repopsy/internal/git"
	"github.com/andpalmier/repopsy/internal/snapshot"
)

const (
	// outputSuffix is appended to the repository name to name the output directory.
	outputSuffix = "-exploded"

	// detachedHeadDir holds commits that belong to no branch. git refuses "HEAD"
	// as a branch name, so this cannot collide with a branch's directory.
	detachedHeadDir = "HEAD"
)

// Config holds the application configuration
type Config struct {
	RepoPath  string
	OutputDir string
	Workers   int
	Limit     int
	Branch    string // If empty, extract all local branches
	Verbose   bool

	// IncludeRewritten also extracts commits recovered from the reflog.
	IncludeRewritten bool

	// Writer receives all terminal output. Defaults to standard error.
	Writer io.Writer

	// Tool build information, recorded in the extraction manifest.
	ToolVersion string
	ToolCommit  string
	ToolBuilt   string
}

// branchRun is one branch's contribution to an explosion. It is the single
// accumulator: the console summary and the extraction manifest are both
// projections of it.
type branchRun struct {
	label   string // the ref as reported to the user
	commits int    // reachable commits found, before any recovery
	skipped string // why the ref produced nothing, if it did not
	results []extractor.Result
}

// Run executes the repopsy application logic
func Run(ctx context.Context, cfg Config) error {
	out := console.New(cfg.Writer)
	startedAt := time.Now()

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

	// scheduled holds every commit hash already extracted, so a commit several
	// reflogs remember is recovered once.
	scheduled := map[string]bool{}

	var runs []branchRun
	var runErr error
	if cfg.Branch != "" {
		runs, runErr = runSingleBranch(ctx, out, repo, outDir, cfg, scheduled)
	} else {
		runs, runErr = runAllBranches(ctx, out, repo, outDir, cfg, scheduled)

		// Work abandoned on a detached head belongs to no branch, so no branch
		// reflog recovers it. Only in all-branches mode: naming one branch means
		// that branch's history.
		if headRun, err := runDetachedHead(ctx, out, repo, outDir, cfg, scheduled); err != nil {
			if runErr == nil {
				runErr = err
			}
		} else if headRun != nil {
			runs = append(runs, *headRun)
		}
	}

	// Written even for a partial run: a folder of snapshots that cannot say
	// where it came from is not evidence.
	writeReports(ctx, out, outDir, cfg, repo, startedAt, runs)

	out.Summary(outDir, outcomes(runs), cfg.Verbose)
	return runErr
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
func runAllBranches(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config, scheduled map[string]bool) ([]branchRun, error) {
	branches, err := repo.ListBranches(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}
	if len(branches) == 0 {
		return nil, fmt.Errorf("no branches found")
	}

	out.BranchesFound(len(branches))

	var runs []branchRun
	var extractionErr error

	for i, branch := range branches {
		if ctx.Err() != nil {
			return runs, ctx.Err()
		}

		out.BranchStarted(i+1, len(branches), branch)
		run := branchRun{label: branch}

		commits, err := repo.ListCommits(ctx, git.ListOptions{
			Branch:  branch,
			Limit:   cfg.Limit,
			Reverse: true,
		})
		if err != nil {
			out.BranchListFailed(branch, err)
			run.skipped = "could not list commits: " + err.Error()
			runs = append(runs, run)
			continue
		}
		if len(commits) == 0 {
			out.BranchEmpty()
			run.skipped = "no commits"
			runs = append(runs, run)
			continue
		}

		out.BranchCommits(len(commits))
		run.commits = len(commits)

		// Recovery happens before extraction so both sets travel through one
		// pass, and therefore one progress bar.
		all, err := withRewritten(ctx, out, repo, branch, cfg, commits, scheduled)
		if err != nil && extractionErr == nil {
			extractionErr = err
		}

		results, err := extract(ctx, repo, outDir, branch, cfg, all)
		run.results = results
		if err != nil && extractionErr == nil {
			extractionErr = err
		}

		runs = append(runs, run)
	}

	return runs, extractionErr
}

// runSingleBranch explodes one branch directly into the output directory.
func runSingleBranch(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config, scheduled map[string]bool) ([]branchRun, error) {
	commits, err := repo.ListCommits(ctx, git.ListOptions{
		Branch:  cfg.Branch,
		Limit:   cfg.Limit,
		Reverse: true,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to list commits: %w", err)
	}
	if len(commits) == 0 {
		return nil, fmt.Errorf("no commits found")
	}

	out.CommitsToExtract(len(commits))

	all, err := withRewritten(ctx, out, repo, cfg.Branch, cfg, commits, scheduled)
	if err != nil {
		return nil, err
	}

	// An explicit branch means a flat layout, so no branch is passed through.
	results, err := extract(ctx, repo, outDir, "", cfg, all)

	return []branchRun{{label: cfg.Branch, commits: len(commits), results: results}}, err
}

// withRewritten returns the reachable commits followed by any the ref no longer
// reaches, and marks all of them scheduled. Recovery is off unless asked for:
// the recovered set changes the snapshot count, and on most repositories it is
// empty.
func withRewritten(ctx context.Context, out *console.Console, repo *git.Repository, ref string, cfg Config, reachable []git.Commit, scheduled map[string]bool) ([]git.Commit, error) {
	all := make([]git.Commit, 0, len(reachable))
	all = append(all, reachable...)
	for _, c := range reachable {
		scheduled[c.Hash] = true
	}

	if !cfg.IncludeRewritten || ctx.Err() != nil {
		return all, nil
	}

	rewritten, err := repo.RewrittenCommits(ctx, ref, scheduled, cfg.Limit)
	if err != nil {
		return all, err
	}
	if len(rewritten) == 0 {
		return all, nil
	}

	out.RewrittenFound(len(rewritten))
	for _, c := range rewritten {
		scheduled[c.Hash] = true
	}
	return append(all, rewritten...), nil
}

// runDetachedHead extracts commits HEAD's reflog remembers that no branch
// reaches and no branch reflog recovered — work abandoned on a detached head.
//
// They belong to no branch, so they go in a top-level HEAD directory. git
// refuses "HEAD" as a branch name, so that cannot collide with a branch's own
// directory.
func runDetachedHead(ctx context.Context, out *console.Console, repo *git.Repository, outDir string, cfg Config, scheduled map[string]bool) (*branchRun, error) {
	if !cfg.IncludeRewritten || ctx.Err() != nil {
		return nil, nil
	}

	abandoned, err := repo.RewrittenCommits(ctx, detachedHeadDir, scheduled, cfg.Limit)
	if err != nil || len(abandoned) == 0 {
		return nil, err
	}

	out.DetachedHeadFound(len(abandoned))
	for _, c := range abandoned {
		scheduled[c.Hash] = true
	}

	results, err := extract(ctx, repo, outDir, detachedHeadDir, cfg, abandoned)
	return &branchRun{label: detachedHeadDir + " (detached)", results: results}, err
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

// outcomes projects the runs onto what the console needs to report.
func outcomes(runs []branchRun) []console.Outcome {
	var all []console.Outcome
	for _, run := range runs {
		for _, r := range run.results {
			all = append(all, console.Outcome{ShortHash: r.Commit.ShortHash, Err: r.Error})
		}
	}
	return all
}

// writeReports records everything about the run that is not a single commit.
// Each failure is reported and the rest still get written: a missing reflog
// should not cost you the manifest.
func writeReports(ctx context.Context, out *console.Console, outDir string, cfg Config, repo *git.Repository, startedAt time.Time, runs []branchRun) {
	reports := []snapshot.Report{buildManifest(cfg, repo, startedAt, runs)}

	// Skipped silently on cancellation, where these would only fail noisily.
	if ctx.Err() == nil {
		if entries, err := repo.Reflog(ctx); err != nil {
			out.ReportFailed(snapshot.Reflog{}.Filename(), err)
		} else {
			reports = append(reports, snapshot.Reflog{Entries: entries})
		}

		if tags, err := repo.Tags(ctx); err != nil {
			out.ReportFailed(snapshot.Tags{}.Filename(), err)
		} else {
			reports = append(reports, snapshot.Tags{Tags: tags})
		}
	}

	if state, err := repo.ReadState(); err != nil {
		out.ReportFailed(snapshot.RepositoryState{}.Filename(), err)
	} else {
		reports = append(reports, snapshot.RepositoryState{State: state})
	}

	reports = append(reports, snapshot.NewIdentities(extracted(runs)))

	for _, r := range reports {
		if err := snapshot.Write(outDir, r); err != nil {
			out.ReportFailed(r.Filename(), err)
		}
	}
}

// buildManifest projects the runs onto the provenance record.
func buildManifest(cfg Config, repo *git.Repository, startedAt time.Time, runs []branchRun) snapshot.Manifest {
	m := snapshot.Manifest{
		ToolVersion: cfg.ToolVersion,
		ToolCommit:  cfg.ToolCommit,
		ToolBuilt:   cfg.ToolBuilt,
		StartedAt:   startedAt,
		FinishedAt:  time.Now(),
		RepoPath:    repo.Path,
		Branch:      cfg.Branch,
		Workers:     cfg.Workers,
		Limit:       cfg.Limit,
	}

	for _, run := range runs {
		summary := snapshot.BranchSummary{
			Name:    run.label,
			Commits: run.commits,
			Skipped: run.skipped,
		}
		for _, r := range run.results {
			if r.Commit.Unreachable {
				summary.Rewritten++
			}
			if r.Error != nil {
				m.Failures = append(m.Failures, snapshot.Failure{
					Branch:    run.label,
					ShortHash: r.Commit.ShortHash,
					Reason:    r.Error.Error(),
				})
				continue
			}
			summary.Extracted++
		}
		m.Branches = append(m.Branches, summary)
	}
	return m
}

// extracted flattens the commits that produced a snapshot.
func extracted(runs []branchRun) []git.Commit {
	var commits []git.Commit
	for _, run := range runs {
		for _, r := range run.results {
			if r.Error == nil {
				commits = append(commits, r.Commit)
			}
		}
	}
	return commits
}
