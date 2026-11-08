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

	// Unreachable marks a commit no branch reaches any more, recovered from the
	// reflog. Its presence is itself evidence that history was rewritten.
	Unreachable bool

	// ShallowBoundary marks a commit whose parents a shallow clone cut off. git
	// reports no parents for it, so it would otherwise read as a root commit.
	ShallowBoundary bool

	// Anomalies are integrity and provenance findings about this commit, worked
	// out across the whole extracted history rather than from the commit alone.
	Anomalies []Anomaly

	// Tree describes what extraction did with the commit's tree. Filled in at
	// extraction time.
	Tree TreeReport

	// Submodules present in this commit's tree. Filled in at extraction time,
	// since the commit log describes changes rather than the whole tree.
	Submodules []Submodule
}

// Signature holds what git can tell us about a commit's signature. The status
// verdict alone does not identify the signer, which is what attribution
// actually needs.
type Signature struct {
	Status      string // %G? — see FormatStatus
	Signer      string // %GS — the signer's declared name
	Key         string // %GK — the signing key ID
	Fingerprint string // %GF — the signing key fingerprint
	Trust       string // %GT — the trust level git assigns the key

	// The fields below are read from the signature in the commit object
	// itself, so they are known whether or not the signature can be verified
	// here. They are claims, not verified facts.

	// Type is the kind of signature present: SigGPG, SigSSH, SigX509 or
	// SigUnknown, or empty when the commit is unsigned.
	Type string

	// IssuerKeyID and IssuerFingerprint identify the key the signature claims
	// to be made with. For SSH there is no key ID, and the fingerprint is the
	// SHA256 form ssh-keygen prints.
	IssuerKeyID       string
	IssuerFingerprint string

	// Algorithm is the public key algorithm, e.g. ssh-ed25519 or RSA.
	Algorithm string

	// ClaimedSigner is a signer identity stated inside the signature, which
	// OpenPGP allows but few signers include.
	ClaimedSigner string

	// NoAllowedSigners records that no allowed-signers file was in effect when
	// an SSH signature was checked. git then reports only whether the signature
	// matches the key embedded in it, which anyone can arrange.
	NoAllowedSigners bool
}

// Signature types, as read from the signature's armor.
const (
	SigGPG     = "GPG"
	SigSSH     = "SSH"
	SigX509    = "X.509"
	SigUnknown = "unknown"
)

// Signed reports whether the commit carries a signature at all, whether or
// not git could verify it.
func (s Signature) Signed() bool {
	return s.Type != "" || (s.Status != "" && s.Status != "N")
}

// Verified reports whether git checked the signature against a key the
// examiner holds and found it good.
//
// An SSH signature carries its own public key. When git cannot match that key
// to a principal in an allowed-signers file, it falls back to checking the
// signature against the embedded key and reports "U". That proves nothing
// about who made it, so for SSH it does not count.
func (s Signature) Verified() bool {
	switch s.Status {
	case "G", "X", "Y":
		return true
	case "U":
		return s.Type != SigSSH
	}
	return false
}

// Anomaly is one integrity or provenance finding, rendered as a single line.
type Anomaly struct {
	// Kind is a stable identifier, e.g. "date-skew".
	Kind string

	// Detail is a short human-readable reason.
	Detail string
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
