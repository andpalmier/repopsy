package git

import (
	"context"
	"fmt"
	"os"
	"sync"
)

const (
	// dirPerms is the mode for directories created to hold a snapshot.
	dirPerms = 0o755

	// git's tree entry modes.
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"

	// maxLinkDepth bounds how many symlinks resolving one target may follow,
	// the same bound the kernel applies. A cycle counts as an escape.
	maxLinkDepth = 40

	// renameSuffix marks a path component repopsy had to rename.
	renameSuffix = "~repopsy"
)

// Submodule is a gitlink: a pointer to a commit in another repository.
//
// git stores only the pointer, so a snapshot cannot contain the submodule's
// content. Recording the pointer is the difference between an incomplete
// snapshot and a silently incomplete one.
type Submodule struct {
	Path   string
	Commit string
}

// ExtractResult describes what one extraction produced.
type ExtractResult struct {
	// Submodules are the gitlinks encountered, whose content is not captured.
	Submodules []Submodule

	// Digests is the SHA-256 of every regular file in the tree, in tree order,
	// by the path it was written to. The bytes pass through this process anyway,
	// so hashing them is nearly free — and it lets a reader prove the snapshot
	// was not altered after extraction. Symlinks are not listed: sha256sum
	// would follow them and hash what they point at.
	Digests []FileDigest

	// Tree records every entry not written exactly as git records it.
	Tree TreeReport
}

// FileDigest is one extracted file and the hash of its content.
type FileDigest struct {
	Path   string
	SHA256 string
}

// TreeReport records what extraction did with a commit's tree wherever the
// files under tree/ differ from what git records. Without it a neutralised mode
// or a renamed path would silently misstate the evidence.
type TreeReport struct {
	// Special lists every entry whose git mode a plain file does not carry:
	// executables and symlinks. It is the authoritative record of modes.
	Special []SpecialEntry

	// Issues lists entries that were skipped or written under another name.
	Issues []PathIssue

	// Filtered counts entries left out because they matched no --paths pattern.
	Filtered int

	// NotWritten is set when no files were written at all (--no-tree).
	NotWritten bool
}

// SpecialEntry is one executable or symlink tree entry.
type SpecialEntry struct {
	Path string
	Mode string

	// Target is a symlink's target, as recorded in git.
	Target string

	// Written says how the entry appears under tree/.
	Written string
}

// PathIssue is one tree entry that was not written at the path git records.
type PathIssue struct {
	Path string

	// WrittenAs is where the entry was written instead, or "" if it was
	// skipped. The content of a skipped entry is still named by its blob hash.
	WrittenAs string

	Reason string
}

// ExtractOptions controls how a commit's tree is written. The zero value is
// the safe default.
type ExtractOptions struct {
	// PreserveModes writes executable files with their executable bit.
	//
	// Off by default: the tree comes from a repository under examination, and a
	// script that keeps its bit is one double-click away from running. The git
	// mode is recorded in TreeReport.Special either way.
	PreserveModes bool

	// NoTree computes the integrity record without writing any files.
	NoTree bool

	// Paths limits extraction to matching entries. Nil means everything.
	Paths *PathFilter

	// Digests caches blob digests across commits. With NoTree, a blob already
	// in the cache is not read again, which is what makes hashing every commit
	// affordable: most blobs are shared between commits.
	Digests *DigestCache
}

// DigestCache maps blob hashes to the SHA-256 of their content. Safe for
// concurrent use.
type DigestCache struct {
	mu   sync.Mutex
	sums map[string]string
}

// NewDigestCache returns an empty cache.
func NewDigestCache() *DigestCache {
	return &DigestCache{sums: map[string]string{}}
}

func (c *DigestCache) get(blob string) (string, bool) {
	if c == nil {
		return "", false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	sum, ok := c.sums[blob]
	return sum, ok
}

func (c *DigestCache) put(blob, sum string) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	c.sums[blob] = sum
}

// Extract writes a commit's tree into destPath.
//
// The tree is read with ls-tree and cat-file rather than piped through
// "git archive | tar", for two reasons. git archive honours export-ignore from
// the repository's own .gitattributes, which lets the subject of an
// investigation withhold files from its own snapshot — it cannot be overridden,
// as pathspecs and --worktree-attributes both still respect it and there is no
// --no-export-ignore. And it removes tar as an undeclared runtime dependency.
// See docs/adr/0005.
//
// Every decision about where an entry goes is made from the tree listing
// before anything is written, so it is the same on every host. See planTree.
func (r *Repository) Extract(ctx context.Context, hash, destPath string, opts ExtractOptions) (ExtractResult, error) {
	entries, err := r.listTree(ctx, hash)
	if err != nil {
		return ExtractResult{}, err
	}

	if !opts.NoTree {
		if err := os.MkdirAll(destPath, dirPerms); err != nil {
			return ExtractResult{}, fmt.Errorf("failed to create output directory: %w", err)
		}
	}

	return r.writeTree(ctx, destPath, entries, opts)
}
