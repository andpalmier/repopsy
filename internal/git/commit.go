// Package git provides functionality for interacting with git repositories
package git

import "time"

// Commit represents a single git commit with its metadata.
type Commit struct {
	Hash      string
	ShortHash string

	// TreeHash identifies the content state independently of commit metadata,
	// so identical trees can be spotted across rewritten or cherry-picked
	// history.
	TreeHash string

	Author      string
	AuthorEmail string

	// AuthorDate and CommitDate carry the offset git recorded, not the offset of
	// the machine running repopsy. The offset is evidence in its own right.
	AuthorDate     time.Time
	Committer      string
	CommitterEmail string
	CommitDate     time.Time

	Subject      string
	FullMessage  string
	Encoding     string
	Notes        string
	ParentHashes []string

	// Refs lists the branches and tags pointing at this commit, as git reports
	// them, or empty when none do.
	Refs string

	Signature Signature

	// Files lists every path the commit touched, in git's order.
	Files []FileChange

	// Aggregates over Files, kept as fields so the metadata template can read
	// them directly.
	FilesChanged int
	Insertions   int
	Deletions    int
}

// Signature holds what git can tell us about a commit's GPG signature. The
// status verdict alone does not identify the signer, which is what attribution
// actually needs.
type Signature struct {
	Status      string // %G? — see FormatStatus
	Signer      string // %GS — the signer's declared name
	Key         string // %GK — the signing key ID
	Fingerprint string // %GF — the signing key fingerprint
	Trust       string // %GT — the trust level git assigns the key
}

// Signed reports whether git found any signature at all.
func (s Signature) Signed() bool {
	return s.Status != "" && s.Status != "N"
}

// FileChange is one path touched by a commit.
type FileChange struct {
	Path string

	// OldPath is set only for renames and copies.
	OldPath string

	// Status is git's raw status letter: A added, M modified, D deleted,
	// R renamed, C copied, T type changed.
	Status string

	// OldMode and NewMode are git's six-digit file modes. A change between them
	// is security-relevant: 100644 to 100755 makes a file executable.
	OldMode string
	NewMode string

	OldBlob string
	NewBlob string

	Insertions int
	Deletions  int

	// Binary marks a file git could not diff by line.
	Binary bool
}

// ModeChanged reports whether the file's mode changed, ignoring the additions
// and deletions where one side has no mode at all.
func (f FileChange) ModeChanged() bool {
	const noMode = "000000"
	if f.OldMode == "" || f.NewMode == "" || f.OldMode == noMode || f.NewMode == noMode {
		return false
	}
	return f.OldMode != f.NewMode
}

// Backdated reports whether the commit was authored after it was committed,
// which cannot happen in normal use and indicates a rewrite or a forged date.
func (c Commit) Backdated() bool {
	return c.AuthorDate.After(c.CommitDate)
}
