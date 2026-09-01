package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestReadStateCapturesHooksAndConfig(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	hooks := filepath.Join(b.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	// An executable hook, a non-executable one, and a sample git never runs.
	if err := os.WriteFile(filepath.Join(hooks, "pre-commit"), []byte("#!/bin/sh\nexfil\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "post-commit"), []byte("#!/bin/sh\ninert\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(hooks, "update.sample"), []byte("sample\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := b.open().ReadState()
	if err != nil {
		t.Fatalf("ReadState: %v", err)
	}

	if !strings.Contains(state.Config, "[core]") {
		t.Errorf("config not captured verbatim: %q", state.Config)
	}

	if len(state.Hooks) != 2 {
		t.Fatalf("got %d hooks, want 2 (the .sample is excluded): %+v", len(state.Hooks), state.Hooks)
	}
	byName := map[string]Hook{}
	for _, h := range state.Hooks {
		byName[h.Name] = h
	}

	pre, ok := byName["pre-commit"]
	if !ok {
		t.Fatal("pre-commit hook missing")
	}
	if !pre.Executable {
		t.Error("pre-commit should be executable")
	}
	if !strings.Contains(pre.Content, "exfil") {
		t.Errorf("hook content not recorded: %q", pre.Content)
	}
	if len(pre.SHA256) != 64 {
		t.Errorf("hook hash = %q", pre.SHA256)
	}

	if post := byName["post-commit"]; post.Executable {
		t.Error("post-commit should be reported as non-executable")
	}
	if _, found := byName["update.sample"]; found {
		t.Error("git's inert *.sample hooks should be excluded")
	}
}

func TestReadStateTruncatesAHugeHook(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	hooks := filepath.Join(b.Dir, ".git", "hooks")
	if err := os.MkdirAll(hooks, 0o755); err != nil {
		t.Fatal(err)
	}
	big := strings.Repeat("A", maxHookBytes*2)
	if err := os.WriteFile(filepath.Join(hooks, "pre-push"), []byte(big), 0o755); err != nil {
		t.Fatal(err)
	}

	state, err := b.open().ReadState()
	if err != nil {
		t.Fatal(err)
	}
	if len(state.Hooks) != 1 {
		t.Fatalf("got %d hooks, want 1", len(state.Hooks))
	}
	h := state.Hooks[0]
	if !h.Truncated {
		t.Error("expected the hook to be marked truncated")
	}
	if len(h.Content) != maxHookBytes {
		t.Errorf("content is %d bytes, want %d", len(h.Content), maxHookBytes)
	}
	// The size and hash must describe the whole file, not the excerpt.
	if h.Size != int64(len(big)) {
		t.Errorf("Size = %d, want %d", h.Size, len(big))
	}
}

func TestReadStateNoHooksDirectory(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")
	if err := os.RemoveAll(filepath.Join(b.Dir, ".git", "hooks")); err != nil {
		t.Fatal(err)
	}

	state, err := b.open().ReadState()
	if err != nil {
		t.Errorf("a missing hooks directory should not be an error: %v", err)
	}
	if len(state.Hooks) != 0 {
		t.Errorf("got %d hooks, want none", len(state.Hooks))
	}
}

func TestGitDirIsResolved(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	if got := b.open().GitDir; !strings.HasSuffix(got, ".git") {
		t.Errorf("GitDir = %q, want a path ending in .git", got)
	}

	// A bare repository is its own git directory.
	bare := filepath.Join(t.TempDir(), "bare.git")
	b.Git("clone", "--bare", b.Dir, bare)
	repo, err := Open(bare)
	if err != nil {
		t.Fatal(err)
	}
	if repo.GitDir == "" {
		t.Error("a bare repository should still resolve a git directory")
	}
}

func TestRewrittenCommitsRecoversWhatAResetRemoved(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "v1\n", 0o644)
	b.Commit("one")
	b.Write("f.txt", "v2\n", 0o644)
	lost := b.Commit("SENSITIVE")
	b.Git("reset", "--hard", "HEAD~1")
	b.Write("f.txt", "v2again\n", 0o644)
	b.Commit("replacement")

	repo := b.open()
	branch := b.Git("rev-parse", "--abbrev-ref", "HEAD")

	listed, err := repo.ListCommits(context.Background(), ListOptions{Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	reachable := listed.Commits
	exclude := map[string]bool{}
	for _, c := range reachable {
		exclude[c.Hash] = true
	}
	rewritten, err := repo.RewrittenCommits(context.Background(), branch, exclude, 0)
	if err != nil {
		t.Fatalf("RewrittenCommits: %v", err)
	}

	var found bool
	for _, c := range rewritten {
		if c.Hash == lost {
			found = true
			if !c.Unreachable {
				t.Error("a recovered commit must be marked unreachable")
			}
		}
		// Nothing already reachable may be returned, or it would be extracted twice.
		for _, r := range reachable {
			if c.Hash == r.Hash {
				t.Errorf("%s is reachable and must not be reported as rewritten", c.ShortHash)
			}
		}
	}
	if !found {
		t.Errorf("the reset-away commit %s was not recovered", lost[:7])
	}
}

func TestRewrittenCommitsEmptyWithoutAReflog(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	bare := filepath.Join(t.TempDir(), "bare.git")
	b.Git("clone", "--bare", b.Dir, bare)
	repo, err := Open(bare)
	if err != nil {
		t.Fatal(err)
	}

	// Reflogs are local, so a bare clone has nothing to recover from.
	rewritten, err := repo.RewrittenCommits(context.Background(), "main", map[string]bool{}, 0)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("got %d rewritten commits from a bare clone, want none", len(rewritten))
	}
}

// TestRewrittenCommitsFromHeadFindsDetachedWork covers the gap between the two
// records: HEAD's reflog remembers work abandoned on a detached head, which no
// branch reaches and no branch reflog recovers.
func TestRewrittenCommitsFromHeadFindsDetachedWork(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "v1\n", 0o644)
	b.Commit("one")

	// Captured before detaching: "git branch" adds a pseudo-entry once detached.
	branch := b.Git("rev-parse", "--abbrev-ref", "HEAD")

	b.Git("checkout", "--detach")
	b.Write("g.txt", "detached\n", 0o644)
	abandoned := b.Commit("DETACHED work")
	b.Git("checkout", branch)

	repo := b.open()

	listed, err := repo.ListCommits(context.Background(), ListOptions{Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	reachable := listed.Commits
	exclude := map[string]bool{}
	for _, c := range reachable {
		exclude[c.Hash] = true
	}

	// The branch's own reflog never held it.
	fromBranch, err := repo.RewrittenCommits(context.Background(), branch, exclude, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range fromBranch {
		if c.Hash == abandoned {
			t.Fatal("the branch reflog should not know about detached work")
		}
	}

	// HEAD's reflog does.
	fromHead, err := repo.RewrittenCommits(context.Background(), "HEAD", exclude, 0)
	if err != nil {
		t.Fatal(err)
	}
	var found bool
	for _, c := range fromHead {
		if c.Hash == abandoned {
			found = true
			if !c.Unreachable {
				t.Error("recovered detached work must be marked unreachable")
			}
		}
	}
	if !found {
		t.Errorf("HEAD reflog did not recover the abandoned commit %s", abandoned[:7])
	}
}

// TestRewrittenCommitsRespectsExclusion stops a commit several reflogs remember
// from being recovered more than once.
func TestRewrittenCommitsRespectsExclusion(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "v1\n", 0o644)
	b.Commit("one")
	b.Write("f.txt", "v2\n", 0o644)
	lost := b.Commit("lost")
	b.Git("reset", "--hard", "HEAD~1")

	repo := b.open()
	all, err := repo.RewrittenCommits(context.Background(), "HEAD", map[string]bool{}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(all) == 0 {
		t.Fatal("expected something to recover")
	}

	// Excluding it removes it from the result.
	excluded, err := repo.RewrittenCommits(context.Background(), "HEAD", map[string]bool{lost: true}, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, c := range excluded {
		if c.Hash == lost {
			t.Error("an excluded commit was recovered anyway")
		}
	}
	if len(excluded) >= len(all) {
		t.Errorf("exclusion had no effect: %d vs %d", len(excluded), len(all))
	}
}
