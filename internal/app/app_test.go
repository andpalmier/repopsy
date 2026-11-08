package app

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/console"
	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/gittest"
)

func TestWriteReportsReturnsWriteErrors(t *testing.T) {
	b := gittest.New(t).WithCommits(1)
	repo, err := git.Open(b.Dir)
	if err != nil {
		t.Fatal(err)
	}
	outDir := filepath.Join(t.TempDir(), "out")
	if err := os.WriteFile(outDir, []byte("not a directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &plan{commits: map[string]*git.Commit{}}
	if err := writeReports(context.Background(), console.New(io.Discard), outDir, Config{}, repo, time.Now(), p); err == nil {
		t.Fatal("writeReports returned nil despite unwritable output")
	}
}

func TestDedupeLinkFailureIsReturnedAndSnapshotStaysRecorded(t *testing.T) {
	b := gittest.New(t).WithCommits(1)
	repo, err := git.Open(b.Dir)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commit := listed.Commits[0]
	outDir := t.TempDir()
	blocked := filepath.Join(outDir, "blocked")
	if err := os.WriteFile(blocked, []byte("file, not directory"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := &plan{
		commits: map[string]*git.Commit{commit.Hash: &commit},
		order:   []string{commit.Hash},
		runs: []branchRun{{
			label:  "main",
			dir:    blocked,
			hashes: []string{commit.Hash},
		}},
	}
	err = p.extractDeduplicated(context.Background(), repo, outDir, Config{Workers: 1, Writer: io.Discard}, git.ExtractOptions{})
	if err == nil {
		t.Fatal("dedupe link failure was not returned")
	}
	if len(p.runs[0].results) != 1 || !p.runs[0].results[0].Stored {
		t.Fatalf("stored result = %+v", p.runs[0].results)
	}
	if got := p.extracted(); len(got) != 1 || got[0].Hash != commit.Hash {
		t.Fatalf("extracted = %+v", got)
	}
}
