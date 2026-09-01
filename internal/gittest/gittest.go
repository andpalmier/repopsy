// Package gittest builds throwaway git repositories for tests.
//
// It deliberately does not import internal/git: the git package's own tests
// live in package git, which would make that an import cycle. Callers get a
// directory and open it themselves.
package gittest

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// identity pins what git records. GIT_AUTHOR_* and GIT_COMMITTER_* override
// local config, and a developer machine may well have them set, so relying on
// "git config user.name" alone makes any test that asserts an identity pass
// locally and differ on CI.
var identity = []string{
	"GIT_AUTHOR_NAME=Test User",
	"GIT_AUTHOR_EMAIL=test@example.com",
	"GIT_COMMITTER_NAME=Test User",
	"GIT_COMMITTER_EMAIL=test@example.com",
}

// Repo is a temporary git repository, removed when the test ends.
type Repo struct {
	t *testing.T

	// Dir is the repository's working directory.
	Dir string
}

// New initialises an empty repository with signing and identity pinned.
func New(t *testing.T) *Repo {
	t.Helper()
	r := &Repo{t: t, Dir: t.TempDir()}
	r.Git("init")
	r.Git("config", "user.name", "Test User")
	r.Git("config", "user.email", "test@example.com")
	// Independent of the machine's global git config.
	r.Git("config", "commit.gpgsign", "false")
	return r
}

// Git runs a git command in the repository and returns its trimmed output.
// A failure fails the test.
func (r *Repo) Git(args ...string) string {
	r.t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Env = append(os.Environ(), identity...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		r.t.Fatalf("git %v failed: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// Write creates a file, making any parent directories it needs.
func (r *Repo) Write(path, content string, mode os.FileMode) {
	r.t.Helper()
	full := filepath.Join(r.Dir, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		r.t.Fatal(err)
	}
	if err := os.WriteFile(full, []byte(content), mode); err != nil {
		r.t.Fatal(err)
	}
}

// Symlink creates a symbolic link at path pointing to target.
func (r *Repo) Symlink(target, path string) {
	r.t.Helper()
	if err := os.Symlink(target, filepath.Join(r.Dir, path)); err != nil {
		r.t.Fatal(err)
	}
}

// Commit stages everything and commits it, returning the new commit's hash.
func (r *Repo) Commit(message string) string {
	r.t.Helper()
	r.Git("add", "-A")
	r.Git("commit", "-m", message)
	return r.Git("rev-parse", "HEAD")
}

// Branch returns the current branch's name, which depends on the machine's
// init.defaultBranch.
func (r *Repo) Branch() string {
	r.t.Helper()
	return r.Git("rev-parse", "--abbrev-ref", "HEAD")
}

// WithCommits adds n commits, each introducing one file.
func (r *Repo) WithCommits(n int) *Repo {
	r.t.Helper()
	for i := range n {
		name := "file" + string(rune('a'+i)) + ".txt"
		r.Write(name, "content of "+name+"\n", 0o644)
		r.Commit("commit " + string(rune('a'+i)))
	}
	return r
}
