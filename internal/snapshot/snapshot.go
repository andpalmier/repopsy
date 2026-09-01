// Package snapshot decides what a snapshot is called and what metadata it
// carries. A snapshot is one commit's complete working tree written to its own
// directory, alongside that commit's metadata.
//
// Naming and metadata used to be split across three packages — the branch
// directory in app, the commit directory in extractor, and the metadata file in
// git. This package owns all three, so git no longer knows what the output
// looks like.
package snapshot

import (
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"text/template"

	"github.com/andpalmier/repopsy/internal/git"
)

const (
	// MetadataFilename is the metadata file written inside every snapshot.
	MetadataFilename = "COMMIT_INFO.txt"

	// ChecksumFilename is the integrity record written inside every snapshot.
	ChecksumFilename = "SHA256SUMS"

	// timestampFormat names a snapshot directory: 20231205_143022_abc1234.
	timestampFormat = "20060102_150405"

	// dateFormat renders a commit date with the offset git recorded.
	dateFormat = "2006-01-02T15:04:05Z07:00"
)

// Path returns the directory a commit's snapshot is written to.
//
// A branch name maps to nested directories mirroring its ref path, so "feat/x"
// nests under "feat". Git refuses to create refs/heads/main/x while
// refs/heads/main exists, so this inherits git's own guarantee that no two
// branches can claim the same directory, and no escaping scheme is needed. An
// empty branch puts snapshots directly under outDir.
//
// The timestamp is rendered in the offset the commit itself records, never the
// offset of the machine running repopsy, so the same repository always explodes
// into the same directory names.
//
// Known limitation: ref names that are legal in git but illegal as Windows
// path components — reserved device names such as "aux" and "CON", and the
// characters < > " | — still produce unusable directories on Windows. This
// predates the nesting scheme; see docs/adr/0001.
func Path(outDir, branch string, c git.Commit) string {
	parts := make([]string, 0, 4)
	parts = append(parts, outDir)
	for _, segment := range strings.Split(branch, "/") {
		if segment != "" {
			parts = append(parts, segment)
		}
	}
	return filepath.Join(append(parts, dirName(c))...)
}

// dirName is the snapshot's own directory name, without any parent path.
func dirName(c git.Commit) string {
	return c.AuthorDate.Format(timestampFormat) + "_" + c.ShortHash
}

// WriteMetadata renders a commit's metadata into w. Callers own the
// destination, so the rendered text is reachable in tests without a filesystem.
func WriteMetadata(w io.Writer, c git.Commit) error {
	if err := metadataTemplate.Execute(w, c); err != nil {
		return fmt.Errorf("failed to render commit metadata: %w", err)
	}
	return nil
}

// WriteChecksums records the SHA-256 of every extracted file, in the format
// "sha256sum -c" reads, so a reader can verify the snapshot was not altered
// after extraction. Paths are relative to the snapshot directory.
func WriteChecksums(w io.Writer, digests []git.FileDigest) error {
	for _, d := range digests {
		if _, err := fmt.Fprintf(w, "%s  %s\n", d.SHA256, d.Path); err != nil {
			return fmt.Errorf("failed to write checksums: %w", err)
		}
	}
	return nil
}

var metadataTemplate = template.Must(template.New("metadata").Funcs(reportFuncs).Funcs(template.FuncMap{
	"formatGPGStatus": formatGPGStatus,
	"fileLine":        fileLine,
}).Parse(metadataTemplateStr))

const metadataTemplateStr = `COMMIT INFORMATION
===========================

Hash:           {{.Hash}}
Short Hash:     {{.ShortHash}}
Tree:           {{.TreeHash}}
{{if .Refs}}Refs:           {{.Refs}}
{{end}}{{if .Encoding}}Encoding:       {{.Encoding}}
{{end}}
AUTHOR (who wrote the code)
---------------------------
Name:           {{.Author}}
Email:          {{.AuthorEmail}}
Date:           {{.AuthorDate | formatDate}}
Timestamp:      {{.AuthorDate.Unix}}

COMMITTER (who applied the commit)
----------------------------------
Name:           {{.Committer}}
Email:          {{.CommitterEmail}}
Date:           {{.CommitDate | formatDate}}
Timestamp:      {{.CommitDate.Unix}}
{{if ne .Author .Committer}}
NOTE: Author and Committer are different.
{{end}}{{if .Unreachable}}
NOTE: no branch reaches this commit. It was recovered from the reflog, so
history was rewritten after it was made.
{{end}}{{if .Backdated}}
ANOMALY: the author date is later than the committer date. This does not
occur in normal use and indicates a rewritten or forged date.
{{end}}
VERIFICATION
------------
GPG Signature:  {{.Signature.Status | formatGPGStatus}}
{{if .Signature.Signed}}Signer:         {{with .Signature.Signer}}{{.}}{{else}}(not reported){{end}}
Key:            {{with .Signature.Key}}{{.}}{{else}}(not reported){{end}}
Fingerprint:    {{with .Signature.Fingerprint}}{{.}}{{else}}(not reported){{end}}
Trust:          {{with .Signature.Trust}}{{.}}{{else}}(not reported){{end}}
{{end}}
LINEAGE
-------
Parents:        {{if .ParentHashes}}{{range .ParentHashes}}{{.}} {{end}}{{else}}(root commit - no parents){{end}}

CHANGE STATISTICS
-----------------
Files Changed:  {{.FilesChanged}}
Insertions:     +{{.Insertions}}
Deletions:      -{{.Deletions}}

CHANGED FILES
-------------
Status, line counts, the new content's blob hash, and the path.

{{if .Files}}{{range .Files}}{{fileLine .}}
{{end}}{{else}}(no file changes recorded)
{{end}}{{if .Submodules}}
SUBMODULES
----------
git stores only a pointer to another repository, so this snapshot does not
contain the content below.

{{range .Submodules}}{{.Path}}  ->  {{.Commit}}
{{end}}{{end}}
COMMIT MESSAGE
--------------
Subject:
{{.Subject}}

Full Message:
{{.FullMessage}}
{{if .Notes}}
NOTES
-----
{{.Notes}}
{{end}}`

// fileLine renders one changed file as a fixed-width row: status, line counts,
// path, and any mode change. Mode transitions are called out because a file
// gaining the executable bit is security-relevant.
func fileLine(f git.FileChange) string {
	status := f.Status
	if status == "" {
		status = "?"
	}

	counts := fmt.Sprintf("+%-6d -%-6d", f.Insertions, f.Deletions)
	if f.Binary {
		counts = fmt.Sprintf("%-15s", "binary")
	}

	path := f.Path
	if f.OldPath != "" {
		path = f.OldPath + " -> " + f.Path
	}

	// The blob hash names this file's content in the object store, so a reader
	// can retrieve and verify it with "git cat-file -p" against the original
	// repository. Abbreviated to keep the row readable; git resolves a prefix.
	line := fmt.Sprintf("%-4s %s %-12s %s", status, counts, blobRef(contentBlob(f)), path)
	if f.ModeChanged() {
		line += fmt.Sprintf("  [mode %s -> %s]", f.OldMode, f.NewMode)
	}
	return line
}

// contentBlob picks the blob that identifies what a change is about: the new
// content normally, and the removed content for a deletion — where the old blob
// is the only pointer to what the file contained.
func contentBlob(f git.FileChange) string {
	if isAbsentBlob(f.NewBlob) {
		return f.OldBlob
	}
	return f.NewBlob
}

// isAbsentBlob reports whether a blob field names no object.
func isAbsentBlob(blob string) bool {
	return blob == "" || strings.Trim(blob, "0") == ""
}

// blobRef abbreviates a blob hash, or marks its absence.
func blobRef(blob string) string {
	const width = 12
	if isAbsentBlob(blob) {
		return strings.Repeat("-", width)
	}
	if len(blob) > width {
		return blob[:width]
	}
	return blob
}

// formatGPGStatus renders git's %G? signature codes as prose.
func formatGPGStatus(status string) string {
	switch status {
	case "G":
		return "Valid signature (good)"
	case "B":
		return "Bad signature"
	case "U":
		return "Valid signature, unknown key"
	case "X":
		return "Valid signature, expired"
	case "Y":
		return "Valid signature, expired key"
	case "R":
		return "Valid signature, revoked key"
	case "E":
		return "Cannot verify (missing key)"
	case "N", "":
		return "Not signed"
	default:
		return "Unknown (" + status + ")"
	}
}
