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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	hooks := filepath.Join(b.dir, ".git", "hooks")
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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	hooks := filepath.Join(b.dir, ".git", "hooks")
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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")
	if err := os.RemoveAll(filepath.Join(b.dir, ".git", "hooks")); err != nil {
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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	if got := b.open().GitDir; !strings.HasSuffix(got, ".git") {
		t.Errorf("GitDir = %q, want a path ending in .git", got)
	}

	// A bare repository is its own git directory.
	bare := filepath.Join(t.TempDir(), "bare.git")
	b.git("clone", "--bare", b.dir, bare)
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
	b.write("f.txt", "v1\n", 0o644)
	b.commit("one")
	b.write("f.txt", "v2\n", 0o644)
	lost := b.commit("SENSITIVE")
	b.git("reset", "--hard", "HEAD~1")
	b.write("f.txt", "v2again\n", 0o644)
	b.commit("replacement")

	repo := b.open()
	branch := b.git("rev-parse", "--abbrev-ref", "HEAD")

	reachable, err := repo.ListCommits(context.Background(), ListOptions{Branch: branch})
	if err != nil {
		t.Fatal(err)
	}
	rewritten, err := repo.RewrittenCommits(context.Background(), branch, reachable, 0)
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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	bare := filepath.Join(t.TempDir(), "bare.git")
	b.git("clone", "--bare", b.dir, bare)
	repo, err := Open(bare)
	if err != nil {
		t.Fatal(err)
	}

	// Reflogs are local, so a bare clone has nothing to recover from.
	rewritten, err := repo.RewrittenCommits(context.Background(), "main", nil, 0)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(rewritten) != 0 {
		t.Errorf("got %d rewritten commits from a bare clone, want none", len(rewritten))
	}
}
