// Package git provides functionality for interacting with git repositories
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// Repository represents an opened git repository
type Repository struct {
	// Path is the absolute path to the repository root
	Path string

	// gitDir is the path to the .git directory
	gitDir string
}

// Open opens and validates a git repository at the given path
// It returns an error if the path is not a valid git repository
func Open(path string) (*Repository, error) {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	// Verify it's a git repository by checking for .git
	gitDir := filepath.Join(absPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		// Could be a bare repository or worktree, try git rev-parse
		cmd := exec.Command("git", "rev-parse", "--git-dir")
		cmd.Dir = absPath
		output, err := cmd.Output()
		if err != nil {
			return nil, fmt.Errorf("not a git repository: %s", absPath)
		}
		gitDir = strings.TrimSpace(string(output))
		if !filepath.IsAbs(gitDir) {
			gitDir = filepath.Join(absPath, gitDir)
		}
	}

	return &Repository{
		Path:   absPath,
		gitDir: gitDir,
	}, nil
}

// runGitCommand executes a git command and returns trimmed output
func (r *Repository) runGitCommand(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s failed: %s", args[0], string(exitErr.Stderr))
		}
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetCurrentBranch returns the name of the current branch
func (r *Repository) GetCurrentBranch(ctx context.Context) string {
	branch, err := r.runGitCommand(ctx, "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "HEAD"
	}
	return branch
}

// GetRemoteURL returns the origin remote URL
func (r *Repository) GetRemoteURL(ctx context.Context) string {
	url, err := r.runGitCommand(ctx, "remote", "get-url", "origin")
	if err != nil {
		return ""
	}
	return url
}

// ListBranches returns all local branch names in the repository
func (r *Repository) ListBranches(ctx context.Context) ([]string, error) {
	output, err := r.runGitCommand(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	if output == "" {
		return nil, nil
	}

	branches := strings.Split(output, "\n")
	return branches, nil
}
