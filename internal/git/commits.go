package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CommitList is the result of listing commits: the commits themselves, plus any
// anomaly in the listing that a reader has to be told about.
type CommitList struct {
	Commits []Commit

	// Unparsed counts records git emitted that could not be parsed.
	//
	// Non-zero means commits are missing from Commits. git permits the record
	// separator inside a commit message, and a message containing it splits its
	// record in two so that neither half parses — which is how a commit can be
	// hidden from a listing. Reporting the count cannot recover the commit, but
	// it turns a silent omission into a visible anomaly.
	Unparsed int
}

// ListOptions selects which commits ListCommits returns, and in what order.
type ListOptions struct {
	Branch  string
	Limit   int
	Reverse bool

	// Tips walks history from these commits instead of from a branch. Passed on
	// stdin, so a long reflog cannot overflow the argument list.
	Tips []string

	// Hashes lists exactly these commits, in this order, without walking
	// history. Passed on stdin, like Tips.
	Hashes []string
}

const (
	// recordSep starts records in the other git commands that cannot use NUL
	// because their output includes a commit message. Commit listing uses -z
	// instead; see ListCommits.
	recordSep = "\x1e"

	// fieldSep separates the fixed fields within a record. Fields are split on
	// NUL rather than newline, so a multi-line field such as the notes or the
	// message body is safe anywhere in the list.
	fieldSep = "\x00"

	// fieldCount is how many fields logFormat emits. The message body is last so
	// that the diff lines following it are unambiguously separate.
	fieldCount = 19

	// shortHashLength is how much of a hash names a snapshot directory.
	//
	// Derived here rather than taken from git's %h, which honours core.abbrev
	// from the examined repository's own config — so the same repository would
	// explode into differently named directories depending on a setting in the
	// thing being examined.
	shortHashLength = 7

	// logFormat requests every field a forensic record needs, in one pass.
	//
	// Dates use %aI/%cI rather than %at/%ct: the epoch forms discard the offset
	// git recorded, and rendering them would silently substitute the offset of
	// whatever machine runs repopsy. The recorded offset indicates the author's
	// locale and working hours, so it is evidence and must survive.
	logFormat = "%H" + "%x00" + // commit hash
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
		"%B" // full message; -z supplies the record terminator
)

// ListCommits returns the commits selected by opts, fully populated.
//
// Every field is gathered in this single git invocation, including the message
// body, the per-file changes and their modes, so extraction does not shell out
// per commit. The whole log is held in memory: message bodies average a few
// hundred bytes, so this is roughly 2 MB at 10k commits and 23 MB at 100k —
// negligible beside the working tree each snapshot writes to disk. Batch in
// chunks if that ever stops being true.
func (r *Repository) ListCommits(ctx context.Context, opts ListOptions) (CommitList, error) {
	args := []string{
		"log",
		// Commit messages cannot contain NUL, so -z makes the record boundary
		// unambiguous even when a message contains control characters.
		"-z",
		"--format=" + logFormat,
		// --raw carries file modes and status letters; --numstat carries the line
		// counts. git emits the raw block first, then the numstat block, listing
		// the same files in the same order.
		"--raw",
		"--numstat",
		// --raw abbreviates blob hashes to 7 characters by default, which is not
		// enough to name an object unambiguously in a large repository.
		"--no-abbrev",
		// Without this git omits diffs for merge commits entirely, silently
		// zeroing their change statistics. Reports a merge against its first
		// parent, matching what git show does. Requires git 2.31 or newer.
		"--diff-merges=first-parent",
		// Both would run a program the examined repository's attributes and
		// configuration name. Neither changes --raw or --numstat for text.
		"--no-textconv",
		"--no-ext-diff",
		// log.showSignature would interleave gpg's output with the records.
		"--no-show-signature",
	}

	if opts.Limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", opts.Limit))
	}
	if opts.Reverse {
		args = append(args, "--reverse")
	}
	stdin := opts.Tips
	switch {
	case len(opts.Hashes) > 0:
		args = append(args, "--no-walk=unsorted", "--stdin")
		stdin = opts.Hashes
	case len(opts.Tips) > 0:
		args = append(args, "--stdin")
	case opts.Branch != "":
		args = append(args, "--end-of-options", opts.Branch)
	}

	cmd := r.command(ctx, args...)
	if len(stdin) > 0 {
		cmd.Stdin = strings.NewReader(strings.Join(stdin, "\n") + "\n")
	}

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return CommitList{}, fmt.Errorf("git log failed: %s", string(exitErr.Stderr))
		}
		return CommitList{}, fmt.Errorf("git log failed: %w", err)
	}

	tokens := strings.Split(string(output), fieldSep)
	list := CommitList{Commits: make([]Commit, 0, len(tokens)/fieldCount)}
	for pos := 0; pos < len(tokens); {
		// The final NUL in -z output leaves one empty token.
		if pos == len(tokens)-1 && tokens[pos] == "" {
			break
		}
		commit, next, err := parseCommitTokens(tokens, pos)
		if err != nil {
			// Counted rather than dropped: see CommitList.Unparsed.
			list.Unparsed++
			break
		}
		commit.ShallowBoundary = r.IsShallowBoundary(commit.Hash)
		list.Commits = append(list.Commits, commit)
		pos = next
	}

	// %G? reports an SSH signature as absent when no allowed-signers file is
	// configured, and never says which kind of signature it saw, so the raw
	// signature is read as well.
	if err := r.readSignatures(ctx, list.Commits); err != nil {
		return CommitList{}, err
	}
	return list, nil
}

// parseCommitTokens reads one commit from the NUL-separated tokens emitted by
// ListCommits, including its raw/numstat diff block. The returned position is
// the first token of the next commit.
func parseCommitTokens(tokens []string, pos int) (Commit, int, error) {
	if len(tokens)-pos < fieldCount {
		return Commit{}, len(tokens), fmt.Errorf("invalid commit record: only %d fields", len(tokens)-pos)
	}
	commit, err := parseCommitFields(tokens[pos : pos+fieldCount])
	if err != nil {
		return Commit{}, pos + fieldCount, err
	}
	pos += fieldCount
	files, next, err := parseDiffTokens(tokens, pos)
	if err != nil {
		return Commit{}, next, err
	}
	commit.Files = files
	commit.addFileTotals()
	return commit, next, nil
}

func parseCommitFields(parts []string) (Commit, error) {
	if len(parts) < fieldCount {
		return Commit{}, fmt.Errorf("invalid commit record: only %d fields", len(parts))
	}
	authorDate, err := parseGitDate(parts[4])
	if err != nil {
		return Commit{}, fmt.Errorf("invalid author date: %w", err)
	}
	commitDate, err := parseGitDate(parts[7])
	if err != nil {
		return Commit{}, fmt.Errorf("invalid commit date: %w", err)
	}

	var parents []string
	if parts[13] != "" {
		parents = strings.Fields(parts[13])
	}

	return Commit{
		Hash:           parts[0],
		ShortHash:      shortHash(parts[0]),
		TreeHash:       parts[1],
		Author:         parts[2],
		AuthorEmail:    parts[3],
		AuthorDate:     authorDate,
		Committer:      parts[5],
		CommitterEmail: parts[6],
		CommitDate:     commitDate,
		Signature: Signature{
			Status:      parts[8],
			Signer:      parts[9],
			Key:         parts[10],
			Fingerprint: parts[11],
			Trust:       parts[12],
		},
		ParentHashes: parents,
		Encoding:     parts[14],
		Refs:         parts[15],
		Notes:        strings.TrimSpace(parts[16]),
		Subject:      parts[17],
		FullMessage:  strings.TrimSpace(parts[18]),
	}, nil
}

func (c *Commit) addFileTotals() {
	for _, f := range c.Files {
		c.FilesChanged++
		c.Insertions += f.Insertions
		c.Deletions += f.Deletions
	}
}

// shortHash abbreviates a commit hash for display and directory naming.
func shortHash(hash string) string {
	if len(hash) <= shortHashLength {
		return hash
	}
	return hash[:shortHashLength]
}

// parseGitDate reads the ISO 8601 form git emits for %aI and %cI, preserving
// the offset it recorded.
func parseGitDate(s string) (time.Time, error) {
	return time.Parse(time.RFC3339, s)
}

// parseDiffTokens parses the raw and numstat tokens that follow a commit's
// fields in ListCommits. Paths are their own NUL-terminated tokens, so names
// containing tabs, newlines or non-ASCII bytes survive intact.
func parseDiffTokens(tokens []string, pos int) ([]FileChange, int, error) {
	var files []FileChange
	for pos < len(tokens) {
		token := strings.TrimPrefix(tokens[pos], "\n")
		if !strings.HasPrefix(token, ":") {
			break
		}
		f, ok := parseRawMeta(token)
		if !ok {
			return nil, pos, fmt.Errorf("invalid raw diff entry %q", token)
		}
		pos++
		if pos >= len(tokens) {
			return nil, pos, fmt.Errorf("raw diff entry %q has no path", token)
		}
		if isRenameOrCopy(f.Status) {
			f.OldPath = tokens[pos]
			pos++
			if pos >= len(tokens) {
				return nil, pos, fmt.Errorf("raw rename %q has no new path", token)
			}
		}
		f.Path = tokens[pos]
		pos++
		files = append(files, f)
	}

	for i := range files {
		if pos >= len(tokens) || !isNumstatToken(tokens[pos]) {
			return nil, pos, fmt.Errorf("missing numstat entry for %s", files[i].Path)
		}
		token := tokens[pos]
		pos++
		files[i].Insertions, files[i].Deletions, files[i].Binary = parseNumstatToken(token)
		if isRenameOrCopy(files[i].Status) {
			if pos+2 > len(tokens) {
				return nil, pos, fmt.Errorf("numstat rename for %s has too few paths", files[i].Path)
			}
			pos += 2
		}
	}
	return files, pos, nil
}

func parseRawMeta(line string) (FileChange, bool) {
	meta := strings.Fields(strings.TrimPrefix(line, ":"))
	if len(meta) < 5 {
		return FileChange{}, false
	}
	return FileChange{
		OldMode: meta[0],
		NewMode: meta[1],
		OldBlob: meta[2],
		NewBlob: meta[3],
		Status:  meta[4],
	}, true
}

func isRenameOrCopy(status string) bool {
	return strings.HasPrefix(status, "R") || strings.HasPrefix(status, "C")
}

func isNumstatToken(token string) bool {
	fields := strings.SplitN(token, "\t", 3)
	if len(fields) < 3 {
		return false
	}
	number := func(s string) bool {
		if s == "-" {
			return true
		}
		if s == "" {
			return false
		}
		for _, c := range s {
			if c < '0' || c > '9' {
				return false
			}
		}
		return true
	}
	return number(fields[0]) && number(fields[1])
}

func parseNumstatToken(token string) (insertions, deletions int, binary bool) {
	fields := strings.SplitN(token, "\t", 3)
	if len(fields) < 3 || fields[0] == "-" || fields[1] == "-" {
		return 0, 0, true
	}
	insertions, _ = strconv.Atoi(fields[0])
	deletions, _ = strconv.Atoi(fields[1])
	return insertions, deletions, false
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
		Path:    unquoteGitPath(fields[1]),
	}
	// Renames and copies name both sides.
	if len(fields) >= 3 {
		f.OldPath, f.Path = unquoteGitPath(fields[1]), unquoteGitPath(fields[2])
	}
	return f, true
}

// unquoteGitPath decodes the C-style quoting git uses for names containing
// control bytes, backslashes or non-ASCII bytes when core.quotePath is on.
func unquoteGitPath(p string) string {
	if strings.HasPrefix(p, `"`) {
		if unquoted, err := strconv.Unquote(p); err == nil {
			return unquoted
		}
	}
	return p
}

// RewrittenCommits returns commits ref once pointed at that are not already
// accounted for — what a reset or force-push replaced.
//
// exclude holds hashes already scheduled for extraction, so the same commit is
// not recovered twice when several refs' reflogs held it. ref is any ref with a
// reflog, including HEAD, whose log holds work abandoned on a detached head.
//
// Reflogs are local and are not transferred by clone, so this is empty for a
// bare repository or a fresh clone however much history was rewritten upstream.
func (r *Repository) RewrittenCommits(ctx context.Context, ref string, exclude map[string]bool, limit int) ([]Commit, error) {
	tips, err := r.ReflogTips(ctx, ref)
	if err != nil || len(tips) == 0 {
		return nil, err
	}

	// Deliberately unlimited: the newest commits a reflog names are usually the
	// reachable ones, so limiting the walk first would filter away almost
	// everything and make recovery silently useless alongside -n. The limit is
	// applied to the recovered set instead.
	fromReflog, err := r.ListCommits(ctx, ListOptions{Tips: tips, Reverse: true})
	if err != nil {
		return nil, err
	}

	var rewritten []Commit
	for _, c := range fromReflog.Commits {
		if exclude[c.Hash] {
			continue
		}
		c.Unreachable = true
		rewritten = append(rewritten, c)
	}

	if limit > 0 && len(rewritten) > limit {
		// Keep the newest, matching how -n selects reachable commits.
		rewritten = rewritten[len(rewritten)-limit:]
	}
	return rewritten, nil
}
