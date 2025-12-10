// Package git provides functionality for interacting with git repositories
package git

import (
	"fmt"
	"os"
	"path/filepath"
	"text/template"
	"time"
)

// metadataTemplateStr is the template for COMMIT_INFO.txt files
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

var metadataTemplate = template.Must(template.New("metadata").Funcs(template.FuncMap{
	"formatGPGStatus": formatGPGStatus,
}).Parse(metadataTemplateStr))

// Commit represents a single git commit with its metadata
type Commit struct {
	Hash           string
	ShortHash      string
	Author         string
	AuthorEmail    string
	AuthorDate     time.Time
	Committer      string
	CommitterEmail string
	CommitDate     time.Time
	Subject        string
	ParentHashes   []string
	FullMessage    string
	GPGSignature   string
	FilesChanged   int
	Insertions     int
	Deletions      int
}

// String returns a human-readable representation of the commit
func (c Commit) String() string {
	return c.ShortHash + " " + c.Subject
}

// WriteMetadataFile writes a COMMIT_INFO.txt file with commit metadata
func (c Commit) WriteMetadataFile(destPath string) (err error) {
	metadataPath := filepath.Join(destPath, "COMMIT_INFO.txt")
	f, err := os.Create(metadataPath)
	if err != nil {
		return fmt.Errorf("failed to create metadata file: %w", err)
	}
	// Use a deferred function to ensure the file is closed,
	// and to capture any error from closing if no other error occurred.
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close metadata file: %w", closeErr)
		}
	}()

	// Execute the template against the metadata
	if execErr := metadataTemplate.Execute(f, c); execErr != nil {
		return fmt.Errorf("failed to execute metadata template: %w", execErr)
	}

	return nil
}

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
