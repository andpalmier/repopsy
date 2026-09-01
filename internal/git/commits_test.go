package git

import (
	"strings"
	"testing"
)

// rec builds a git log record field by field, so a test only names what it
// cares about. Field order matches logFormat.
type rec struct {
	hash, short, tree              string
	an, ae, adate                  string
	cn, ce, cdate                  string
	gstatus, gsigner, gkey         string
	gfp, gtrust                    string
	parents, encoding, refs, notes string
	subject, body                  string
	raw, numstat                   []string
}

func baseRec() rec {
	return rec{
		hash: "8f6a2b1c4d5e", short: "8f6a2b1", tree: "21b28cfd",
		an: "Alice Dev", ae: "alice@example.com", adate: "2023-12-05T14:30:22+01:00",
		cn: "Alice Dev", ce: "alice@example.com", cdate: "2023-12-05T14:30:22+01:00",
		gstatus: "N", subject: "Subject line", body: "Subject line\n",
	}
}

func (r rec) build() string {
	fields := []string{
		r.hash, r.short, r.tree,
		r.an, r.ae, r.adate,
		r.cn, r.ce, r.cdate,
		r.gstatus, r.gsigner, r.gkey, r.gfp, r.gtrust,
		r.parents, r.encoding, r.refs, r.notes,
		r.subject, r.body,
	}
	out := strings.Join(fields, "\x00") + "\x00"
	if len(r.raw) > 0 || len(r.numstat) > 0 {
		out += "\n\n" + strings.Join(append(append([]string{}, r.raw...), r.numstat...), "\n") + "\n"
	}
	return out
}

func TestParseCommitRecord(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*rec)
		wantErr bool
		check   func(*testing.T, Commit)
	}{
		{
			name:   "identity and tree",
			mutate: func(r *rec) {},
			check: func(t *testing.T, c Commit) {
				if c.Hash != "8f6a2b1c4d5e" || c.ShortHash != "8f6a2b1" {
					t.Errorf("hash = %q / %q", c.Hash, c.ShortHash)
				}
				if c.TreeHash != "21b28cfd" {
					t.Errorf("TreeHash = %q", c.TreeHash)
				}
			},
		},
		{
			// The whole point of %aI over %at: the offset git recorded must
			// survive, rather than being replaced by the analyst's zone.
			name:   "dates keep the recorded offset",
			mutate: func(r *rec) { r.adate = "2023-12-05T14:30:22+09:00" },
			check: func(t *testing.T, c Commit) {
				if got := c.AuthorDate.Format("2006-01-02T15:04:05Z07:00"); got != "2023-12-05T14:30:22+09:00" {
					t.Errorf("AuthorDate = %q, want the +09:00 offset preserved", got)
				}
				_, offset := c.AuthorDate.Zone()
				if offset != 9*3600 {
					t.Errorf("zone offset = %d seconds, want 32400", offset)
				}
				// The epoch is still derivable and is zone-independent.
				if c.AuthorDate.Unix() != 1701754222 {
					t.Errorf("Unix = %d", c.AuthorDate.Unix())
				}
			},
		},
		{
			name:   "negative offsets survive too",
			mutate: func(r *rec) { r.adate = "2023-12-05T14:30:22-07:00" },
			check: func(t *testing.T, c Commit) {
				if _, offset := c.AuthorDate.Zone(); offset != -7*3600 {
					t.Errorf("zone offset = %d, want -25200", offset)
				}
			},
		},
		{
			name: "signature identity is captured, not just the verdict",
			mutate: func(r *rec) {
				r.gstatus, r.gsigner = "G", "Alice Dev <alice@example.com>"
				r.gkey, r.gfp, r.gtrust = "ABCD1234", "FFFF0000AAAA", "ultimate"
			},
			check: func(t *testing.T, c Commit) {
				if !c.Signature.Signed() {
					t.Error("Signed() = false for status G")
				}
				if c.Signature.Signer != "Alice Dev <alice@example.com>" {
					t.Errorf("Signer = %q", c.Signature.Signer)
				}
				if c.Signature.Key != "ABCD1234" || c.Signature.Fingerprint != "FFFF0000AAAA" {
					t.Errorf("key/fp = %q / %q", c.Signature.Key, c.Signature.Fingerprint)
				}
				if c.Signature.Trust != "ultimate" {
					t.Errorf("Trust = %q", c.Signature.Trust)
				}
			},
		},
		{
			name:   "unsigned commit is not Signed",
			mutate: func(r *rec) { r.gstatus = "N" },
			check: func(t *testing.T, c Commit) {
				if c.Signature.Signed() {
					t.Error("Signed() = true for status N")
				}
			},
		},
		{
			name:   "empty status is not Signed either",
			mutate: func(r *rec) { r.gstatus = "" },
			check: func(t *testing.T, c Commit) {
				if c.Signature.Signed() {
					t.Error("Signed() = true for an empty status")
				}
			},
		},
		{
			name:   "refs, encoding and notes are recorded",
			mutate: func(r *rec) { r.refs, r.encoding, r.notes = "HEAD -> main, tag: v1", "ISO-8859-1", "reviewed by bob\n" },
			check: func(t *testing.T, c Commit) {
				if c.Refs != "HEAD -> main, tag: v1" {
					t.Errorf("Refs = %q", c.Refs)
				}
				if c.Encoding != "ISO-8859-1" {
					t.Errorf("Encoding = %q", c.Encoding)
				}
				if c.Notes != "reviewed by bob" {
					t.Errorf("Notes = %q", c.Notes)
				}
			},
		},
		{
			name:   "root commit has no parents",
			mutate: func(r *rec) { r.parents = "" },
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 0 {
					t.Errorf("ParentHashes = %v", c.ParentHashes)
				}
			},
		},
		{
			name:   "merge commit has two parents",
			mutate: func(r *rec) { r.parents = "aaa111 bbb222" },
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 2 {
					t.Errorf("ParentHashes = %v, want 2", c.ParentHashes)
				}
			},
		},
		{
			name:   "octopus merge has three parents",
			mutate: func(r *rec) { r.parents = "a1 b2 c3" },
			check: func(t *testing.T, c Commit) {
				if len(c.ParentHashes) != 3 {
					t.Errorf("ParentHashes = %v, want 3", c.ParentHashes)
				}
			},
		},
		{
			name: "backdating is detectable",
			mutate: func(r *rec) {
				r.adate = "2023-12-05T14:30:22+00:00"
				r.cdate = "2023-12-05T10:00:00+00:00"
			},
			check: func(t *testing.T, c Commit) {
				if !c.Backdated() {
					t.Error("Backdated() = false when the author date is later")
				}
			},
		},
		{
			name:   "a normal commit is not backdated",
			mutate: func(r *rec) {},
			check: func(t *testing.T, c Commit) {
				if c.Backdated() {
					t.Error("Backdated() = true for equal dates")
				}
			},
		},
		{
			name:   "multi-line body is captured and trimmed",
			mutate: func(r *rec) { r.body = "Subject line\n\nBody paragraph.\n\n\n" },
			check: func(t *testing.T, c Commit) {
				if c.FullMessage != "Subject line\n\nBody paragraph." {
					t.Errorf("FullMessage = %q", c.FullMessage)
				}
			},
		},
		{
			name:   "subject with awkward characters survives",
			mutate: func(r *rec) { r.subject = "Commit with | pipe and\ttab" },
			check: func(t *testing.T, c Commit) {
				if c.Subject != "Commit with | pipe and\ttab" {
					t.Errorf("Subject = %q", c.Subject)
				}
			},
		},
		{
			name:    "too few fields is an error",
			mutate:  func(r *rec) {},
			wantErr: true,
		},
		{
			name:    "unparseable author date is an error",
			mutate:  func(r *rec) { r.adate = "not-a-date" },
			wantErr: true,
		},
		{
			name:    "unparseable committer date is an error",
			mutate:  func(r *rec) { r.cdate = "1701786622" }, // the old epoch form
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := baseRec()
			tc.mutate(&r)
			record := r.build()
			if tc.name == "too few fields is an error" {
				record = strings.Join([]string{"h", "s", "t"}, "\x00")
			}

			got, err := parseCommitRecord(record)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", got)
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

func TestParseCommitRecordFileChanges(t *testing.T) {
	r := baseRec()
	r.raw = []string{
		":100644 100644 a23b1f0 9817e7c M\tgo.mod",
		":000000 100644 0000000 d54bb3c A\tinternal/new.go",
		":100644 100755 aaa1111 bbb2222 M\tscripts/deploy.sh",
		":100644 000000 ccc3333 0000000 D\told/gone.go",
		":100644 100644 ddd4444 eee5555 M\tdemo.gif",
	}
	r.numstat = []string{
		"1\t1\tgo.mod",
		"263\t0\tinternal/new.go",
		"4\t2\tscripts/deploy.sh",
		"0\t99\told/gone.go",
		"-\t-\tdemo.gif",
	}

	c, err := parseCommitRecord(r.build())
	if err != nil {
		t.Fatalf("parseCommitRecord: %v", err)
	}

	if len(c.Files) != 5 {
		t.Fatalf("got %d files, want 5: %+v", len(c.Files), c.Files)
	}

	// Aggregates exclude the binary file's (absent) line counts.
	if c.FilesChanged != 5 || c.Insertions != 268 || c.Deletions != 102 {
		t.Errorf("aggregates = %d files +%d -%d, want 5/+268/-102",
			c.FilesChanged, c.Insertions, c.Deletions)
	}

	tests := []struct {
		i                   int
		path, status        string
		ins, del            int
		binary, modeChanged bool
	}{
		{0, "go.mod", "M", 1, 1, false, false},
		{1, "internal/new.go", "A", 263, 0, false, false},
		{2, "scripts/deploy.sh", "M", 4, 2, false, true}, // gained the executable bit
		{3, "old/gone.go", "D", 0, 99, false, false},
		{4, "demo.gif", "M", 0, 0, true, false},
	}
	for _, tc := range tests {
		f := c.Files[tc.i]
		if f.Path != tc.path || f.Status != tc.status {
			t.Errorf("file %d = %q %q, want %q %q", tc.i, f.Path, f.Status, tc.path, tc.status)
		}
		if f.Insertions != tc.ins || f.Deletions != tc.del {
			t.Errorf("file %d counts = +%d -%d, want +%d -%d", tc.i, f.Insertions, f.Deletions, tc.ins, tc.del)
		}
		if f.Binary != tc.binary {
			t.Errorf("file %d Binary = %v, want %v", tc.i, f.Binary, tc.binary)
		}
		if f.ModeChanged() != tc.modeChanged {
			t.Errorf("file %d ModeChanged = %v (%s -> %s), want %v",
				tc.i, f.ModeChanged(), f.OldMode, f.NewMode, tc.modeChanged)
		}
	}

	// Blobs are recorded so a file's content can be located in the object store.
	if c.Files[0].OldBlob != "a23b1f0" || c.Files[0].NewBlob != "9817e7c" {
		t.Errorf("blobs = %q / %q", c.Files[0].OldBlob, c.Files[0].NewBlob)
	}
}

func TestParseCommitRecordRename(t *testing.T) {
	r := baseRec()
	r.raw = []string{":100644 100644 d790d64 bce10ae R100\tdocs/old.md\tdocs/new.md"}
	r.numstat = []string{"0\t0\tdocs/old.md => docs/new.md"}

	c, err := parseCommitRecord(r.build())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 1 {
		t.Fatalf("got %d files, want 1", len(c.Files))
	}
	f := c.Files[0]
	if f.Status != "R100" {
		t.Errorf("Status = %q, want R100", f.Status)
	}
	if f.OldPath != "docs/old.md" || f.Path != "docs/new.md" {
		t.Errorf("rename = %q -> %q", f.OldPath, f.Path)
	}
}

func TestParseCommitRecordNoChanges(t *testing.T) {
	// A merge commit whose first-parent diff is empty, or an empty commit.
	c, err := parseCommitRecord(baseRec().build())
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Files) != 0 || c.FilesChanged != 0 {
		t.Errorf("expected no files, got %d (%+v)", c.FilesChanged, c.Files)
	}
}

// TestParseDiffBlockNumstatOnly covers the fallback for output without raw
// lines, so the file list survives even then.
func TestParseDiffBlockNumstatOnly(t *testing.T) {
	files := parseDiffBlock("\n\n3\t1\ta.go\n-\t-\tb.png\n")
	if len(files) != 2 {
		t.Fatalf("got %d files, want 2", len(files))
	}
	if files[0].Path != "a.go" || files[0].Insertions != 3 || files[0].Deletions != 1 {
		t.Errorf("files[0] = %+v", files[0])
	}
	if !files[1].Binary {
		t.Errorf("files[1] should be binary: %+v", files[1])
	}
}

func TestParseDiffBlockIgnoresGarbage(t *testing.T) {
	files := parseDiffBlock("\n\nnot a diff line\n:short\n1\t2\tok.go\n")
	if len(files) != 1 || files[0].Path != "ok.go" {
		t.Errorf("got %+v, want just ok.go", files)
	}
}

func TestParseNumstatLineSpacesInPath(t *testing.T) {
	f, ok := parseNumstatLine("7\t2\tsome dir/a file with spaces.txt")
	if !ok {
		t.Fatal("expected the line to parse")
	}
	if f.Path != "some dir/a file with spaces.txt" || f.Insertions != 7 || f.Deletions != 2 {
		t.Errorf("got %+v", f)
	}
}
