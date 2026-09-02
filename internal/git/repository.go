// Package git reads a git repository through the git command line: its refs,
// its commits, its trees, and the unversioned state around them.
//
// Nothing here knows what repopsy's output looks like. Rendering and naming
// belong to the snapshot package.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repository represents an opened git repository.
type Repository struct {
	// Path is the absolute path to the repository root
	Path string

	// GitDir is the absolute path to the repository's git directory, which is
	// Path/.git for a work tree and Path itself for a bare repository. Its
	// contents are not versioned, so no commit walk reaches them.
	GitDir string
}

// Open opens and validates the git repository containing path.
//
// The repository is identified by its top level, so naming any directory inside
// a work tree names the repository itself rather than the subtree. Bare
// repositories have no work tree and are their own root. See docs/adr/0002.
func Open(path string) (*Repository, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Stat before asking git, so a missing or non-directory path reports what is
	// actually wrong instead of "not a git repository".
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	root, err := resolveRoot(absPath)
	if err != nil {
		return nil, err
	}

	// Best effort: everything except the repository-state record works without it.
	gitDir, _ := gitOutput(context.Background(), root, "rev-parse", "--absolute-git-dir")

	return &Repository{Path: root, GitDir: gitDir}, nil
}

// resolveRoot finds the root repopsy should treat dir as naming. git resolves
// symlinks along the way, so the result is canonical.
func resolveRoot(dir string) (string, error) {
	top, topErr := gitOutput(context.Background(), dir, "rev-parse", "--show-toplevel")
	if topErr == nil && top != "" {
		return top, nil
	}

	// --show-toplevel fails inside a bare repository ("must be run in a work
	// tree"), so fall back to asking whether that is why.
	if bare, err := gitOutput(context.Background(), dir, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		return dir, nil
	}

	// Report git's own diagnosis rather than assuming the path is not a
	// repository. git distinguishes cases that matter: a directory it refuses
	// to read is not the same as a directory holding no repository, and saying
	// the wrong one sends the reader to check the wrong thing. Its refusal on
	// ownership grounds inside a container is the common example.
	if topErr != nil {
		return "", fmt.Errorf("cannot read a git repository at %s: %w", dir, topErr)
	}
	return "", fmt.Errorf("not a git repository: %s", dir)
}

// gitOutput runs git in dir and returns its trimmed standard output. git's own
// stderr is surfaced in the error, which is the most useful thing it produces.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s failed: %s", args[0], strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

// ListBranches returns all local branch names in the repository.
func (r *Repository) ListBranches(ctx context.Context) ([]string, error) {
	output, err := gitOutput(ctx, r.Path, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}
	if output == "" {
		return nil, nil
	}
	// Ref names cannot contain whitespace, and the output is already trimmed,
	// so every remaining line is a branch name.
	return strings.Split(output, "\n"), nil
}
