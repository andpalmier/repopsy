package extractor

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/gittest"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// setupRepo builds a temporary repository with n commits, each adding one file.
func setupRepo(t *testing.T, n int) *git.Repository {
	t.Helper()
	built := gittest.New(t).WithCommits(n)
	repo, err := git.Open(built.Dir)
	if err != nil {
		t.Fatalf("git.Open: %v", err)
	}
	return repo
}

func TestRunProducesOneSnapshotPerCommit(t *testing.T) {
	repo := setupRepo(t, 4)
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{Reverse: true})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	commits := listed.Commits
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

		for _, name := range []string{snapshot.MetadataFilename, snapshot.ChecksumFilename} {
			if _, err := os.Stat(filepath.Join(r.OutputPath, name)); err != nil {
				t.Errorf("result %d: missing %s: %v", i, name, err)
			}
		}
	}

	// A snapshot holds its two records and one tree directory, whatever the
	// commit contains — that separation is what stops a file called
	// COMMIT_INFO.txt from overwriting the record of the same name.
	entries, err := os.ReadDir(results[3].OutputPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 3 {
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		t.Errorf("snapshot has %d entries (%v), want 3: metadata, checksums, tree", len(entries), names)
	}

	// Every commit adds one file, so the last commit's tree holds all four.
	treeEntries, err := os.ReadDir(snapshot.TreePath(results[3].OutputPath))
	if err != nil {
		t.Fatal(err)
	}
	if len(treeEntries) != 4 {
		t.Errorf("last tree has %d files, want 4", len(treeEntries))
	}

	// Progress went to the injected writer, not to the process's stderr.
	if progressOut.Len() == 0 {
		t.Error("expected progress output on the configured writer")
	}
}

func TestRunNestsUnderBranch(t *testing.T) {
	repo := setupRepo(t, 1)
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commits := listed.Commits

	outDir := filepath.Join(t.TempDir(), "out")
	ext := New(repo, Config{OutputDir: outDir, Branch: "feat/x", Workers: 1, Writer: &strings.Builder{}})
	results, err := ext.Run(context.Background(), commits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	want := filepath.Join(outDir, snapshot.RefsDir, "feat", "x")
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
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commits := listed.Commits

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

// TestRunChecksumsMatchTheExtractedFiles verifies the integrity record against
// the bytes actually on disk. A checksum file that does not match what it
// describes is worse than none at all.
func TestRunChecksumsMatchTheExtractedFiles(t *testing.T) {
	repo := setupRepo(t, 3)
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{Reverse: true})
	if err != nil {
		t.Fatal(err)
	}
	commits := listed.Commits

	outDir := filepath.Join(t.TempDir(), "out")
	ext := New(repo, Config{OutputDir: outDir, Workers: 2, Writer: &strings.Builder{}})
	results, err := ext.Run(context.Background(), commits)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	for _, r := range results {
		sums, err := os.ReadFile(filepath.Join(r.OutputPath, snapshot.ChecksumFilename))
		if err != nil {
			t.Fatalf("%s: %v", snapshot.ChecksumFilename, err)
		}

		lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
		if len(lines) == 0 || lines[0] == "" {
			t.Errorf("%s is empty for %s", snapshot.ChecksumFilename, r.Commit.ShortHash)
			continue
		}

		for _, line := range lines {
			want, path, found := strings.Cut(line, "  ")
			if !found {
				t.Errorf("malformed checksum line %q", line)
				continue
			}
			content, err := os.ReadFile(filepath.Join(r.OutputPath, path))
			if err != nil {
				t.Errorf("%s named in %s but absent: %v", path, snapshot.ChecksumFilename, err)
				continue
			}
			sum := sha256.Sum256(content)
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Errorf("%s: checksum %s does not match content hash %s", path, want, got)
			}
		}

		// The metadata and checksum files describe the tree, so they are not
		// themselves listed.
		if strings.Contains(string(sums), snapshot.ChecksumFilename) {
			t.Errorf("%s lists itself", snapshot.ChecksumFilename)
		}
	}
}
