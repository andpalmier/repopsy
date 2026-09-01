package extractor

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/internal/git"
	"github.com/andpalmier/repopsy/internal/snapshot"
)

// setupRepo builds a temporary repository with n commits, each adding one file.
func setupRepo(t *testing.T, n int) *git.Repository {
	t.Helper()
	dir := t.TempDir()

	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
	}

	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")
	// Independent of the machine's global git config.
	run("config", "commit.gpgsign", "false")

	for i := range n {
		name := "file" + string(rune('a'+i)) + ".txt"
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", name)
		run("commit", "-m", "commit "+string(rune('a'+i)))
	}

	repo, err := git.Open(dir)
	if err != nil {
		t.Fatalf("git.Open: %v", err)
	}
	return repo
}

func TestRunProducesOneSnapshotPerCommit(t *testing.T) {
	repo := setupRepo(t, 4)
	commits, err := repo.ListCommits(context.Background(), git.ListOptions{Reverse: true})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if len(commits) != 4 {
		t.Fatalf("expected 4 commits, got %d", len(commits))
	}

	outDir := filepath.Join(t.TempDir(), "out")
	var progressOut strings.Builder

	ext := New(repo, Config{OutputDir: outDir, Workers: 3, Writer: &progressOut})
	results, err := ext.Run(context.Background(), commits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(results) != len(commits) {
		t.Fatalf("got %d results, want %d", len(results), len(commits))
	}

	// Results are index-addressed, so they must come back in commit order
	// regardless of which worker finished first.
	for i, r := range results {
		if r.Error != nil {
			t.Errorf("result %d: %v", i, r.Error)
		}
		if r.Commit.Hash != commits[i].Hash {
			t.Errorf("result %d is commit %s, want %s", i, r.Commit.ShortHash, commits[i].ShortHash)
		}
		if want := snapshot.Path(outDir, "", commits[i]); r.OutputPath != want {
			t.Errorf("result %d path = %q, want %q", i, r.OutputPath, want)
		}

		metadata := filepath.Join(r.OutputPath, snapshot.MetadataFilename)
		if _, err := os.Stat(metadata); err != nil {
			t.Errorf("result %d: missing %s: %v", i, snapshot.MetadataFilename, err)
		}
	}

	// Every commit adds one file, so the last snapshot holds all four plus the
	// metadata file.
	entries, err := os.ReadDir(results[3].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 5 {
		t.Errorf("last snapshot has %d entries, want 5 (4 files + metadata)", len(entries))
	}

	// Progress went to the injected writer, not to the process's stderr.
	if progressOut.Len() == 0 {
		t.Error("expected progress output on the configured writer")
	}
}

func TestRunNestsUnderBranch(t *testing.T) {
	repo := setupRepo(t, 1)
	commits, err := repo.ListCommits(context.Background(), git.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	ext := New(repo, Config{OutputDir: outDir, Branch: "feat/x", Workers: 1, Writer: &strings.Builder{}})
	results, err := ext.Run(context.Background(), commits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := filepath.Join(outDir, "feat", "x")
	if got := filepath.Dir(results[0].OutputPath); got != want {
		t.Errorf("snapshot parent = %q, want %q", got, want)
	}
	if _, err := os.Stat(filepath.Join(results[0].OutputPath, snapshot.MetadataFilename)); err != nil {
		t.Errorf("missing metadata: %v", err)
	}
}

func TestRunWithNoCommits(t *testing.T) {
	repo := setupRepo(t, 1)
	ext := New(repo, Config{OutputDir: t.TempDir(), Writer: &strings.Builder{}})

	results, err := ext.Run(context.Background(), nil)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected no results, got %d", len(results))
	}
}

// TestRunCancelledContextDispatchesNothing checks that a cancelled run reports
// only commits it actually attempted, rather than zero-valued results that
// would be counted as successes.
func TestRunCancelledContextDispatchesNothing(t *testing.T) {
	repo := setupRepo(t, 3)
	commits, err := repo.ListCommits(context.Background(), git.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	ext := New(repo, Config{OutputDir: filepath.Join(t.TempDir(), "out"), Workers: 2, Writer: &strings.Builder{}})
	results, _ := ext.Run(ctx, commits)

	for i, r := range results {
		if r.Commit.Hash == "" {
			t.Errorf("result %d is a zero value; it would be counted as a success", i)
		}
	}
}

func TestNewDefaultsWorkers(t *testing.T) {
	ext := New(nil, Config{Workers: 0})
	if ext.config.Workers < 1 {
		t.Errorf("Workers = %d, want at least 1", ext.config.Workers)
	}
}
