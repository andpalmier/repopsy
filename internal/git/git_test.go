package git

import (
	"context"
	"testing"
)

// setupTestRepo creates a repository with two commits, the second carrying a
// character that has tripped up field-separated parsing before.
func setupTestRepo(t *testing.T) *Repository {
	t.Helper()
	b := newRepo(t)
	b.Write("file1.txt", "content1", 0o644)
	b.Commit("Initial commit")
	b.Write("file2.txt", "content2", 0o644)
	b.Commit("Commit with | pipe")
	return b.open()
}

func TestListCommits(t *testing.T) {
	repo := setupTestRepo(t)

	listed, err := repo.ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits failed: %v", err)
	}
	commits := listed.Commits

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
