// Package git provides functionality for interacting with git repositories
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
	return &Repository{Path: root}, nil
}

// resolveRoot finds the root repopsy should treat dir as naming. git resolves
// symlinks along the way, so the result is canonical.
func resolveRoot(dir string) (string, error) {
	if top, err := gitOutput(context.Background(), dir, "rev-parse", "--show-toplevel"); err == nil && top != "" {
		return top, nil
	}

	// --show-toplevel fails inside a bare repository ("must be run in a work
	// tree"), so fall back to asking whether that is why.
	if bare, err := gitOutput(context.Background(), dir, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		return dir, nil
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
