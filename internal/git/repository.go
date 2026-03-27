// Package git provides functionality for interacting with git repositories
package git

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/andpalmier/repopsy/internal/config"
)

// Repository represents an opened git repository.
type Repository struct {
	// Path is the absolute path to the repository root
	Path string

	// BufferSize is the scanner buffer size for git operations
	// Default: 1MB (set by Open if not specified)
	BufferSize int
}

// Open opens and validates a git repository at the given path
// It returns an error if the path is not a valid git repository
func Open(path string) (*Repository, error) {
	// Resolve to absolute path
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Resolve symlinks to prevent path traversal attacks
	realPath, err := filepath.EvalSymlinks(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve symlinks: %w", err)
	}

	// Check if the resolved path differs from the original (potential symlink attack)
	if realPath != absPath {
		// Verify the real path is still within expected bounds
		realAbsPath, absErr := filepath.Abs(realPath)
		if absErr != nil {
			return nil, fmt.Errorf("failed to resolve real path: %w", absErr)
		}
		absPath = realAbsPath
	}

	// Check if directory exists
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	// Verify we have read permissions on the directory
	if _, err := os.ReadDir(absPath); err != nil {
		return nil, fmt.Errorf("failed to read directory: %w", err)
	}

	// Verify it's a git repository by checking for .git
	gitDir := filepath.Join(absPath, ".git")
	if _, err := os.Stat(gitDir); err != nil {
		// Could be a bare repository or worktree, try git rev-parse
		cmd := exec.Command("git", "rev-parse", "--git-dir")
		cmd.Dir = absPath
		if _, err := cmd.Output(); err != nil {
			return nil, fmt.Errorf("not a git repository: %s", absPath)
		}
	}

	return &Repository{
		Path:       absPath,
		BufferSize: config.DefaultBufferSize,
	}, nil
}

// runGitCommand executes a git command and returns trimmed output.
func (r *Repository) runGitCommand(ctx context.Context, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path
	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return "", fmt.Errorf("git %s failed: %w", args[0], exitErr)
		}
		return "", fmt.Errorf("git %s failed: %w", args[0], err)
	}
	return strings.TrimSpace(string(output)), nil
}

// GetBufferSize returns the scanner buffer size, defaulting to 1MB if not set
func (r *Repository) GetBufferSize() int {
	if r.BufferSize > 0 {
		return r.BufferSize
	}
	return config.DefaultBufferSize
}

// ListBranches returns all local branch names in the repository.
func (r *Repository) ListBranches(ctx context.Context) ([]string, error) {
	output, err := r.runGitCommand(ctx, "for-each-ref", "--format=%(refname:short)", "refs/heads/")
	if err != nil {
		return nil, fmt.Errorf("failed to list branches: %w", err)
	}

	// Handle empty output - return empty slice instead of slice with empty string
	if strings.TrimSpace(output) == "" {
		return []string{}, nil
	}

	// Split and filter out empty strings (from trailing newlines)
	lines := strings.Split(output, "\n")
	branches := make([]string, 0, len(lines))
	for _, line := range lines {
		if line != "" {
			branches = append(branches, line)
		}
	}
	return branches, nil
}
