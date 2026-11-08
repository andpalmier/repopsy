package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/gittest"
)

// newRepo wraps the shared builder and adds opening it as a Repository, which
// gittest cannot do without importing this package.
type repo struct {
	*gittest.Repo
	t *testing.T
}

func newRepo(t *testing.T) *repo {
	t.Helper()
	return &repo{gittest.New(t), t}
}

func (r *repo) open() *Repository {
	repository, err := Open(r.Dir)
	if err != nil {
		r.Git("status") // surface anything useful before failing
		panic(err)
	}
	return repository
}

func listBranchNames(t *testing.T, ctx context.Context, repo *Repository) ([]string, error) {
	t.Helper()
	refs, err := repo.ListRefs(ctx, []string{"refs/heads/"})
	if err != nil {
		return nil, err
	}
	names := make([]string, 0, len(refs))
	for _, ref := range refs {
		if name, ok := ref.Branch(); ok {
			names = append(names, name)
		}
	}
	return names, nil
}

func TestExtract(t *testing.T) {
	b := newRepo(t)
	b.Write("readme.md", "hello\n", 0o644)
	b.Write("src/deep/nested.go", "package deep\n", 0o644)
	b.Write("scripts/run.sh", "#!/bin/sh\necho hi\n", 0o755)
	b.Symlink("readme.md", "link.md")
	hash := b.Commit("everything")

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{}); err != nil {
		t.Fatalf("Extract: %v", err)
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

	// The tree comes from an untrusted repository, so nothing in it is
	// executable by default.
	info, err := os.Stat(filepath.Join(dest, "scripts/run.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm()&0o111 != 0 {
		t.Errorf("scripts/run.sh mode = %v, want no executable bit", info.Mode().Perm())
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

func TestExtractLargeFileStreamsToDisk(t *testing.T) {
	b := newRepo(t)
	content := strings.Repeat("A", 4<<20)
	b.Write("large.bin", content, 0o644)
	hash := b.Commit("large")

	dest := filepath.Join(t.TempDir(), "snap")
	res, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(dest, "large.bin"))
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != content {
		t.Errorf("large file content changed: got %d bytes, want %d", len(got), len(content))
	}
	if len(res.Digests) != 1 {
		t.Fatalf("digests = %+v", res.Digests)
	}
	sum := sha256.Sum256([]byte(content))
	if res.Digests[0].SHA256 != hex.EncodeToString(sum[:]) {
		t.Errorf("digest = %s, want %s", res.Digests[0].SHA256, hex.EncodeToString(sum[:]))
	}
}

func TestExtractCreatesDestination(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	hash := b.Commit("one")

	// Two levels that do not exist yet.
	dest := filepath.Join(t.TempDir(), "a", "b", "snap")
	if _, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected f.txt under a freshly created path: %v", err)
	}
}

func TestExtractUnknownHash(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	dest := filepath.Join(t.TempDir(), "snap")
	_, err := b.open().Extract(context.Background(), "0000000000000000000000000000000000000000", dest, ExtractOptions{})
	if err == nil {
		t.Fatal("expected an error for an unknown hash")
	}
	// git's own diagnosis is the useful part, so it must reach the caller.
	if !strings.Contains(err.Error(), "ls-tree") && !strings.Contains(err.Error(), "Not a valid") {
		t.Errorf("error does not carry git's diagnosis: %v", err)
	}
}

func TestExtractCancelledContext(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	hash := b.Commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().Extract(ctx, hash, dest, ExtractOptions{}); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestExtractEmptyCommit(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")
	b.Git("commit", "--allow-empty", "-m", "empty")
	hash := b.Git("rev-parse", "HEAD")

	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{}); err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// An empty commit still has the tree of its parent.
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected the parent's tree: %v", err)
	}
}

// TestExtractIgnoresExportIgnore holds the line on a property that matters more
// than it looks: a repository cannot withhold files from its own snapshot.
//
// git archive honours .gitattributes export-ignore and offers no way to override
// it, which would let the subject of an investigation choose what gets
// collected. Reading the tree with ls-tree and cat-file bypasses attributes
// entirely. See docs/adr/0005.
func TestExtractIgnoresExportIgnore(t *testing.T) {
	b := newRepo(t)
	b.Write("normal.txt", "public\n", 0o644)
	b.Write("secret.txt", "withheld\n", 0o644)
	b.Write(".gitattributes", "secret.txt export-ignore\n", 0o644)
	hash := b.Commit("with export-ignore")

	repo := b.open()
	dest := filepath.Join(t.TempDir(), "snap")
	if _, err := repo.Extract(context.Background(), hash, dest, ExtractOptions{}); err != nil {
		t.Fatalf("Extract: %v", err)
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
	result, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{})
	if err != nil {
		t.Fatalf("Extract: %v", err)
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

// TestExtractReturnsOnWriteFailure guards a deadlock: writeTree streams
// blob content from cat-file, so returning early leaves the child mid-write with
// nobody draining stdout and the request goroutine mid-write to stdin. Both
// block and the deferred Wait never returns. Needs more than 64KB of content so
// the pipe buffer actually fills.
func TestExtractReturnsOnWriteFailure(t *testing.T) {
	b := newRepo(t)
	for _, name := range []string{"a.bin", "b.bin", "c.bin", "d.bin"} {
		b.Write(name, strings.Repeat("x", 80*1024), 0o644)
	}
	hash := b.Commit("big files")

	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	dest := filepath.Join(t.TempDir(), "snap")
	// A read-only destination, so writing fails. A path that merely collides is
	// recorded rather than failing, so it cannot be used to force an error.
	if err := os.MkdirAll(dest, 0o555); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(dest, 0o755) })

	done := make(chan error, 1)
	go func() {
		_, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("expected a write error")
		}
	case <-time.After(15 * time.Second):
		t.Fatal("Extract deadlocked instead of returning an error")
	}
}

func TestListRefsBranchNames(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")
	b.Git("branch", "feat/x")
	b.Git("branch", "zzz")

	branches, err := listBranchNames(t, context.Background(), b.open())
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}

	got := strings.Join(branches, ",")
	// for-each-ref sorts by refname.
	if want := "feat/x,main,zzz"; got != want && got != "feat/x,master,zzz" {
		t.Errorf("branches = %q, want %q", got, want)
	}
}

func TestListRefsBranchNamesEmptyRepository(t *testing.T) {
	b := newRepo(t) // initialised, no commits, so no branch ref exists yet

	branches, err := listBranchNames(t, context.Background(), b.open())
	if err != nil {
		t.Fatalf("ListRefs: %v", err)
	}
	if len(branches) != 0 {
		t.Errorf("branches = %v, want none", branches)
	}
}

func TestListRefsBranchNamesCancelledContext(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := listBranchNames(t, ctx, b.open()); err == nil {
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
