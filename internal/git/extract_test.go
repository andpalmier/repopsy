package git

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/internal/gittest"
)

// newRepo wraps the shared builder and adds opening it as a Repository, which
// gittest cannot do without importing this package.
type repo struct {
	*gittest.Repo
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	return &repo{gittest.New(t)}
}

func (r *repo) open() *Repository {
	repository, err := Open(r.Dir)
	if err != nil {
		r.Git("status") // surface anything useful before failing
		panic(err)
	}
	return repository
}

func TestExtractCommit(t *testing.T) {
	b := newRepo(t)
	b.Write("readme.md", "hello\n", 0o644)
	b.Write("src/deep/nested.go", "package deep\n", 0o644)
	b.Write("scripts/run.sh", "#!/bin/sh\necho hi\n", 0o755)
	b.Symlink("readme.md", "link.md")
	hash := b.Commit("everything")

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}

	// Contents arrive intact, including through nested directories.
	for path, want := range map[string]string{
		"readme.md":          "hello\n",
		"src/deep/nested.go": "package deep\n",
		"scripts/run.sh":     "#!/bin/sh\necho hi\n",
	} {
		got, err := os.ReadFile(filepath.Join(dest, path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		if string(got) != want {
			t.Errorf("%s = %q, want %q", path, got, want)
		}
	}

	// The executable bit is part of the evidence.
	info, err := os.Stat(filepath.Join(dest, "scripts/run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("scripts/run.sh mode = %v, want the executable bit set", info.Mode().Perm())
	}

	// Symlinks stay symlinks rather than being flattened into copies.
	linkInfo, err := os.Lstat(filepath.Join(dest, "link.md"))
	if err != nil {
		t.Fatalf("link.md: %v", err)
	}
	if linkInfo.Mode()&os.ModeSymlink == 0 {
		t.Errorf("link.md is not a symlink (mode %v)", linkInfo.Mode())
	} else if target, err := os.Readlink(filepath.Join(dest, "link.md")); err != nil || target != "readme.md" {
		t.Errorf("link.md -> %q (err %v), want readme.md", target, err)
	}
}

func TestExtractCommitCreatesDestination(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	hash := b.Commit("one")

	// Two levels that do not exist yet.
	dest := filepath.Join(t.TempDir(), "a", "b", "snap")
	if _, err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected f.txt under a freshly created path: %v", err)
	}
}

func TestExtractCommitUnknownHash(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	dest := filepath.Join(t.TempDir(), "snap")
	_, err := b.open().ExtractCommit(context.Background(), "0000000000000000000000000000000000000000", dest)
	if err == nil {
		t.Fatal("expected an error for an unknown hash")
	}
	// git's own diagnosis is the useful part, so it must reach the caller.
	if !strings.Contains(err.Error(), "ls-tree") && !strings.Contains(err.Error(), "Not a valid") {
		t.Errorf("error does not carry git's diagnosis: %v", err)
	}
}

func TestExtractCommitCancelledContext(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	hash := b.Commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().ExtractCommit(ctx, hash, dest); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestExtractCommitEmptyCommit(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")
	b.Git("commit", "--allow-empty", "-m", "empty")
	hash := b.Git("rev-parse", "HEAD")

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}
	// An empty commit still has the tree of its parent.
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected the parent's tree: %v", err)
	}
}

// TestExtractIgnoresExportIgnore is the inversion of a test that used to assert
// the opposite. A repository could withhold files from its own snapshot with
// .gitattributes export-ignore, because git archive honours it and offers no way
// to override it. Reading the tree with ls-tree and cat-file bypasses attributes
// entirely, so the subject of an investigation no longer chooses what is
// collected. See docs/adr/0005.
func TestExtractIgnoresExportIgnore(t *testing.T) {
	b := newRepo(t)
	b.Write("normal.txt", "public\n", 0o644)
	b.Write("secret.txt", "withheld\n", 0o644)
	b.Write(".gitattributes", "secret.txt export-ignore\n", 0o644)
	hash := b.Commit("with export-ignore")

	repo := b.open()
	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := repo.ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}

	// The commit records the file, and now so does the snapshot.
	listed, err := repo.ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	commits := listed.Commits
	var recorded bool
	for _, f := range commits[0].Files {
		if f.Path == "secret.txt" {
			recorded = true
		}
	}
	if !recorded {
		t.Error("secret.txt is missing from the commit's recorded file list")
	}

	content, err := os.ReadFile(filepath.Join(dest, "secret.txt"))
	if err != nil {
		t.Fatalf("secret.txt was withheld from the snapshot: %v", err)
	}
	if string(content) != "withheld\n" {
		t.Errorf("secret.txt = %q", content)
	}
	if _, err := os.Stat(filepath.Join(dest, "normal.txt")); err != nil {
		t.Errorf("normal.txt should still be extracted: %v", err)
	}
}

func TestExtractRecordsSubmodulePointers(t *testing.T) {
	// A second repository to point at.
	inner := newRepo(t)
	inner.Write("inner.txt", "inner\n", 0o644)
	innerHash := inner.Commit("inner one")

	b := newRepo(t)
	b.Write("outer.txt", "outer\n", 0o644)
	b.Commit("outer one")
	b.Git("-c", "protocol.file.allow=always", "submodule", "add", "-q", inner.Dir, "sub")
	hash := b.Commit("add submodule")

	dest := filepath.Join(t.TempDir(), "snap")
	result, err := b.open().ExtractCommit(context.Background(), hash, dest)
	if err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}

	if len(result.Submodules) != 1 {
		t.Fatalf("got %d submodules, want 1: %+v", len(result.Submodules), result.Submodules)
	}
	sub := result.Submodules[0]
	if sub.Path != "sub" {
		t.Errorf("submodule path = %q, want sub", sub.Path)
	}
	if sub.Commit != innerHash {
		t.Errorf("submodule commit = %q, want %q", sub.Commit, innerHash)
	}

	// The pointer is recorded; the content genuinely is not there.
	if entries, err := os.ReadDir(filepath.Join(dest, "sub")); err == nil && len(entries) > 0 {
		t.Errorf("submodule content unexpectedly present: %v", entries)
	}
}

func TestExtractRefusesEscapingPaths(t *testing.T) {
	// git will not create such an entry, so the guard is checked directly.
	root := t.TempDir()
	if _, err := safeJoin(root, "../escape.txt"); err == nil {
		t.Error("expected ../escape.txt to be refused")
	}
	if _, err := safeJoin(root, "ok/inside.txt"); err != nil {
		t.Errorf("a normal path was refused: %v", err)
	}
}

// TestExtractCommitReturnsOnWriteFailure guards a deadlock: writeTree streams
// blob content from cat-file, so returning early leaves the child mid-write with
// nobody draining stdout and the request goroutine mid-write to stdin. Both
// block and the deferred Wait never returns. Needs more than 64KB of content so
// the pipe buffer actually fills.
func TestExtractCommitReturnsOnWriteFailure(t *testing.T) {
	b := newRepo(t)
	for _, name := range []string{"a.bin", "b.bin", "c.bin", "d.bin"} {
		b.Write(name, strings.Repeat("x", 80*1024), 0o644)
	}
	hash := b.Commit("big files")

	dest := filepath.Join(t.TempDir(), "snap")
	// A directory where a file must go, so writing it fails.
	if err := os.MkdirAll(filepath.Join(dest, "a.bin"), 0o755); err != nil {
		t.Fatal(err)
	}

	done := make(chan error, 1)
	go func() {
		_, err := b.open().ExtractCommit(context.Background(), hash, dest)
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a write error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("ExtractCommit deadlocked instead of returning an error")
	}
}

func TestListBranches(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")
	b.Git("branch", "feat/x")
	b.Git("branch", "zzz")

	branches, err := b.open().ListBranches(context.Background())
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}

	got := strings.Join(branches, ",")
	// for-each-ref sorts by refname.
	if want := "feat/x,main,zzz"; got != want && got != "feat/x,master,zzz" {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestListBranchesEmptyRepository(t *testing.T) {
	b := newRepo(t) // initialised, no commits, so no branch ref exists yet

	branches, err := b.open().ListBranches(context.Background())
	if err != nil {
		t.Fatalf("ListBranches: %v", err)
	}
	if len(branches) != 0 {
		t.Errorf("branches = %v, want none", branches)
	}
}

func TestListBranchesCancelledContext(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := b.open().ListBranches(ctx); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

// TestOpenReportsGitsOwnDiagnosis covers a misleading error. resolveRoot used
// to answer every failure with "not a git repository", which sends the reader
// to check the path even when git refused for an unrelated reason — the
// dubious-ownership refusal on a bind-mounted repository being the case that
// found this.
func TestOpenReportsGitsOwnDiagnosis(t *testing.T) {
	_, err := Open(t.TempDir())
	if err == nil {
		t.Fatal("expected an error for a directory holding no repository")
	}
	// git's own words must reach the caller.
	if !strings.Contains(err.Error(), "fatal:") {
		t.Errorf("error does not carry git's diagnosis: %v", err)
	}
	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error should still identify the cause: %v", err)
	}
}
