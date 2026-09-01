package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// repoBuilder builds a temporary repository. Tests describe the tree they need
// and let the builder deal with git.
type repoBuilder struct {
	t   *testing.T
	dir string
}

func newRepo(t *testing.T) *repoBuilder {
	t.Helper()
	b := &repoBuilder{t: t, dir: t.TempDir()}
	b.git("init")
	b.git("config", "user.name", "Test User")
	b.git("config", "user.email", "test@example.com")
	// Independent of the machine's global git config.
	b.git("config", "commit.gpgsign", "false")
	return b
}

func (b *repoBuilder) git(args ...string) string {
	b.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = b.dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		b.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// write creates a file, making any parent directories it needs.
func (b *repoBuilder) write(path, content string, mode os.FileMode) {
	b.t.Helper()
	full := filepath.Join(b.dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		b.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		b.t.Fatal(err)
	}
}

func (b *repoBuilder) symlink(target, path string) {
	b.t.Helper()
	if err := os.Symlink(target, filepath.Join(b.dir, path)); err != nil {
		b.t.Fatal(err)
	}
}

func (b *repoBuilder) commit(message string) string {
	b.t.Helper()
	b.git("add", "-A")
	b.git("commit", "-m", message)
	return b.git("rev-parse", "HEAD")
}

func (b *repoBuilder) open() *Repository {
	b.t.Helper()
	repo, err := Open(b.dir)
	if err != nil {
		b.t.Fatalf("Open: %v", err)
	}
	return repo
}

func TestExtractCommit(t *testing.T) {
	b := newRepo(t)
	b.write("readme.md", "hello\n", 0o644)
	b.write("src/deep/nested.go", "package deep\n", 0o644)
	b.write("scripts/run.sh", "#!/bin/sh\necho hi\n", 0o755)
	b.symlink("readme.md", "link.md")
	hash := b.commit("everything")

	dest := filepath.Join(t.TempDir(), "snap")
	if err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
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
	b.write("f.txt", "x\n", 0o644)
	hash := b.commit("one")

	// Two levels that do not exist yet.
	dest := filepath.Join(t.TempDir(), "a", "b", "snap")
	if err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected f.txt under a freshly created path: %v", err)
	}
}

func TestExtractCommitUnknownHash(t *testing.T) {
	b := newRepo(t)
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	dest := filepath.Join(t.TempDir(), "snap")
	err := b.open().ExtractCommit(context.Background(), "0000000000000000000000000000000000000000", dest)
	if err == nil {
		t.Fatal("expected an error for an unknown hash")
	}
	// git's own diagnosis is the useful part, so it must reach the caller.
	if !strings.Contains(err.Error(), "archive") && !strings.Contains(err.Error(), "not a valid") {
		t.Errorf("error does not carry git's diagnosis: %v", err)
	}
}

func TestExtractCommitCancelledContext(t *testing.T) {
	b := newRepo(t)
	b.write("f.txt", "x\n", 0o644)
	hash := b.commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	dest := filepath.Join(t.TempDir(), "snap")
	if err := b.open().ExtractCommit(ctx, hash, dest); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}

func TestExtractCommitEmptyCommit(t *testing.T) {
	b := newRepo(t)
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")
	b.git("commit", "--allow-empty", "-m", "empty")
	hash := b.git("rev-parse", "HEAD")

	dest := filepath.Join(t.TempDir(), "snap")
	if err := b.open().ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}
	// An empty commit still has the tree of its parent.
	if _, err := os.Stat(filepath.Join(dest, "f.txt")); err != nil {
		t.Errorf("expected the parent's tree: %v", err)
	}
}

// TestExtractCommitHonoursExportIgnore pins a known hazard: a repository can
// withhold files from its own snapshot via .gitattributes export-ignore, so the
// extracted tree disagrees with the commit's recorded file list. git archive
// offers no way to override it — pathspecs and --worktree-attributes both still
// honour it, and there is no --no-export-ignore.
//
// This test asserts the CURRENT behaviour so the gap is visible. It is inverted
// when extraction stops going through git archive.
func TestExtractCommitHonoursExportIgnore(t *testing.T) {
	b := newRepo(t)
	b.write("normal.txt", "public\n", 0o644)
	b.write("secret.txt", "withheld\n", 0o644)
	b.write(".gitattributes", "secret.txt export-ignore\n", 0o644)
	hash := b.commit("with export-ignore")

	repo := b.open()
	dest := filepath.Join(t.TempDir(), "snap")
	if err := repo.ExtractCommit(context.Background(), hash, dest); err != nil {
		t.Fatalf("ExtractCommit: %v", err)
	}

	// The commit records the file...
	commits, err := repo.ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatal(err)
	}
	var recorded bool
	for _, f := range commits[0].Files {
		if f.Path == "secret.txt" {
			recorded = true
		}
	}
	if !recorded {
		t.Error("secret.txt is missing from the commit's recorded file list")
	}

	// ...but it is absent from the extracted tree.
	if _, err := os.Stat(filepath.Join(dest, "secret.txt")); err == nil {
		t.Error("secret.txt was extracted: the export-ignore hazard is fixed, " +
			"invert this test")
	}
	if _, err := os.Stat(filepath.Join(dest, "normal.txt")); err != nil {
		t.Errorf("normal.txt should still be extracted: %v", err)
	}
}

func TestListBranches(t *testing.T) {
	b := newRepo(t)
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")
	b.git("branch", "feat/x")
	b.git("branch", "zzz")

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
	b.write("f.txt", "x\n", 0o644)
	b.commit("one")

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := b.open().ListBranches(ctx); err == nil {
		t.Error("expected an error from a cancelled context")
	}
}
