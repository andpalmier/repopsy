// Package git provides functionality for interacting with git repositories.
package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ListOptions configures how commits are listed from the repository.
type ListOptions struct {
	Branch  string
	Limit   int
	Reverse bool
}

// ListCommits returns a list of commits based on the provided options.
func (r *Repository) ListCommits(opts ListOptions) ([]Commit, error) {
	args := []string{
		"log",
		"--format=%H|%h|%an|%ae|%at|%cn|%ce|%ct|%G?|%P|%s",
	}

	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}

	if opts.Reverse {
		args = append(args, "--reverse")
	}

	if opts.Branch != "" {
		args = append(args, opts.Branch)
	}

	cmd := exec.Command("git", args...)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	var commits []Commit
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			continue
		}

		commit, err := parseCommitLine(line)
		if err != nil {
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
func parseCommitLine(line string) (Commit, error) {
	parts := strings.SplitN(line, "|", 11)
	if len(parts) < 11 {
		return Commit{}, fmt.Errorf("invalid commit line format: %s", line)
	}

	authorTimestamp, err := strconv.ParseInt(parts[4], 10, 64)
	if err != nil {
		return Commit{}, fmt.Errorf("invalid author timestamp: %w", err)
	}
	commitTimestamp, err := strconv.ParseInt(parts[7], 10, 64)
	if err != nil {
		return Commit{}, fmt.Errorf("invalid commit timestamp: %w", err)
	}

	var parents []string
	if parts[9] != "" {
		parents = strings.Fields(parts[9])
	}

	return Commit{
		Hash:           parts[0],
		ShortHash:      parts[1],
		Author:         parts[2],
		AuthorEmail:    parts[3],
		AuthorDate:     time.Unix(authorTimestamp, 0),
		Committer:      parts[5],
		CommitterEmail: parts[6],
		CommitDate:     time.Unix(commitTimestamp, 0),
		GPGSignature:   parts[8],
		ParentHashes:   parents,
		Subject:        parts[10],
	}, nil
}

// CommitCount returns the total number of commits in the repository.
func (r *Repository) CommitCount(branch string) (int, error) {
	ref := branch
	if ref == "" {
		ref = "HEAD"
	}

	output, err := r.runGitCommand("rev-list", "--count", ref)
	if err != nil {
		return 0, fmt.Errorf("failed to count commits: %w", err)
	}

	count, err := strconv.Atoi(output)
	if err != nil {
		return 0, fmt.Errorf("failed to parse commit count: %w", err)
	}
	return count, nil
}
