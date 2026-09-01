package git

import (
	"strings"
	"testing"
)

// record builds a git log line in the format ListCommits requests: NUL-separated
// fields in the order the format string declares them.
func record(hash, short, an, ae, at, cn, ce, ct, gpg, parents, subject string) string {
	return strings.Join([]string{hash, short, an, ae, at, cn, ce, ct, gpg, parents, subject}, "\x00")
}

func TestParseCommitLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantErr bool
		check   func(*testing.T, Commit)
	}{
		{
			name: "fully populated commit",
			line: record(
				"8f6a2b1c4d5e6f70819243546576879a0b1c2d3e", "8f6a2b1",
				"Alice Dev", "alice@example.com", "1701786622",
				"Bob Ops", "bob@example.com", "1701788400",
				"G", "7e5d1c2b", "Fix the extraction logic",
			),
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
				if c.AuthorDate.Unix() != 1701786622 {
					t.Errorf("AuthorDate = %d", c.AuthorDate.Unix())
				}
				if c.CommitDate.Unix() != 1701788400 {
					t.Errorf("CommitDate = %d", c.CommitDate.Unix())
				}
				if c.GPGSignature != "G" {
					t.Errorf("GPGSignature = %q", c.GPGSignature)
				}
				if c.Subject != "Fix the extraction logic" {
					t.Errorf("Subject = %q", c.Subject)
				}
			},
		},
		{
			name: "root commit has no parents",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", "", "Initial commit"),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 0 {
					t.Errorf("ParentHashes = %v, want empty", c.ParentHashes)
				}
			},
		},
		{
			name: "merge commit has two parents",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", "aaa111 bbb222", "Merge branch 'x'"),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 2 {
					t.Fatalf("ParentHashes = %v, want 2", c.ParentHashes)
				}
				if c.ParentHashes[0] != "aaa111" || c.ParentHashes[1] != "bbb222" {
					t.Errorf("ParentHashes = %v", c.ParentHashes)
				}
			},
		},
		{
			name: "octopus merge has three parents",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", "a1 b2 c3", "Merge three"),
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 3 {
					t.Errorf("ParentHashes = %v, want 3", c.ParentHashes)
				}
			},
		},
		{
			name: "subject containing the field separator of other tools",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", "p", "Commit with | pipe and\ttab"),
			check: func(t *testing.T, c Commit) {
				if c.Subject != "Commit with | pipe and\ttab" {
					t.Errorf("Subject = %q", c.Subject)
				}
			},
		},
		{
			name: "empty subject is preserved",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "N", "p", ""),
			check: func(t *testing.T, c Commit) {
				if c.Subject != "" {
					t.Errorf("Subject = %q, want empty", c.Subject)
				}
			},
		},
		{
			name: "unsigned commit reports an empty signature field",
			line: record("h", "s", "a", "a@e", "1", "c", "c@e", "2", "", "p", "s"),
			check: func(t *testing.T, c Commit) {
				if c.GPGSignature != "" {
					t.Errorf("GPGSignature = %q, want empty", c.GPGSignature)
				}
			},
		},
		{
			name:    "too few fields is an error",
			line:    strings.Join([]string{"h", "s", "a"}, "\x00"),
			wantErr: true,
		},
		{
			name:    "empty line is an error",
			line:    "",
			wantErr: true,
		},
		{
			name:    "non-numeric author timestamp is an error",
			line:    record("h", "s", "a", "a@e", "not-a-number", "c", "c@e", "2", "N", "p", "s"),
			wantErr: true,
		},
		{
			name:    "non-numeric commit timestamp is an error",
			line:    record("h", "s", "a", "a@e", "1", "c", "c@e", "not-a-number", "N", "p", "s"),
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := parseCommitLine(tc.line)

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

func TestFormatGPGStatus(t *testing.T) {
	tests := map[string]string{
		"G": "Valid signature (good)",
		"B": "Bad signature",
		"U": "Valid signature, unknown key",
		"X": "Valid signature, expired",
		"Y": "Valid signature, expired key",
		"R": "Valid signature, revoked key",
		"E": "Cannot verify (missing key)",
		"N": "Not signed",
		"":  "Not signed",
		"?": "Unknown (?)",
	}

	for status, want := range tests {
		if got := formatGPGStatus(status); got != want {
			t.Errorf("formatGPGStatus(%q) = %q, want %q", status, got, want)
		}
	}
}
