// Package git provides functionality for interacting with git repositories
package git

import "time"

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
