// Package git provides functionality for interacting with git repositories.
package git

import (
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// ListOptions configures how commits are listed from the repository
type ListOptions struct {
	Branch  string
	Limit   int
	Reverse bool
}

const (
	// recordSep starts each commit record. A commit message could in principle
	// contain this control character; nothing in git forbids it, but it is
	// vanishingly rare in practice.
	recordSep = "\x1e"

	// fieldSep separates the fixed fields within a record.
	fieldSep = "\x00"

	// fieldCount is how many fields logFormat emits. The message body (%B) is
	// last so the numstat rows that follow it are unambiguously separate.
	fieldCount = 12

	// logFormat requests every field a snapshot needs in one pass. Combined
	// with --numstat it replaces what used to be three git invocations per
	// commit (log, then log -1 --format=%B, then show --numstat) with one for
	// the whole branch.
	logFormat = "%x1e" +
		"%H" + "%x00" + // full hash
		"%h" + "%x00" + // abbreviated hash
		"%an" + "%x00" + // author name
		"%ae" + "%x00" + // author email
		"%at" + "%x00" + // author timestamp
		"%cn" + "%x00" + // committer name
		"%ce" + "%x00" + // committer email
		"%ct" + "%x00" + // committer timestamp
		"%G?" + "%x00" + // signature status
		"%P" + "%x00" + // parent hashes
		"%s" + "%x00" + // subject
		"%B" + "%x00" // full message
)

// ListCommits returns the commits selected by opts, fully populated.
//
// Every field is gathered in this single git invocation, including the message
// body and change statistics, so extraction does not shell out per commit.
// The whole log is held in memory: message bodies average a few hundred bytes,
// so this is roughly 2 MB at 10k commits and 23 MB at 100k — negligible beside
// the working tree each snapshot writes to disk. Batch in chunks if that ever
// stops being true.
func (r *Repository) ListCommits(ctx context.Context, opts ListOptions) ([]Commit, error) {
	// --diff-merges=first-parent makes git log report a merge's diff against
	// its first parent. Without it git log omits merge diffs entirely, which
	// would silently zero the change statistics of every merge commit. This
	// matches what "git show --numstat" reported before the batching. Requires
	// git 2.31 or newer.
	args := []string{"log", "--format=" + logFormat, "--numstat", "--diff-merges=first-parent"}

	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}
	if opts.Reverse {
		args = append(args, "--reverse")
	}
	if opts.Branch != "" {
		args = append(args, opts.Branch)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log failed: %s", string(exitErr.Stderr))
		}
		return nil, fmt.Errorf("git log failed: %w", err)
	}

	records := strings.Split(string(output), recordSep)
	commits := make([]Commit, 0, len(records))
	for _, record := range records {
		if strings.TrimSpace(record) == "" {
			continue
		}
		commit, err := parseCommitRecord(record)
		if err != nil {
			continue
		}
		commits = append(commits, commit)
	}

	return commits, nil
}

// parseCommitRecord parses one record of git log output: the fixed fields
// emitted by logFormat, followed by the commit's --numstat rows.
func parseCommitRecord(record string) (Commit, error) {
	// One extra split so the numstat block lands in its own part.
	parts := strings.SplitN(record, fieldSep, fieldCount+1)
	if len(parts) < fieldCount {
		return Commit{}, fmt.Errorf("invalid commit record: %q", record)
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

	commit := Commit{
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
		FullMessage:    strings.TrimSpace(parts[11]),
	}

	if len(parts) > fieldCount {
		commit.FilesChanged, commit.Insertions, commit.Deletions = parseNumstat(parts[fieldCount])
	}

	return commit, nil
}

// parseNumstat sums the rows of git's --numstat output for a single commit.
//
// Binary files appear as "-" in place of both counts and contribute to the file
// count only. Merge commits are reported against their first parent, so they
// carry real counts rather than zeros.
func parseNumstat(block string) (filesChanged, insertions, deletions int) {
	for line := range strings.SplitSeq(block, "\n") {
		// Split on tabs rather than whitespace so filenames containing spaces
		// do not shift the count columns.
		fields := strings.SplitN(line, "\t", 3)
		if len(fields) < 3 {
			continue
		}

		filesChanged++
		if fields[0] == "-" || fields[1] == "-" {
			continue
		}

		added, _ := strconv.Atoi(fields[0])
		deleted, _ := strconv.Atoi(fields[1])
		insertions += added
		deletions += deleted
	}
	return filesChanged, insertions, deletions
}
