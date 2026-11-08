// Package app orchestrates a run: open the repository, decide what to explode,
// and drive the extractor. Everything it reports goes through the console.
package app

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/anomaly"
	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

const (
	// outputSuffix is appended to the repository name to name the output directory.
	outputSuffix = "-exploded"

	// outputDirPerms is the mode for the output directory.
	outputDirPerms = 0o755

	// detachedHeadDir holds commits that belong to no branch. git refuses "HEAD"
	// as a branch name, so this cannot collide with a branch's directory.
	detachedHeadDir = "HEAD"

	// branchesPattern selects local branches, the default scope.
	branchesPattern = "refs/heads/"

	// pullHeadPattern selects the head of every GitHub pull request.
	pullHeadPattern = "refs/pull/*/head"
)

// Config holds everything one run needs, resolved from the command line.
type Config struct {
	RepoPath  string
	OutputDir string
	Workers   int
	Limit     int
	Branch    string // If empty, extract all local branches
	Verbose   bool

	// IncludeRewritten also extracts commits recovered from the reflog.
	IncludeRewritten bool

	// Refs selects refs by for-each-ref pattern instead of local branches.
	// AllRefs selects every ref except those in snapshot.ExcludedFromAllRefs.
	Refs    []string
	AllRefs bool

	// PreserveModes, NoTree and Paths control what each tree/ holds.
	PreserveModes bool
	NoTree        bool
	Paths         []string

	// Dedupe stores each commit once under commits/ and links ref
	// directories to it.
	Dedupe bool

	// DryRun reports what would be extracted and writes nothing.
	DryRun bool

	// JSON also writes a JSON form of each record.
	JSON bool

	// AllowedSigners verifies SSH signatures against this file.
	AllowedSigners string

	// Thresholds tune the anomaly checks.
	Thresholds anomaly.Thresholds

	// Track lists strings whose history is recorded in STRINGS.txt.
	Track []string

	// ScanSecrets records credential-shaped strings in SECRETS.txt.
	ScanSecrets bool

	// Writer receives all terminal output. Defaults to standard error.
	Writer io.Writer

	// Tool build information, recorded in the extraction manifest.
	ToolVersion string
	ToolCommit  string
	ToolBuilt   string
}

// Run explodes the repository described by cfg.
func Run(ctx context.Context, cfg Config) error {
	out := console.New(cfg.Writer)
	startedAt := time.Now()

	repo, err := git.Open(cfg.RepoPath)
	if err != nil {
		return fmt.Errorf("failed to open repository: %w", err)
	}
	if cfg.AllowedSigners != "" {
		if _, err := os.Stat(cfg.AllowedSigners); err != nil {
			return fmt.Errorf("allowed signers file: %w", err)
		}
		abs, err := filepath.Abs(cfg.AllowedSigners)
		if err != nil {
			return fmt.Errorf("allowed signers file: %w", err)
		}
		cfg.AllowedSigners = abs
		repo.SetAllowedSigners(abs)
	}

	var filter *git.PathFilter
	if len(cfg.Paths) > 0 {
		if filter, err = git.NewPathFilter(cfg.Paths); err != nil {
			return err
		}
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
		Scope:     scopeText(cfg),
		DryRun:    cfg.DryRun,
	})
	if len(repo.Shallow) > 0 {
		out.ShallowRepository(len(repo.Shallow))
	}

	p, err := buildPlan(ctx, out, repo, outDir, cfg)
	if err != nil {
		return err
	}
	if p.pullHint > 0 {
		out.PullRefsNotExtracted(p.pullHint)
	}

	var runErr error
	if err := p.recoverRewritten(ctx, out, repo, outDir, cfg); err != nil {
		runErr = err
	}
	p.detectAnomalies(len(repo.Shallow) > 0, cfg.Thresholds)

	if cfg.DryRun {
		return dryRun(ctx, out, repo, cfg, filter, p)
	}

	opts := git.ExtractOptions{PreserveModes: cfg.PreserveModes, NoTree: cfg.NoTree, Paths: filter}
	if cfg.NoTree {
		// Most blobs are shared between commits, so hashing each once is what
		// makes checksums affordable without writing any tree.
		opts.Digests = git.NewDigestCache()
	}

	var extractErr error
	if cfg.Dedupe {
		extractErr = p.extractDeduplicated(ctx, repo, outDir, cfg, opts)
	} else {
		extractErr = p.extractPerRef(ctx, out, repo, cfg, opts)
	}
	if runErr == nil {
		runErr = extractErr
	}

	// Written even for a partial run: a folder of snapshots that cannot say
	// where it came from is not evidence.
	reportErr := writeReports(ctx, out, outDir, cfg, repo, startedAt, p)
	if runErr == nil {
		runErr = reportErr
	} else if reportErr != nil {
		runErr = errors.Join(runErr, reportErr)
	}

	out.Summary(outDir, outcomes(p.runs), cfg.Verbose)
	return runErr
}

// scopeText describes a non-default ref selection for the banner.
func scopeText(cfg Config) string {
	switch {
	case cfg.AllRefs:
		return "all except " + strings.Join(snapshot.ExcludedFromAllRefs, ", ")
	case len(cfg.Refs) > 0:
		return strings.Join(cfg.Refs, ", ")
	}
	return ""
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

	// A dry run writes nothing, so it neither needs nor creates the directory.
	if cfg.DryRun {
		return outDir, nil
	}

	if info, err := os.Stat(outDir); err == nil && info.IsDir() {
		return "", fmt.Errorf("output directory already exists: %s", outDir)
	}

	// Created before any work starts. An unwritable destination is otherwise
	// only discovered once extraction is under way, and then it surfaces once
	// per snapshot and once per root record — many messages for one cause,
	// after the run has already spent its time.
	if err := os.MkdirAll(outDir, outputDirPerms); err != nil {
		return "", fmt.Errorf("cannot create output directory %s: %w", outDir, err)
	}
	return outDir, nil
}
