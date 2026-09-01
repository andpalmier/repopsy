package git

import (
	"strings"
	"testing"
)

// fields builds the fixed part of a git log record: the NUL-separated fields
// logFormat emits, in order, followed by the NUL that terminates the body.
func fields(hash, short, an, ae, at, cn, ce, ct, gpg, parents, subject, body string) string {
	return strings.Join([]string{hash, short, an, ae, at, cn, ce, ct, gpg, parents, subject, body}, "\x00") + "\x00"
}

// record appends a --numstat block to a record, the way git emits it.
func record(fixed string, numstatRows ...string) string {
	if len(numstatRows) == 0 {
		return fixed
	}
	return fixed + "\n\n" + strings.Join(numstatRows, "\n") + "\n"
}

// minimal is a valid record with only the fields a test does not care about.
func minimal(parents, subject, body string) string {
	return fields("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", parents, subject, body)
}

func TestParseCommitRecord(t *testing.T) {
	tests := []struct {
		name    string
		record  string
		wantErr bool
		check   func(*testing.T, Commit)
	}{
		{
			name: "fully populated commit",
			record: record(fields(
				"8f6a2b1c4d5e6f70819243546576879a0b1c2d3e", "8f6a2b1",
				"Alice Dev", "alice@example.com", "1701786622",
				"Bob Ops", "bob@example.com", "1701788400",
				"G", "7e5d1c2b", "Fix the extraction logic",
				"Fix the extraction logic\n\nLonger body.\n",
			), "5\t3\tinternal/git/extract.go"),
			check: func(t *testing.T, c Commit) {
				if c.Hash != "8f6a2b1c4d5e6f70819243546576879a0b1c2d3e" {
					t.Errorf("Hash = %q", c.Hash)
				}
				if c.ShortHash != "8f6a2b1" {
					t.Errorf("ShortHash = %q", c.ShortHash)
				}
				if c.Author != "Alice Dev" || c.AuthorEmail != "alice@example.com" {
					t.Errorf("author = %q <%q>", c.Author, c.AuthorEmail)
				}
				if c.Committer != "Bob Ops" || c.CommitterEmail != "bob@example.com" {
					t.Errorf("committer = %q <%q>", c.Committer, c.CommitterEmail)
				}
				if c.AuthorDate.Unix() != 1701786622 || c.CommitDate.Unix() != 1701788400 {
					t.Errorf("dates = %d / %d", c.AuthorDate.Unix(), c.CommitDate.Unix())
				}
				if c.GPGSignature != "G" {
					t.Errorf("GPGSignature = %q", c.GPGSignature)
				}
				if c.Subject != "Fix the extraction logic" {
					t.Errorf("Subject = %q", c.Subject)
				}
				if c.FilesChanged != 1 || c.Insertions != 5 || c.Deletions != 3 {
					t.Errorf("stats = %d files +%d -%d", c.FilesChanged, c.Insertions, c.Deletions)
				}
			},
		},
		{
			name: "multi-line body is captured and trimmed",
			// Trimming preserves what the previous per-commit git call did,
			// which routed through runGitCommand and trimmed its output.
			record: record(minimal("p", "Subject line", "Subject line\n\nBody paragraph.\n\n\n")),
			check: func(t *testing.T, c Commit) {
				if c.FullMessage != "Subject line\n\nBody paragraph." {
					t.Errorf("FullMessage = %q", c.FullMessage)
				}
			},
		},
		{
			name:   "body containing a blank line is not truncated",
			record: record(minimal("p", "s", "a\n\nb\n\nc\n")),
			check: func(t *testing.T, c Commit) {
				if c.FullMessage != "a\n\nb\n\nc" {
					t.Errorf("FullMessage = %q", c.FullMessage)
				}
			},
		},
		{
			name:   "root commit has no parents",
			record: record(minimal("", "Initial commit", "Initial commit\n")),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 0 {
					t.Errorf("ParentHashes = %v, want empty", c.ParentHashes)
				}
			},
		},
		{
			name: "merge commit carries first-parent statistics",
			// --diff-merges=first-parent means a merge reports the diff against
			// its first parent. Without that flag git log emits no rows for
			// merges and every merge would silently report zero.
			record: record(minimal("aaa111 bbb222", "Merge branch 'x'", "Merge branch 'x'\n"),
				"1\t1\tDockerfile"),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 2 {
					t.Fatalf("ParentHashes = %v, want 2", c.ParentHashes)
				}
				if c.FilesChanged != 1 || c.Insertions != 1 || c.Deletions != 1 {
					t.Errorf("stats = %d files +%d -%d, want 1/+1/-1", c.FilesChanged, c.Insertions, c.Deletions)
				}
			},
		},
		{
			name:   "octopus merge has three parents",
			record: record(minimal("a1 b2 c3", "Merge three", "Merge three\n")),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 3 {
					t.Errorf("ParentHashes = %v, want 3", c.ParentHashes)
				}
			},
		},
		{
			name:   "statistics sum across several files",
			record: record(minimal("p", "s", "s\n"), "1\t2\ta.go", "10\t20\tb.go", "100\t0\tc.go"),
			check: func(t *testing.T, c Commit) {
				if c.FilesChanged != 3 || c.Insertions != 111 || c.Deletions != 22 {
					t.Errorf("stats = %d files +%d -%d", c.FilesChanged, c.Insertions, c.Deletions)
				}
			},
		},
		{
			name:   "binary file counts as changed but adds no line counts",
			record: record(minimal("p", "s", "s\n"), "-\t-\tdemo.gif", "3\t1\treadme.md"),
			check: func(t *testing.T, c Commit) {
				if c.FilesChanged != 2 || c.Insertions != 3 || c.Deletions != 1 {
					t.Errorf("stats = %d files +%d -%d", c.FilesChanged, c.Insertions, c.Deletions)
				}
			},
		},
		{
			name:   "filename containing spaces does not shift the count columns",
			record: record(minimal("p", "s", "s\n"), "7\t2\tsome dir/a file with spaces.txt"),
			check: func(t *testing.T, c Commit) {
				if c.FilesChanged != 1 || c.Insertions != 7 || c.Deletions != 2 {
					t.Errorf("stats = %d files +%d -%d", c.FilesChanged, c.Insertions, c.Deletions)
				}
			},
		},
		{
			name:   "rename row is counted once",
			record: record(minimal("p", "s", "s\n"), "0\t0\told/path.go => new/path.go"),
			check: func(t *testing.T, c Commit) {
				if c.FilesChanged != 1 {
					t.Errorf("FilesChanged = %d, want 1", c.FilesChanged)
				}
			},
		},
		{
			name:   "subject containing the field separator of other tools",
			record: record(minimal("p", "Commit with | pipe and\ttab", "body\n")),
			check: func(t *testing.T, c Commit) {
				if c.Subject != "Commit with | pipe and\ttab" {
					t.Errorf("Subject = %q", c.Subject)
				}
			},
		},
		{
			name:   "empty subject and body are preserved",
			record: record(minimal("p", "", "")),
			check: func(t *testing.T, c Commit) {
				if c.Subject != "" || c.FullMessage != "" {
					t.Errorf("Subject = %q, FullMessage = %q", c.Subject, c.FullMessage)
				}
			},
		},
		{
			name:   "unsigned commit reports an empty signature field",
			record: record(fields("h", "s", "a", "a@e", "1", "c", "c@e", "2", "", "p", "s", "b\n")),
			check: func(t *testing.T, c Commit) {
				if c.GPGSignature != "" {
					t.Errorf("GPGSignature = %q, want empty", c.GPGSignature)
				}
			},
		},
		{
			name:    "too few fields is an error",
			record:  strings.Join([]string{"h", "s", "a"}, "\x00"),
			wantErr: true,
		},
		{
			name:    "empty record is an error",
			record:  "",
			wantErr: true,
		},
		{
			name:    "non-numeric author timestamp is an error",
			record:  record(fields("h", "s", "a", "a@e", "not-a-number", "c", "c@e", "2", "N", "p", "s", "b\n")),
			wantErr: true,
		},
		{
			name:    "non-numeric commit timestamp is an error",
			record:  record(fields("h", "s", "a", "a@e", "1", "c", "c@e", "not-a-number", "N", "p", "s", "b\n")),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCommitRecord(tc.record)

			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got commit %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			tc.check(t, got)
		})
	}
}

func TestParseNumstat(t *testing.T) {
	tests := []struct {
		name                   string
		block                  string
		files, insert, deleted int
	}{
		{name: "empty block", block: ""},
		{name: "only newlines", block: "\n\n\n"},
		{name: "single row", block: "\n\n1\t2\ta.go\n", files: 1, insert: 1, deleted: 2},
		{name: "binary only", block: "\n\n-\t-\tx.bin\n", files: 1},
		{
			name:  "mixed rows",
			block: "\n\n1\t1\ta\n-\t-\tb.png\n5\t0\tc\n",
			files: 3, insert: 6, deleted: 1,
		},
		{name: "malformed rows are skipped", block: "\n\ngarbage\n1\t2\tok.go\n", files: 1, insert: 1, deleted: 2},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			files, insert, deleted := parseNumstat(tc.block)
			if files != tc.files || insert != tc.insert || deleted != tc.deleted {
				t.Errorf("parseNumstat = %d files +%d -%d, want %d files +%d -%d",
					files, insert, deleted, tc.files, tc.insert, tc.deleted)
			}
		})
	}
}
