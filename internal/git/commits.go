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

	// Tips walks history from these commits instead of from a branch. Passed on
	// stdin, so a long reflog cannot overflow the argument list.
	Tips []string
}

const (
	// recordSep starts each commit record. A commit message could in principle
	// contain this control character; nothing in git forbids it, but it is
	// vanishingly rare in practice.
	recordSep = "\x1e"

	// fieldSep separates the fixed fields within a record. Fields are split on
	// NUL rather than newline, so a multi-line field such as the notes or the
	// message body is safe anywhere in the list.
	fieldSep = "\x00"

	// fieldCount is how many fields logFormat emits. The message body is last so
	// that the diff lines following it are unambiguously separate.
	fieldCount = 20

	// logFormat requests every field a forensic record needs, in one pass.
	//
	// Dates use %aI/%cI rather than %at/%ct: the epoch forms discard the offset
	// git recorded, and rendering them would silently substitute the offset of
	// whatever machine runs repopsy. The recorded offset indicates the author's
	// locale and working hours, so it is evidence and must survive.
	logFormat = "%x1e" +
		"%H" + "%x00" + // commit hash
		"%h" + "%x00" + // abbreviated hash
		"%T" + "%x00" + // tree hash
		"%an" + "%x00" + // author name
		"%ae" + "%x00" + // author email
		"%aI" + "%x00" + // author date, ISO 8601 with the recorded offset
		"%cn" + "%x00" + // committer name
		"%ce" + "%x00" + // committer email
		"%cI" + "%x00" + // committer date, ISO 8601 with the recorded offset
		"%G?" + "%x00" + // signature status
		"%GS" + "%x00" + // signer name
		"%GK" + "%x00" + // signing key
		"%GF" + "%x00" + // signing key fingerprint
		"%GT" + "%x00" + // signing key trust level
		"%P" + "%x00" + // parent hashes
		"%e" + "%x00" + // message encoding
		"%D" + "%x00" + // refs pointing at this commit
		"%N" + "%x00" + // commit notes
		"%s" + "%x00" + // subject
		"%B" + "%x00" // full message
)

// ListCommits returns the commits selected by opts, fully populated.
//
// Every field is gathered in this single git invocation, including the message
// body, the per-file changes and their modes, so extraction does not shell out
// per commit. The whole log is held in memory: message bodies average a few
// hundred bytes, so this is roughly 2 MB at 10k commits and 23 MB at 100k —
// negligible beside the working tree each snapshot writes to disk. Batch in
// chunks if that ever stops being true.
func (r *Repository) ListCommits(ctx context.Context, opts ListOptions) ([]Commit, error) {
	args := []string{
		"log",
		"--format=" + logFormat,
		// --raw carries file modes and status letters; --numstat carries the line
		// counts. git emits the raw block first, then the numstat block, listing
		// the same files in the same order.
		"--raw",
		"--numstat",
		// Without this git omits diffs for merge commits entirely, silently
		// zeroing their change statistics. Reports a merge against its first
		// parent, matching what git show does. Requires git 2.31 or newer.
		"--diff-merges=first-parent",
	}

	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}
	if opts.Reverse {
		args = append(args, "--reverse")
	}
	switch {
	case len(opts.Tips) > 0:
		args = append(args, "--stdin")
	case opts.Branch != "":
		args = append(args, opts.Branch)
	}

	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = r.Path
	if len(opts.Tips) > 0 {
		cmd.Stdin = strings.NewReader(strings.Join(opts.Tips, "\n") + "\n")
	}

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
// emitted by logFormat, followed by that commit's diff lines.
func parseCommitRecord(record string) (Commit, error) {
	// One extra split so the diff block lands in its own part.
	parts := strings.SplitN(record, fieldSep, fieldCount+1)
	if len(parts) < fieldCount {
		return Commit{}, fmt.Errorf("invalid commit record: %q", record)
	}

	authorDate, err := parseGitDate(parts[5])
	if err != nil {
		return Commit{}, fmt.Errorf("invalid author date: %w", err)
	}
	commitDate, err := parseGitDate(parts[8])
	if err != nil {
		return Commit{}, fmt.Errorf("invalid commit date: %w", err)
	}

	var parents []string
	if parts[14] != "" {
		parents = strings.Fields(parts[14])
	}

	commit := Commit{
		Hash:           parts[0],
		ShortHash:      parts[1],
		TreeHash:       parts[2],
		Author:         parts[3],
		AuthorEmail:    parts[4],
		AuthorDate:     authorDate,
		Committer:      parts[6],
		CommitterEmail: parts[7],
		CommitDate:     commitDate,
		Signature: Signature{
			Status:      parts[9],
			Signer:      parts[10],
			Key:         parts[11],
			Fingerprint: parts[12],
			Trust:       parts[13],
		},
		ParentHashes: parents,
		Encoding:     parts[15],
		Refs:         parts[16],
		Notes:        strings.TrimSpace(parts[17]),
		Subject:      parts[18],
		FullMessage:  strings.TrimSpace(parts[19]),
	}

	if len(parts) > fieldCount {
		commit.Files = parseDiffBlock(parts[fieldCount])
		for _, f := range commit.Files {
			commit.FilesChanged++
			commit.Insertions += f.Insertions
			commit.Deletions += f.Deletions
		}
	}

	return commit, nil
}

// parseGitDate reads the ISO 8601 form git emits for %aI and %cI, preserving
// the offset it recorded.
func parseGitDate(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// parseDiffBlock parses the --raw lines and the --numstat lines git emits after
// a commit's fields.
//
// The raw lines carry modes, blobs and a status letter; the numstat lines carry
// line counts. Both list the same files in the same order, so the counts are
// matched positionally — safer than matching on path, since --numstat rewrites
// renamed paths into an "old => new" form that raw does not use.
func parseDiffBlock(block string) []FileChange {
	var files []FileChange
	var counts []FileChange

	for line := range strings.SplitSeq(block, "\n") {
		switch {
		case strings.HasPrefix(line, ":"):
			if f, ok := parseRawLine(line); ok {
				files = append(files, f)
			}
		case line != "":
			if f, ok := parseNumstatLine(line); ok {
				counts = append(counts, f)
			}
		}
	}

	// No raw lines (older git, or an unexpected shape): the counts alone still
	// describe which files changed.
	if len(files) == 0 {
		return counts
	}

	for i := range files {
		if i >= len(counts) {
			break
		}
		files[i].Insertions = counts[i].Insertions
		files[i].Deletions = counts[i].Deletions
		files[i].Binary = counts[i].Binary
	}
	return files
}

// parseRawLine parses one --raw entry:
//
//	:100644 100755 a23b1f0 9817e7c M	path
//	:100644 100644 d790d64 bce10ae R100	old	new
func parseRawLine(line string) (FileChange, bool) {
	fields := strings.Split(strings.TrimPrefix(line, ":"), "\t")
	if len(fields) < 2 {
		return FileChange{}, false
	}

	meta := strings.Fields(fields[0])
	if len(meta) < 5 {
		return FileChange{}, false
	}

	f := FileChange{
		OldMode: meta[0],
		NewMode: meta[1],
		OldBlob: meta[2],
		NewBlob: meta[3],
		Status:  meta[4],
		Path:    fields[1],
	}
	// Renames and copies name both sides.
	if len(fields) >= 3 {
		f.OldPath, f.Path = fields[1], fields[2]
	}
	return f, true
}

// parseNumstatLine parses one --numstat entry. Binary files report "-" in place
// of both counts.
func parseNumstatLine(line string) (FileChange, bool) {
	// Split on tabs rather than whitespace so filenames containing spaces do not
	// shift the count columns.
	fields := strings.SplitN(line, "\t", 3)
	if len(fields) < 3 {
		return FileChange{}, false
	}

	f := FileChange{Path: fields[2]}
	if fields[0] == "-" || fields[1] == "-" {
		f.Binary = true
		return f, true
	}

	f.Insertions, _ = strconv.Atoi(fields[0])
	f.Deletions, _ = strconv.Atoi(fields[1])
	return f, true
}

// RewrittenCommits returns commits a branch's ref once pointed at but no longer
// reaches — what a reset or force-push replaced.
//
// Reflogs are local and are not transferred by clone, so this is empty for a
// bare repository or a fresh clone however much history was rewritten upstream.
func (r *Repository) RewrittenCommits(ctx context.Context, branch string, reachable []Commit, limit int) ([]Commit, error) {
	tips, err := r.ReflogTips(ctx, branch)
	if err != nil || len(tips) == 0 {
		return nil, err
	}

	fromReflog, err := r.ListCommits(ctx, ListOptions{Tips: tips, Limit: limit, Reverse: true})
	if err != nil {
		return nil, err
	}

	current := make(map[string]bool, len(reachable))
	for _, c := range reachable {
		current[c.Hash] = true
	}

	var rewritten []Commit
	for _, c := range fromReflog {
		if current[c.Hash] {
			continue
		}
		c.Unreachable = true
		rewritten = append(rewritten, c)
	}
	return rewritten, nil
}
