package git

import (
	"context"
	"strings"
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

func TestListCommitsKeepsRecordSeparatorInsideMessage(t *testing.T) {
	b := newRepo(t)
	b.Write("a.txt", "x\n", 0o644)
	b.Git("add", "-A")
	tree := b.Git("write-tree")
	hash := b.gitStdin("subject\n\nbefore\x1eafter\n", "commit-tree", tree)
	b.Git("update-ref", "refs/heads/main", hash)

	listed, err := b.open().ListCommits(context.Background(), ListOptions{Branch: "main"})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(listed.Commits))
	}
	if !strings.Contains(listed.Commits[0].FullMessage, "before\x1eafter") {
		t.Errorf("FullMessage lost the separator: %q", listed.Commits[0].FullMessage)
	}
}

func TestListCommitsKeepsNonASCIIAndNewlinePaths(t *testing.T) {
	b := newRepo(t)
	b.Write("café.txt", "coffee\n", 0o644)
	b.Write("line\nbreak.txt", "newline\n", 0o644)
	b.Commit("paths")

	listed, err := b.open().ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Commits) != 1 {
		t.Fatalf("got %d commits, want 1", len(listed.Commits))
	}
	paths := map[string]bool{}
	for _, f := range listed.Commits[0].Files {
		paths[f.Path] = true
	}
	for _, want := range []string{"café.txt", "line\nbreak.txt"} {
		if !paths[want] {
			t.Errorf("path %q missing from %v", want, paths)
		}
	}
}

func TestListCommitsParsesRenameTokens(t *testing.T) {
	b := newRepo(t)
	b.Write("old.txt", "old\n", 0o644)
	b.Commit("add")
	b.Git("mv", "old.txt", "new.txt")
	b.Git("add", "-A")
	b.Commit("rename")

	listed, err := b.open().ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if len(listed.Commits) != 2 {
		t.Fatalf("got %d commits, want 2", len(listed.Commits))
	}
	files := listed.Commits[0].Files
	if len(files) != 1 || files[0].OldPath != "old.txt" || files[0].Path != "new.txt" {
		t.Fatalf("rename files = %+v", files)
	}
}
