package app

import (
	"context"
	"errors"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/secrets"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

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
func writeReports(ctx context.Context, out *console.Console, outDir string, cfg Config, repo *git.Repository, startedAt time.Time, p *plan) error {
	reports := []snapshot.Report{}
	var firstErr error
	fail := func(err error) { firstErr = errors.Join(firstErr, err) }

	// Skipped silently on cancellation, where these would only fail noisily.
	if ctx.Err() == nil {
		if entries, err := repo.Reflog(ctx); err != nil {
			out.ReportFailed(snapshot.Reflog{}.Filename(), err)
			fail(err)
		} else {
			reports = append(reports, snapshot.Reflog{Entries: entries})
		}

		if tags, err := repo.Tags(ctx); err != nil {
			out.ReportFailed(snapshot.Tags{}.Filename(), err)
			fail(err)
		} else {
			reports = append(reports, snapshot.Tags{Tags: tags})
		}
	}

	if state, err := repo.ReadState(); err != nil {
		out.ReportFailed(snapshot.RepositoryState{}.Filename(), err)
		fail(err)
	} else {
		reports = append(reports, snapshot.RepositoryState{State: state})
	}

	// Each distinct commit counts once, however many refs reach it.
	extracted := p.extracted()
	reports = append(reports, snapshot.NewIdentities(extracted))
	optional, err := p.optionalReports(ctx, out, repo, cfg)
	fail(err)
	reports = append(reports, optional...)

	// The manifest goes last so its finish time covers the other records.
	reports = append(reports, buildManifest(cfg, repo, startedAt, p, extracted))

	for _, r := range reports {
		if err := snapshot.Write(outDir, r); err != nil {
			out.ReportFailed(r.Filename(), err)
			fail(err)
			continue
		}
		if jr, ok := r.(snapshot.JSONReport); ok && cfg.JSON {
			if err := snapshot.WriteJSON(outDir, jr); err != nil {
				out.ReportFailed(snapshot.JSONFilename(r), err)
				fail(err)
			}
		}
	}
	return firstErr
}

// optionalReports builds the records only some runs ask for. Both search the
// extracted refs, so they describe the same history as the snapshots.
func (p *plan) optionalReports(ctx context.Context, out *console.Console, repo *git.Repository, cfg Config) ([]snapshot.Report, error) {
	if ctx.Err() != nil || (len(cfg.Track) == 0 && !cfg.ScanSecrets) {
		return nil, nil
	}
	var refs []git.TrackRef
	var tips []secrets.Tip
	for _, run := range p.runs {
		if len(run.hashes) > 0 {
			refs = append(refs, git.TrackRef{Label: run.label, Rev: run.rev})
			tips = append(tips, secrets.Tip{Label: run.label, Rev: run.rev})
		}
	}

	var reports []snapshot.Report
	var firstErr error
	if len(cfg.Track) > 0 {
		var record snapshot.Strings
		var failed error
		for _, s := range cfg.Track {
			h, err := repo.TrackString(ctx, s, refs)
			if err != nil {
				failed = err
				break
			}
			record.Histories = append(record.Histories, h)
		}
		if failed != nil {
			out.ReportFailed(record.Filename(), failed)
			firstErr = errors.Join(firstErr, failed)
		} else {
			reports = append(reports, record)
		}
	}
	if cfg.ScanSecrets {
		findings, err := secrets.Scan(ctx, repo, p.distinct(), tips)
		if err != nil {
			out.ReportFailed(snapshot.Secrets{}.Filename(), err)
			firstErr = errors.Join(firstErr, err)
		} else {
			reports = append(reports, snapshot.Secrets{Findings: findings})
		}
	}
	return reports, firstErr
}

// extracted returns each distinct commit that produced at least one snapshot.
func (p *plan) extracted() []git.Commit {
	ok := map[string]bool{}
	for _, run := range p.runs {
		for _, r := range run.results {
			if r.Stored {
				ok[r.Commit.Hash] = true
			}
		}
	}
	var commits []git.Commit
	for _, c := range p.distinct() {
		if ok[c.Hash] {
			commits = append(commits, c)
		}
	}
	return commits
}

// buildManifest projects the runs onto the provenance record.
func buildManifest(cfg Config, repo *git.Repository, startedAt time.Time, p *plan, extracted []git.Commit) snapshot.Manifest {
	m := snapshot.Manifest{
		ToolVersion:    cfg.ToolVersion,
		ToolCommit:     cfg.ToolCommit,
		ToolBuilt:      cfg.ToolBuilt,
		StartedAt:      startedAt,
		RepoPath:       repo.Path,
		Branch:         cfg.Branch,
		Workers:        cfg.Workers,
		Limit:          cfg.Limit,
		RefPatterns:    cfg.Refs,
		AllRefs:        cfg.AllRefs,
		Namespaces:     p.namespaces,
		Shallow:        repo.Shallow,
		UniqueCommits:  len(p.commits),
		Dedupe:         cfg.Dedupe,
		NoTree:         cfg.NoTree,
		PreserveModes:  cfg.PreserveModes,
		Paths:          cfg.Paths,
		AllowedSigners: cfg.AllowedSigners,
		Signatures:     snapshot.NewSignatureSummary(extracted),
		Anomalies:      snapshot.AnomalySummary{Counts: p.anomalies.Count(), Repository: p.anomalies.Repository},
		PathIssues:     snapshot.NewPathIssues(extracted),
	}

	for _, run := range p.runs {
		summary := snapshot.BranchSummary{
			Name:     run.label,
			Commits:  run.commits,
			Unparsed: run.unparsed,
			Skipped:  run.skipped,
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
	m.FinishedAt = time.Now()
	return m
}
