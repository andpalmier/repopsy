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

	// timestampFormat names a snapshot directory: 20231205_143022_abc1234.
	timestampFormat = "20060102_150405"
)

// Path returns the directory a commit's snapshot is written to.
//
// A branch name maps to nested directories mirroring its ref path, so "feat/x"
// nests under "feat". Git refuses to create refs/heads/main/x while
// refs/heads/main exists, so this inherits git's own guarantee that no two
// branches can claim the same directory, and no escaping scheme is needed. An
// empty branch puts snapshots directly under outDir.
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

var metadataTemplate = template.Must(template.New("metadata").Funcs(template.FuncMap{
	"formatGPGStatus": formatGPGStatus,
}).Parse(metadataTemplateStr))

const metadataTemplateStr = `COMMIT INFORMATION
===========================

Hash:           {{.Hash}}
Short Hash:     {{.ShortHash}}

AUTHOR (who wrote the code)
---------------------------
Name:           {{.Author}}
Email:          {{.AuthorEmail}}
Date:           {{.AuthorDate.Format "2006-01-02T15:04:05Z07:00"}}
Timestamp:      {{.AuthorDate.Unix}}

COMMITTER (who applied the commit)
----------------------------------
Name:           {{.Committer}}
Email:          {{.CommitterEmail}}
Date:           {{.CommitDate.Format "2006-01-02T15:04:05Z07:00"}}
Timestamp:      {{.CommitDate.Unix}}
{{if ne .Author .Committer}}
NOTE: Author and Committer are different.
{{end}}
VERIFICATION
------------
GPG Signature:  {{.GPGSignature | formatGPGStatus}}

LINEAGE
-------
Parents:        {{if .ParentHashes}}{{range .ParentHashes}}{{.}} {{end}}{{else}}(root commit - no parents){{end}}

CHANGE STATISTICS
-----------------
Files Changed:  {{.FilesChanged}}
Insertions:     +{{.Insertions}}
Deletions:      -{{.Deletions}}

COMMIT MESSAGE
--------------
Subject:
{{.Subject}}

Full Message:
{{.FullMessage}}
`

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
