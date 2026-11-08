package cmd

import (
	"fmt"
	"io"
)

const usage = `repopsy - Explode git repositories by extracting each commit into a separate folder

A forensic tool for analyzing git repository history (Repository Autopsy) by extracting
each commit's state into a separate folder for comparison and analysis.

Any path inside a repository names the repository itself. Requires git 2.31 or
newer, and nothing else at runtime.

Acquire with "git clone --mirror" so every branch lands in refs/heads and none
are missed; a plain clone leaves them under refs/remotes, where repopsy does not
look. Reflogs and repository state do not survive any clone, so --include-rewritten
and REPOSITORY.txt need the original repository.

Usage:
  repopsy [flags] <repository-path>
  repopsy identities [--json] [--ignore PATTERN]... [--no-default-ignore] <exploded-dir>...

Output, at the root:
  EXTRACTION.txt   provenance: tool build, times, scope, per-ref counts, failures,
                   ref namespaces, shallow boundary, signature and anomaly summaries
  REFLOG.txt       every recorded ref movement - the record of rewritten history
  TAGS.txt         tags, with the tagger identity and signature of annotated ones
  IDENTITIES.txt   distinct name/email pairs, collisions, placeholder identities
                   and conflicting GitHub noreply addresses
  REPOSITORY.txt   local config and installed hooks, neither of them versioned
  STRINGS.txt      with --track: where each string appears and changes
  SECRETS.txt      with --scan-secrets: masked credential-shaped strings

Output, in each snapshot under refs/<ref>/<timestamp>_<hash>/:
  COMMIT_INFO.txt  the commit's forensic record: identity and tree hash, refs,
                   dates with the offset git recorded, signature, signer and
                   issuing key, lineage, changed files with blob hashes, modes
                   and status, submodule pointers, entries whose mode or path
                   tree/ does not reproduce, anomalies, message and notes
  SHA256SUMS       SHA-256 of every regular file in tree/, for "sha256sum -c"
  tree/            that commit's working tree

  With --json, every record above also gets a .json twin (COMMIT_INFO.json,
  EXTRACTION.json, TAGS.json, IDENTITIES.json, STRINGS.json, SECRETS.json).

  Content sits under tree/ and refs under refs/ so that repository names can
  never collide with repopsy's own records: a commit may contain a file called
  COMMIT_INFO.txt and a branch may be called EXTRACTION.txt.

  Snapshot directories are named <timestamp>_<short-hash>, timestamped in the
  offset the commit records, so output is identical on any host. Branch names
  containing "/" nest as directories. Refs outside refs/heads/ go under
  other-refs/ by their path, e.g. other-refs/pull/12/head/. A commit on several
  refs is extracted under each of them, unless --dedupe stores it once under
  commits/ and links each ref's directory to it.

  With --include-rewritten and no -b, a refs/HEAD/ directory holds commits HEAD's
  reflog remembers that no branch reaches - work abandoned on a detached head.

Safety:
  The tree comes from a repository under examination, so files under tree/ are
  never executable unless --preserve-modes is given, a symlink is kept only if
  it resolves inside tree/ (otherwise it becomes a file holding its target), and
  a path that would leave tree/, make it a git repository (.git) or collide on
  a case-insensitive filesystem is skipped or renamed. COMMIT_INFO.txt records
  every such entry with its git mode. Nothing from the repository is run: its
  hooks, filters, textconv drivers and gpg/fsmonitor settings are not honoured.

Examples:
  # Explode all commits from all local branches
  repopsy .

  # Explode the last 5 commits
  repopsy -n 5 /path/to/repo

  # Explode a specific branch only
  repopsy -b main /path/to/repo

  # Explode with verbose output
  repopsy -v .

  # Also recover commits a reset or force-push left unreachable
  repopsy --include-rewritten .

  # A GitHub mirror with its pull requests, each commit stored once
  repopsy --refs 'refs/heads/*' --refs 'refs/pull/*/head' --dedupe repo.git

  # How big would it be? Then records only, no trees
  repopsy --dry-run --all-refs repo.git
  repopsy --no-tree --json repo.git

  # When did a string first appear, and where is it now?
  repopsy --no-tree --track 'Mozilla/5.0 (X11' repo.git

  # Which identities do two investigated repositories share?
  repopsy identities a-exploded b-exploded

Flags:
`

// printUsage writes the help text followed by the generated flag list.
func printUsage(w io.Writer) {
	_, _ = fmt.Fprint(w, usage)
	var o options
	fs := newFlagSet(&o)
	fs.SetOutput(w)
	fs.PrintDefaults()
}

// printVersion writes version information, omitting fields that were not set
// at build time.
func printVersion(w io.Writer, version, commit, date string) {
	_, _ = fmt.Fprintf(w, "%s version %s\n", appName, version)
	if commit != "none" {
		_, _ = fmt.Fprintf(w, "  commit: %s\n", commit)
	}
	if date != "unknown" {
		_, _ = fmt.Fprintf(w, "  built:  %s\n", date)
	}
}
