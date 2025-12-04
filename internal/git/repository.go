// Package git provides functionality for interacting with git repositories.
package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Repository represents an opened git repository.
// It provides methods for listing commits and extracting their contents.
type Repository struct {
	// Path is the absolute path to the repository root
	Path string

	// gitDir is the path to the .git directory
	gitDir string
}

// ListOptions configures how commits are listed from the repository.
type ListOptions struct {
	// Branch specifies which branch to list commits from.
	// If empty, uses the current HEAD.
	Branch string

	// Limit specifies the maximum number of commits to return.
	// If 0, returns all commits.
	Limit int

	// Reverse, if true, returns commits in chronological order (oldest first).
	// Default is reverse chronological (newest first).
	Reverse bool
}

// Open opens and validates a git repository at the given path.
// It returns an error if the path is not a valid git repository.
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

// ListCommits returns a list of commits based on the provided options.
// Commits are returned in reverse chronological order by default (newest first).
func (r *Repository) ListCommits(opts ListOptions) ([]Commit, error) {
	// Build git log command with custom format
	// Format: hash|short_hash|author|timestamp|subject
	args := []string{
		"log",
		"--format=%H|%h|%an|%at|%s",
	}

	// Add limit if specified
	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}

	// Add reverse flag if requested
	if opts.Reverse {
		args = append(args, "--reverse")
	}

	// Add branch if specified
	if opts.Branch != "" {
		args = append(args, opts.Branch)
	}

	// Execute git log
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	// Parse output into commits
	var commits []Commit
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		commit, err := parseCommitLine(line)
		if err != nil {
			// Log warning but continue with other commits
			continue
		}
		commits = append(commits, commit)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("failed to parse git log output: %w", err)
	}

	return commits, nil
}

// parseCommitLine parses a single line of git log output.
// Expected format: hash|short_hash|author|timestamp|subject
func parseCommitLine(line string) (Commit, error) {
	parts := strings.SplitN(line, "|", 5)
	if len(parts) < 5 {
		return Commit{}, fmt.Errorf("invalid commit line format: %s", line)
	}

	// Parse timestamp
	var timestamp int64
	for _, c := range parts[3] {
		if c >= '0' && c <= '9' {
			timestamp = timestamp*10 + int64(c-'0')
		}
	}

	return Commit{
		Hash:      parts[0],
		ShortHash: parts[1],
		Author:    parts[2],
		Date:      time.Unix(timestamp, 0),
		Subject:   parts[4],
	}, nil
}

// ExtractCommit extracts the contents of a commit to the specified destination path.
// It uses git archive to efficiently extract the commit's tree without .git metadata.
func (r *Repository) ExtractCommit(hash, destPath string) error {
	// Create destination directory
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Use git archive piped to tar for extraction
	// This is more efficient than checkout and doesn't require worktree manipulation
	archiveCmd := exec.Command("git", "archive", "--format=tar", hash)
	archiveCmd.Dir = r.Path

	tarCmd := exec.Command("tar", "-xf", "-", "-C", destPath)

	// Connect archive output to tar input
	pipe, err := archiveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	tarCmd.Stdin = pipe

	// Capture stderr for both commands
	var archiveStderr, tarStderr bytes.Buffer
	archiveCmd.Stderr = &archiveStderr
	tarCmd.Stderr = &tarStderr

	// Start both commands
	if err := archiveCmd.Start(); err != nil {
		return fmt.Errorf("failed to start git archive: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		archiveCmd.Process.Kill()
		return fmt.Errorf("failed to start tar: %w", err)
	}

	// Wait for both commands to complete
	archiveErr := archiveCmd.Wait()
	tarErr := tarCmd.Wait()

	if archiveErr != nil {
		return fmt.Errorf("git archive failed: %s", archiveStderr.String())
	}
	if tarErr != nil {
		return fmt.Errorf("tar extraction failed: %s", tarStderr.String())
	}

	return nil
}

// GetCurrentBranch returns the name of the current branch.
// Returns "HEAD" if in detached HEAD state.
func (r *Repository) GetCurrentBranch() string {
	cmd := exec.Command("git", "rev-parse", "--abbrev-ref", "HEAD")
	cmd.Dir = r.Path
	output, err := cmd.Output()
	if err != nil {
		return "HEAD"
	}
	return strings.TrimSpace(string(output))
}

// CommitCount returns the total number of commits in the repository.
func (r *Repository) CommitCount(branch string) (int, error) {
	args := []string{"rev-list", "--count"}
	if branch != "" {
		args = append(args, branch)
	} else {
		args = append(args, "HEAD")
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = r.Path
	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("failed to count commits: %w", err)
	}

	// Parse count
	var count int
	for _, c := range strings.TrimSpace(string(output)) {
		if c >= '0' && c <= '9' {
			count = count*10 + int(c-'0')
		}
	}
	return count, nil
}
