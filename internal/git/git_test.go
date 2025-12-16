package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// setupTestRepo creates a temporary git repository with some commits
func setupTestRepo(t *testing.T) *Repository {
	dir, err := os.MkdirTemp("", "repopsy-test-")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	// Cleanup
	t.Cleanup(func() { _ = os.RemoveAll(dir) })

	run := func(args ...string) {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v failed: %v\nOutput: %s", args, err, out)
		}
	}

	run("init")
	run("config", "user.name", "Test User")
	run("config", "user.email", "test@example.com")

	// Commit 1
	if err := os.WriteFile(filepath.Join(dir, "file1.txt"), []byte("content1"), 0644); err != nil {
		t.Fatalf("failed to write file1: %v", err)
	}
	run("add", "file1.txt")
	run("commit", "-m", "Initial commit")

	// Commit 2: Special char in subject
	if err := os.WriteFile(filepath.Join(dir, "file2.txt"), []byte("content2"), 0644); err != nil {
		t.Fatalf("failed to write file2: %v", err)
	}
	run("add", "file2.txt")
	run("commit", "-m", "Commit with | pipe")

	repo, err := Open(dir)
	if err != nil {
		t.Fatalf("failed to open repo: %v", err)
	}

	return repo
}

func TestListCommits(t *testing.T) {
	repo := setupTestRepo(t)

	commits, err := repo.ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits failed: %v", err)
	}

	if len(commits) != 2 {
		t.Errorf("expected 2 commits, got %d", len(commits))
	}

	// Verify parsing of special char
	if commits[0].Subject != "Commit with | pipe" {
		t.Errorf("expected subject 'Commit with | pipe', got '%s'", commits[0].Subject)
	}
}

func TestContextCancellation(t *testing.T) {
	repo := setupTestRepo(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := repo.ListCommits(ctx, ListOptions{})
	if err == nil {
		t.Error("expected error due to cancelled context, got nil")
	}
}

func TestCommitCount(t *testing.T) {
	repo := setupTestRepo(t)

	count, err := repo.CommitCount(context.Background(), "")
	if err != nil {
		t.Fatalf("CommitCount failed: %v", err)
	}

	if count != 2 {
		t.Errorf("expected count 2, got %d", count)
	}
}
