# repopsy

<p align="center">
    <a href="https://github.com/andpalmier/repopsy/blob/main/LICENSE"><img alt="Software License" src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg"></a>
    <a href="https://godoc.org/github.com/andpalmier/repopsy"><img alt="GoDoc Card" src="https://godoc.org/github.com/andpalmier/repopsy?status.svg"></a>
    <a href="https://goreportcard.com/report/github.com/andpalmier/repopsy"><img alt="Go Report Card" src="https://goreportcard.com/badge/github.com/andpalmier/repopsy?style=flat-square"></a>
    <a href="https://x.com/intent/follow?screen_name=andpalmier"><img src="https://img.shields.io/twitter/follow/andpalmier?style=social&logo=x" alt="follow on X"></a>
</p>

<p align="center">
  <img src="repopsy_demo.gif" alt="Repopsy Demo">
</p>

**Repopsy** stands for **Rep**ository aut**opsy**.

**Repopsy** is an OSINT tool to gather information on a git repository, it takes a git repo and *"explodes it"*: creating a snapshot folder for every commit, enabling easy comparison, analysis, and archival of code evolution.

How It Works:

1. Resolves the path to its repository root and validates it
2. Lists commits, gathering every metadata field in a single `git log`
3. Creates worker goroutines
4. Each worker reads the commit's tree with `git ls-tree` and `git cat-file`
5. Writes a forensic record and a checksum record to each snapshot
6. Writes the reflog, tags, identities, repository state and run provenance

Any path inside a repository names the repository itself, so `repopsy .` behaves
the same from any subdirectory. Bare repositories are supported.

Trees are read directly rather than through `git archive`, because `git archive`
honours `export-ignore` — which would let a repository withhold files from its
own snapshot. `tar` is not required.

Requires `git` 2.31 or newer. No other runtime dependency.

## Installation

### With Homebrew

```bash
brew install andpalmier/tap/repopsy
```

### With Go

```bash
go install github.com/andpalmier/repopsy@latest
```

### Pre-built Binaries

Download pre-built binaries from the [Releases](https://github.com/andpalmier/repopsy/releases) page:

**Linux:**

```bash
curl -LO https://github.com/andpalmier/repopsy/releases/latest/download/repopsy_linux_amd64.tar.gz
tar -xzf repopsy_linux_amd64.tar.gz
sudo mv repopsy /usr/local/bin/
```

**macOS:**

```bash
curl -LO https://github.com/andpalmier/repopsy/releases/latest/download/repopsy_darwin_arm64.tar.gz
tar -xzf repopsy_darwin_arm64.tar.gz
sudo mv repopsy /usr/local/bin/
```

### Docker

```bash
docker pull ghcr.io/andpalmier/repopsy:latest
docker run --rm -v "$(pwd):/repo" ghcr.io/andpalmier/repopsy:latest /repo
```

### From Source

```bash
git clone https://github.com/andpalmier/repopsy.git
cd repopsy
go build -o repopsy .
```

## Usage

```bash
repopsy [flags] <repository-path>
```

### Basic execution

```bash
repopsy .
```

### Options

| Flag | Description | Default |
|------|-------------|---------|
| `-o`, `--output` | Output directory | `./<repo-name>-exploded` |
| `-w`, `--workers` | Number of parallel workers, per branch (max 32) | CPUs, capped at 32 |
| `-n`, `--limit` | Maximum number of commits to extract | 0 (all) |
| `-b`, `--branch` | Branch to extract from | all local branches |
| `-v`, `--verbose` | Show detailed output per commit | false |
| `--include-rewritten` | Also extract commits recovered from the reflog that no branch reaches | false |
| `-h`, `--help` | Show help message | false |
| `--version` | Show version information | false |

### Examples

Extract last 5 commits:

```bash
repopsy -n 5 /path/to/repo
```

Extract from a specific branch:

```bash
repopsy -b main /path/to/repo
```

Verbose output:

```bash
repopsy -v .
```

Extract with 8 workers:

```bash
repopsy -w 8 /path/to/repo
```

## Output Structure

When extracting all branches, each branch name becomes a directory path. A
branch containing `/` nests, mirroring its ref path:

```
<repo>-exploded/
├── EXTRACTION.txt                <- provenance for the whole run
├── REFLOG.txt                    <- every recorded ref movement
├── TAGS.txt                      <- tags and their attestations
├── IDENTITIES.txt                <- who appears in the history
├── REPOSITORY.txt                <- local config and installed hooks
├── main/
│   ├── 20231205_143022_abc1234/
│   │   ├── COMMIT_INFO.txt
│   │   ├── SHA256SUMS
│   │   └── ... (source files)
│   └── 20231205_150000_def5678/
├── feature/
│   └── login/                    <- branch "feature/login"
│       └── ...
├── develop/
│   └── ...
└── HEAD/                         <- only with --include-rewritten:
    └── ...                          work abandoned on a detached head
```

A commit reachable from several branches is extracted under each of them. That
duplication is deliberate: each branch directory is a complete account of that
branch's history, rather than a set of pointers into a shared pool.

`HEAD/` appears only with `--include-rewritten`, and holds commits that HEAD's
reflog remembers but no branch reaches — typically work committed on a detached
head and then abandoned. It cannot collide with a branch directory, because git
refuses `HEAD` as a branch name.

Nesting rather than flattening `/` to `_` means `feature/login` and
`feature_login` stay distinct. Git already refuses to create a branch `main/x`
while `main` exists, so no two branches can claim the same directory.

When extracting a single branch:
```
<repo>-exploded/
├── EXTRACTION.txt
├── (and the other root records)
├── 20231205_143022_abc1234/
│   ├── COMMIT_INFO.txt
│   ├── SHA256SUMS
│   └── ... (source files)
└── 20231205_150000_def5678/
```

Snapshot directory names are timestamped in **the offset the commit itself
records**, never the offset of the machine running repopsy, and the short hash is
derived from the full commit hash rather than from git's `%h` — which honours
`core.abbrev` from the examined repository's own configuration. The same
repository therefore always explodes into the same directory names, on any host
and whatever the examined repository is configured to do.

## Known Limitations

- **Branch names and Windows paths.** Some ref names are legal in git but cannot
  be directory names on Windows: reserved device names (`aux`, `CON`, `NUL`,
  `COM1`…) and the characters `<`, `>`, `"`, `|`. Extracting all branches from a
  repository containing such a branch will fail on Windows. Linux and macOS are
  unaffected.
- **"All branches" means local branches.** Only `refs/heads/` is listed;
  remote-tracking refs are not extracted. Tags are recorded in `TAGS.txt` but
  their targets are only extracted if a branch reaches them. On a fresh clone
  this is usually a single branch — fetch or check out the branches you want
  first, or pass `-b`.
- **Reflog recovery needs the original repository.** Reflogs are local and are
  not transferred by clone, so `--include-rewritten` finds nothing in a bare
  mirror or a fresh clone. Reflog entries also expire (`gc.reflogExpire`,
  90 days by default).
- **Detached-head recovery needs all-branches mode.** `HEAD/` is only produced
  when no `-b` is given: naming one branch means that branch's history. Combine
  `--include-rewritten` with no `-b` to recover everything the reflogs remember.
- **Submodule content is not captured.** git stores only a pointer; the pointer
  is recorded in `COMMIT_INFO.txt`.
- **Stashes are reported but not extracted.** `refs/stash` has a reflog, so
  `REFLOG.txt` lists every stash entry with its commit hash and message, and the
  content is retrievable from the original repository with
  `git stash show -p` or `git cat-file`. `--include-rewritten` does not
  materialise stash entries as snapshots: a stash is a three-parent merge whose
  tree is a working state rather than a commit in any branch's history, and
  giving it a snapshot directory would misrepresent it as one.

## Root Records

Alongside the snapshots, repopsy writes five **root records** describing the run
rather than any single commit.

**`EXTRACTION.txt`** — provenance of the run: which repopsy build produced it,
start and finish, scope, worker count and limit, per-branch commits against
snapshots written, and any failures with reasons.

**`REFLOG.txt`** — every recorded movement of every ref: what moved, to what,
when, by whom, and git's own description of why. A reset or force-push leaves the
commits it replaced unreachable, so this is the record of history that was
rewritten away. Reflogs are **local and are not transferred by clone**, so this
is empty for a bare mirror or a fresh clone however much was rewritten upstream —
an empty log is not proof that nothing happened.

**`TAGS.txt`** — every tag with its target. An annotated tag also carries its own
tagger identity, date and signature, which is an attestation separate from the
commit it points at.

**`IDENTITIES.txt`** — every distinct name and email pair, with per-role counts
and first and last seen, plus collisions: one email under several names, or one
name under several emails. Invisible commit by commit, and how both impersonation
and a reconfigured client look.

**`REPOSITORY.txt`** — the repository's local configuration verbatim and its
installed hooks, with each hook's size, SHA-256, executable bit and content.
Neither is versioned, so no commit contains them; a malicious hook is a real
attack and never appears in any history walk.

## Integrity

Every snapshot contains an **integrity record**, `SHA256SUMS`, listing the
SHA-256 of each extracted file in the format `sha256sum -c` reads:

```bash
cd <repo>-exploded/main/20231205_143022_abc1234
sha256sum -c SHA256SUMS
```

Together with `EXTRACTION.txt` this closes the chain of custody: the manifest
says how the snapshots were produced, and the checksums show they have not been
altered since.

## Forensic Record

Each snapshot folder includes a `COMMIT_INFO.txt` recording everything git knows
about that commit.

**Identity** — commit hash, abbreviated hash, and tree hash. The tree hash
identifies the content independently of commit metadata, so identical trees can
be spotted across rewritten or cherry-picked history.

**Refs** — the branches and tags pointing at the commit, when any do.

**Dates** — author and committer dates in ISO 8601, carrying **the UTC offset
git recorded**, plus the Unix timestamp. The recorded offset indicates the
author's locale and working hours, so it is preserved rather than being
re-rendered in the timezone of whoever runs the tool. Output is therefore
identical on every host.

**Signature** — the verification verdict *and* the signer's identity: declared
name, key ID, fingerprint, and the trust level git assigns the key. A valid
signature made by an unexpected key is indistinguishable from a legitimate one
if only the verdict is recorded.

**Lineage** — parent hashes, or an explicit note for a root commit.

**Changed files** — every path the commit touched, with its status letter
(`A` added, `M` modified, `D` deleted, `R` renamed, `C` copied, `T` type
changed), line counts, a blob hash, and both file modes. A mode change is called
out explicitly: `100644 -> 100755` means a file became executable.

The blob hash names the content in the object store, so it can be retrieved and
verified against the original repository:

```bash
git cat-file -p 615eb37b3006
```

For a deletion the hash is the **removed** content's, since that is the only
pointer left to what the file contained — git reports a deletion with an
all-zero new blob, so recording that instead would lose it.

**Change statistics** are measured against the commit's first parent. A merge
commit is measured the same way, so changes brought in from the other side of
the merge are not counted twice.

**Submodules** — a gitlink records only a pointer to a commit in another
repository, so the content cannot be in the snapshot. The pointer and its target
are recorded rather than the entry being silently omitted.

**Anomalies** — a commit whose author date is *later* than its committer date is
flagged; this does not occur in normal use and indicates a rewritten or forged
date. A commit recovered with `--include-rewritten` is flagged as unreachable,
which is itself evidence that history was rewritten after it was made.

**Message and notes** — subject, full message, declared encoding, and any git
notes attached to the commit.

**Example `COMMIT_INFO.txt` content:**

```text
COMMIT INFORMATION
===========================

Hash:           58bb650e3a850c51fe605f8725a7338d903a061c
Short Hash:     58bb650
Tree:           344892c8bb98059a9e529b0833c4b4a6e5907f66
Refs:           origin/main, origin/HEAD, main

AUTHOR (who wrote the code)
---------------------------
Name:           Alice Dev
Email:          alice@example.com
Date:           2023-12-05T14:30:22+01:00
Timestamp:      1701786622

COMMITTER (who applied the commit)
----------------------------------
Name:           Bob Ops
Email:          bob@example.com
Date:           2023-12-05T15:00:00+01:00
Timestamp:      1701788400

NOTE: Author and Committer are different.

VERIFICATION
------------
GPG Signature:  Valid signature (good)
Signer:         Alice Dev <alice@example.com>
Key:            ABCD1234EF567890
Fingerprint:    FFFF0000AAAA1111BBBB2222CCCC3333DDDD4444
Trust:          ultimate

LINEAGE
-------
Parents:        7e5d1c2b

CHANGE STATISTICS
-----------------
Files Changed:  3
Insertions:     +5
Deletions:      -3

CHANGED FILES
-------------
Status, line counts, the new content's blob hash, and the path.

M    +1      -1      9817e7ca8f21 go.mod
M    +4      -2      bbb2222aaa11 scripts/deploy.sh  [mode 100644 -> 100755]
A    binary          ccc3333ddd44 demo.gif
D    +0      -99     ddd4444eee55 old/removed.go

COMMIT MESSAGE
--------------
Subject:
Fix the extraction logic

Full Message:
Fix the extraction logic

Longer body here.
```

## Extraction Manifest

A folder of snapshots says nothing about where it came from. `EXTRACTION.txt` at
the output root records the provenance of the run itself: which repopsy build
produced it, when it started and finished, which repository and scope, the
worker count and commit limit, a per-branch breakdown of commits found against
snapshots written, and any failures with their reasons.

**Example `EXTRACTION.txt` content:**

```text
REPOPSY EXTRACTION MANIFEST
===========================

Provenance record for this exploded repository. Snapshot directories and their
COMMIT_INFO.txt files describe individual commits; this file describes how the
extraction itself was performed.

TOOL
----
Version:        1.4.0
Build commit:   58bb650
Built:          2023-12-05T12:00:00Z

EXTRACTION
----------
Started:        2023-12-05T15:10:10+01:00
Finished:       2023-12-05T15:10:19+01:00
Duration:       9.412s

SOURCE
------
Repository:     /repos/demo
Scope:          all local branches
Workers:        8
Commit limit:   none

BRANCHES
--------
main                                     412 commits, 412 snapshots
feature/login                            37 commits, 37 snapshots
stale-branch                             no commits

RESULTS
-------
Snapshots:      449
Failures:       0
```
