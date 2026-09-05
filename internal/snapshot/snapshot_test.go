package snapshot

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// sampleCommit is a fully-populated commit with a distinct author and committer,
// a parent, a signature, and a multi-paragraph message — so the golden exercises
// every branch of the template at once.
func sampleCommit() git.Commit {
	author, _ := time.Parse(time.RFC3339, "2023-12-05T14:30:22+01:00")
	committer, _ := time.Parse(time.RFC3339, "2023-12-05T15:00:00+01:00")
	return git.Commit{
		Hash:           "8f6a2b1c4d5e6f70819243546576879a0b1c2d3e",
		ShortHash:      "8f6a2b1",
		TreeHash:       "21b28cfd6375d10b1cda85f50a998fdf1557404f",
		Author:         "Alice Dev",
		AuthorEmail:    "alice@example.com",
		AuthorDate:     author,
		Committer:      "Bob Ops",
		CommitterEmail: "bob@example.com",
		CommitDate:     committer,
		Subject:        "Fix the extraction logic",
		ParentHashes:   []string{"7e5d1c2b"},
		FullMessage:    "Fix the extraction logic\n\nLonger body here.",
		Refs:           "HEAD -> main, tag: v1.2.3",
		Signature: git.Signature{
			Status:      "G",
			Signer:      "Alice Dev <alice@example.com>",
			Key:         "ABCD1234EF567890",
			Fingerprint: "FFFF0000AAAA1111BBBB2222CCCC3333DDDD4444",
			Trust:       "ultimate",
		},
		Files: []git.FileChange{
			{Path: "go.mod", Status: "M", OldMode: "100644", NewMode: "100644",
				OldBlob: "a23b1f0", NewBlob: "9817e7c", Insertions: 1, Deletions: 1},
			{Path: "scripts/deploy.sh", Status: "M", OldMode: "100644", NewMode: "100755",
				OldBlob: "aaa1111", NewBlob: "bbb2222", Insertions: 4, Deletions: 2},
			{Path: "demo.gif", Status: "A", OldMode: "000000", NewMode: "100644",
				OldBlob: "0000000", NewBlob: "ccc3333", Binary: true},
		},
		FilesChanged: 3,
		Insertions:   5,
		Deletions:    3,
	}
}

// assertGolden compares got against testdata/<name>, rewriting it under -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden (run: go test ./internal/snapshot -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("rendered metadata does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

func TestWriteMetadataGolden(t *testing.T) {
	var buf strings.Builder
	if err := WriteMetadata(&buf, sampleCommit()); err != nil {
		t.Fatalf("WriteMetadata: %v", err)
	}
	assertGolden(t, "metadata.golden", buf.String())
}

func TestWriteMetadataVariants(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*git.Commit)
		want    []string
		notWant []string
	}{
		{
			name:   "same author and committer omits the note",
			mutate: func(c *git.Commit) { c.Committer = c.Author },
			// The note is the template's only conditional block.
			notWant: []string{"NOTE: Author and Committer are different."},
		},
		{
			name:    "root commit says so instead of listing parents",
			mutate:  func(c *git.Commit) { c.ParentHashes = nil },
			want:    []string{"Parents:        (root commit - no parents)"},
			notWant: []string{"7e5d1c2b"},
		},
		{
			name:   "merge commit lists every parent",
			mutate: func(c *git.Commit) { c.ParentHashes = []string{"aaa111", "bbb222", "ccc333"} },
			want:   []string{"aaa111", "bbb222", "ccc333"},
		},
		{
			name: "merge commit reports zero change statistics",
			// Merges produce no diff against their first parent, so git reports
			// nothing and the counts are zero by construction. See CONTEXT.md.
			mutate: func(c *git.Commit) {
				c.FilesChanged, c.Insertions, c.Deletions = 0, 0, 0
			},
			want: []string{"Files Changed:  0", "Insertions:     +0", "Deletions:      -0"},
		},
		{
			name:   "unsigned commit renders prose, not the raw code",
			mutate: func(c *git.Commit) { c.Signature.Status = "" },
			want:   []string{"GPG Signature:  Not signed"},
		},
		{
			name:   "bad signature is surfaced",
			mutate: func(c *git.Commit) { c.Signature.Status = "B" },
			want:   []string{"GPG Signature:  Bad signature"},
		},
		{
			name:   "unrecognised signature code is passed through",
			mutate: func(c *git.Commit) { c.Signature.Status = "Z" },
			want:   []string{"GPG Signature:  Unknown (Z)"},
		},
		{
			name:   "multi-line message body is preserved verbatim",
			mutate: func(c *git.Commit) { c.FullMessage = "one\n\ntwo\n\nthree\n" },
			want:   []string{"one\n\ntwo\n\nthree\n"},
		},
		{
			name:   "empty message renders without error",
			mutate: func(c *git.Commit) { c.Subject, c.FullMessage = "", "" },
			want:   []string{"COMMIT MESSAGE"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := sampleCommit()
			tc.mutate(&c)

			var buf strings.Builder
			if err := WriteMetadata(&buf, c); err != nil {
				t.Fatalf("WriteMetadata: %v", err)
			}
			got := buf.String()

			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Errorf("output missing %q\n%s", want, got)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(got, notWant) {
					t.Errorf("output unexpectedly contains %q\n%s", notWant, got)
				}
			}
		})
	}
}

func TestPath(t *testing.T) {
	c := sampleCommit() // AuthorDate 1701786622 UTC, ShortHash 8f6a2b1
	const snap = "20231205_143022_8f6a2b1"

	tests := []struct {
		name   string
		outDir string
		branch string
		want   string
	}{
		{
			name:   "no branch puts snapshots directly under the output directory",
			outDir: "/out",
			branch: "",
			want:   filepath.Join("/out", snap),
		},
		{
			name:   "simple branch becomes one directory",
			outDir: "/out",
			branch: "main",
			want:   filepath.Join("/out", RefsDir, "main", snap),
		},
		{
			name:   "slash in a ref name becomes nested directories",
			outDir: "/out",
			branch: "feat/x",
			want:   filepath.Join("/out", RefsDir, "feat", "x", snap),
		},
		{
			name:   "deeply nested ref name nests all the way down",
			outDir: "/out",
			branch: "team/feat/sub/thing",
			want:   filepath.Join("/out", RefsDir, "team", "feat", "sub", "thing", snap),
		},
		{
			name:   "relative output directory is preserved",
			outDir: "out",
			branch: "main",
			want:   filepath.Join("out", RefsDir, "main", snap),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := Path(tc.outDir, tc.branch, c); got != tc.want {
				t.Errorf("Path(%q, %q) = %q, want %q", tc.outDir, tc.branch, got, tc.want)
			}
		})
	}
}

// TestPathDistinguishesSlashAndUnderscoreBranches is the bug that motivated
// nesting: replacing "/" with "_" mapped feat/x and feat_x onto one directory,
// silently interleaving two branches' snapshots.
func TestPathDistinguishesSlashAndUnderscoreBranches(t *testing.T) {
	c := sampleCommit()

	slashed := Path("/out", "feat/x", c)
	underscored := Path("/out", "feat_x", c)

	if slashed == underscored {
		t.Fatalf("feat/x and feat_x both map to %q", slashed)
	}
	if want := filepath.Join("/out", RefsDir, "feat", "x", "20231205_143022_8f6a2b1"); slashed != want {
		t.Errorf("feat/x -> %q, want %q", slashed, want)
	}
	if want := filepath.Join("/out", RefsDir, "feat_x", "20231205_143022_8f6a2b1"); underscored != want {
		t.Errorf("feat_x -> %q, want %q", underscored, want)
	}
}

// TestPathUsesNativeSeparators guards against embedding a literal "/" in a
// single path component, which would produce mixed separators on Windows.
func TestPathUsesNativeSeparators(t *testing.T) {
	got := Path("out", "feat/x", sampleCommit())
	for _, part := range strings.Split(got, string(filepath.Separator)) {
		if strings.ContainsAny(part, `/\`) {
			t.Errorf("path component %q contains a separator; full path %q", part, got)
		}
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

// TestFileLineBlobForDeletion covers the only pointer to a deleted file's
// content. git reports a deletion with an all-zero new blob, so showing the new
// blob would lose the one hash that can still retrieve what was removed.
func TestFileLineBlobForDeletion(t *testing.T) {
	deleted := git.FileChange{
		Path: "old/gone.go", Status: "D",
		OldMode: "100644", NewMode: "000000",
		OldBlob: "ddd4444eee5555ffff", NewBlob: "0000000000000000000",
		Deletions: 99,
	}

	line := fileLine(deleted)
	if !strings.Contains(line, "ddd4444eee55") {
		t.Errorf("a deletion must carry the removed content's blob: %q", line)
	}
	if strings.Contains(line, "000000000000") {
		t.Errorf("the all-zero new blob leaked into the record: %q", line)
	}

	// A normal modification still shows the new content.
	modified := git.FileChange{
		Path: "go.mod", Status: "M",
		OldBlob: "aaaaaaaaaaaa", NewBlob: "bbbbbbbbbbbb",
	}
	if line := fileLine(modified); !strings.Contains(line, "bbbbbbbbbbbb") {
		t.Errorf("a modification must show the new content's blob: %q", line)
	}

	// An addition has no old blob, so the new one is all there is.
	added := git.FileChange{Path: "new.go", Status: "A", OldBlob: "0000000", NewBlob: "cccccccccccc"}
	if line := fileLine(added); !strings.Contains(line, "cccccccccccc") {
		t.Errorf("an addition must show the new content's blob: %q", line)
	}
}

func TestWriteChecksums(t *testing.T) {
	var buf strings.Builder
	err := WriteChecksums(&buf, []git.FileDigest{
		{Path: "a.go", SHA256: strings.Repeat("a", 64)},
		{Path: "dir/b with space.txt", SHA256: strings.Repeat("b", 64)},
	})
	if err != nil {
		t.Fatalf("WriteChecksums: %v", err)
	}

	// Two spaces between hash and path is what sha256sum -c expects.
	// Paths are relative to the snapshot directory, so they carry the tree/
	// prefix and "sha256sum -c" works from where the record lives.
	want := strings.Repeat("a", 64) + "  " + TreeDir + "/a.go\n" +
		strings.Repeat("b", 64) + "  " + TreeDir + "/dir/b with space.txt\n"
	if buf.String() != want {
		t.Errorf("got  %q\nwant %q", buf.String(), want)
	}
}

func TestWriteChecksumsEmpty(t *testing.T) {
	var buf strings.Builder
	if err := WriteChecksums(&buf, nil); err != nil {
		t.Fatalf("WriteChecksums: %v", err)
	}
	if buf.String() != "" {
		t.Errorf("expected no output for no files, got %q", buf.String())
	}
}

// TestPathKeepsRefNamesOutOfTheRootNamespace is the reason RefsDir exists: git
// permits a branch called EXTRACTION.txt, whose directory would otherwise
// displace the manifest of the same name.
func TestPathKeepsRefNamesOutOfTheRootNamespace(t *testing.T) {
	c := sampleCommit()

	for _, ref := range []string{"EXTRACTION.txt", "REFLOG.txt", "TAGS.txt", "IDENTITIES.txt", "REPOSITORY.txt"} {
		got := Path("/out", ref, c)
		if want := filepath.Join("/out", RefsDir, ref, "20231205_143022_8f6a2b1"); got != want {
			t.Errorf("Path for ref %q = %q, want %q", ref, got, want)
		}
		// Nothing a ref is called may land directly in the output root.
		if filepath.Dir(filepath.Dir(got)) == "/out" && !strings.Contains(got, RefsDir) {
			t.Errorf("ref %q escaped into the root namespace: %q", ref, got)
		}
	}
}

// TestTreePathSeparatesContentFromRecords is the reason TreeDir exists: a commit
// may contain a file called COMMIT_INFO.txt, which would otherwise be
// overwritten by the snapshot's own record.
func TestTreePathSeparatesContentFromRecords(t *testing.T) {
	snap := Path("/out", "main", sampleCommit())
	tree := TreePath(snap)

	if tree != filepath.Join(snap, TreeDir) {
		t.Errorf("TreePath = %q, want %q", tree, filepath.Join(snap, TreeDir))
	}
	// The records sit beside the tree, never inside it.
	for _, record := range []string{MetadataFilename, ChecksumFilename} {
		if strings.HasPrefix(filepath.Join(snap, record), tree) {
			t.Errorf("%s would land inside the extracted tree", record)
		}
	}
}
