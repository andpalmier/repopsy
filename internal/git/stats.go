// Package git provides functionality for interacting with git repositories.
package git

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// CommitStats holds statistics about changes in a commit
type CommitStats struct {
	FilesChanged int
	Insertions   int
	Deletions    int
}

// GetCommitStats returns statistics about the changes in a commit
func (r *Repository) GetCommitStats(ctx context.Context, hash string) (CommitStats, error) {
	cmd := exec.CommandContext(ctx, "git", "show", "--numstat", "--format=", hash)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		return CommitStats{}, fmt.Errorf("failed to get commit stats: %w", err)
	}

	stats := CommitStats{}
	scanner := bufio.NewScanner(bytes.NewReader(output))

	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		parts := strings.Fields(line)
		if len(parts) < 3 {
			continue
		}

		// Binary files are shown as - - filename
		if parts[0] == "-" || parts[1] == "-" {
			stats.FilesChanged++
			continue
		}

		added, _ := strconv.Atoi(parts[0])
		deleted, _ := strconv.Atoi(parts[1])

		stats.FilesChanged++
		stats.Insertions += added
		stats.Deletions += deleted
	}

	return stats, nil
}

// GetCommitFullMessage retrieves the full commit message
func (r *Repository) GetCommitFullMessage(ctx context.Context, hash string) (string, error) {
	return r.runGitCommand(ctx, "log", "-1", "--format=%B", hash)
}

// GetCommitParents returns the parent commit hashes.
func (r *Repository) GetCommitParents(ctx context.Context, hash string) ([]string, error) {
	parentStr, err := r.runGitCommand(ctx, "log", "-1", "--format=%P", hash)
	if err != nil {
		return nil, err
	}
	if parentStr == "" {
		return nil, nil // Root commit
	}
	return strings.Fields(parentStr), nil
}

// GetFileDiff returns the diff for a specific commit
func (r *Repository) GetFileDiff(ctx context.Context, hash string) (string, error) {
	output, err := r.runGitCommand(ctx, "diff-tree", "-p", "--root", hash)
	if err != nil {
		return "", err
	}
	return output, nil
}
