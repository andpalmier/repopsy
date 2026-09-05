# repopsy

<p align="center">
    <a href="https://github.com/andpalmier/repopsy/blob/main/LICENSE"><img alt="Software License" src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg"></a>
    <a href="https://godoc.org/github.com/andpalmier/repopsy"><img alt="GoDoc Card" src="https://godoc.org/github.com/andpalmier/repopsy?status.svg"></a>
    <a href="https://github.com/andpalmier/repopsy/actions/workflows/ci.yaml"><img alt="CI" src="https://github.com/andpalmier/repopsy/actions/workflows/ci.yaml/badge.svg"></a>
    <a href="https://x.com/intent/follow?screen_name=andpalmier"><img src="https://img.shields.io/twitter/follow/andpalmier?style=social&logo=x" alt="follow on X"></a>
</p>

<p align="center">
  <img src="repopsy_demo.gif" alt="Repopsy Demo">
</p>

**Repopsy** stands for **Rep**ository aut**opsy**.

**Repopsy** is an OSINT tool to gather information on a git repository, it takes a git repo and *"explodes it"*: creating a snapshot folder for every commit, enabling easy comparison, analysis, and archival of code evolution.

Every snapshot holds the commit's complete working tree, a forensic record of
everything git knows about that commit, and a checksum record. At the output
root repopsy writes the reflog, the tags, the identities seen in the history,
the repository's unversioned state, and the provenance of the run itself.

Any path inside a repository names the repository itself, so `repopsy .` behaves
the same from any subdirectory. Bare repositories work too.

repopsy reads trees directly instead of going through `git archive`. `git
archive` honours `export-ignore`, which would let a repository withhold files
from its own snapshot, and it also needs `tar` on the machine.

You need `git` 2.31 or newer, and nothing else.

## Installation

### With Homebrew

```bash
brew install --cask andpalmier/tap/repopsy
```

Homebrew casks are macOS only. On Linux, use `go install` or a pre-built binary.

### With Go

```bash
go install github.com/andpalmier/repopsy/v2@latest
```

### Pre-built binaries

Archives for Linux, macOS and Windows are on the
[Releases](https://github.com/andpalmier/repopsy/releases) page. Each is named
`repopsy_<version>_<os>_<arch>`, so pick the one matching your machine, unpack
it and put `repopsy` somewhere on your `PATH`.

### Docker

```bash
docker pull ghcr.io/andpalmier/repopsy:latest

docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$(pwd):/repo:ro" \
  -v "$(pwd)/exploded:/data" \
  ghcr.io/andpalmier/repopsy:latest /repo
```

Both mounts matter. The repository goes in read-only, since repopsy never writes
to the repository it examines. The second mount catches the output: `/data` is
the image's working directory, so snapshots land there when you give no `-o`.
Skip that mount and the container writes them into itself, then loses them when
it exits.

You need `--user` as well. A bind mount keeps the host's ownership, so the
image's own user cannot write into a directory you own and the run stops at
`permission denied`.

The image follows the OCI standard, so the same commands work with any
compatible runtime, including Apple's `container` on macOS.

### From source

```bash
git clone https://github.com/andpalmier/repopsy.git
cd repopsy
go build -o repopsy .
```

## Usage

```bash
repopsy [flags] <repository-path>
```

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

```bash
repopsy .                          # every local branch
repopsy -n 5 /path/to/repo         # the last 5 commits
repopsy -b main /path/to/repo      # one branch
repopsy -w 8 -v /path/to/repo      # 8 workers, per-commit output
repopsy --include-rewritten .      # also what the reflog remembers
```

## Acquiring a repository

How you obtain the repository decides how much of it exists to explode.

```bash
git clone --mirror git@example.com:acme/demo.git demo.git
repopsy demo.git
```

`--mirror` (and `--bare`) map every branch into `refs/heads/`, so repopsy sees
all of them. A plain `git clone` checks out one branch and leaves the rest under
`refs/remotes/`, where repopsy does not look, so you get one branch's snapshots
and nothing else.

Two things never survive a clone, and both need the original repository. The
first is reflogs, and with them `--include-rewritten`: a clone starts its own
reflog, so rewritten history and abandoned detached-head work stay recoverable
only from the repository where they happened. The second is repository state,
meaning local configuration and installed hooks, which git does not transfer, so
`REPOSITORY.txt` ends up describing the clone rather than the original.

If either of those matters to the examination, work from a copy of the
repository directory itself instead of a clone.

## Output structure

Each branch name becomes a directory path, and a branch containing `/` nests,
mirroring its ref path:

```
<repo>-exploded/
├── EXTRACTION.txt                <- provenance for the whole run
├── REFLOG.txt                    <- every recorded ref movement
├── TAGS.txt                      <- tags and their attestations
├── IDENTITIES.txt                <- who appears in the history
├── REPOSITORY.txt                <- local config and installed hooks
└── refs/
    ├── main/
    │   └── 20231205_143022_abc1234/
    │       ├── COMMIT_INFO.txt   <- the commit's record
    │       ├── SHA256SUMS        <- the integrity record
    │       └── tree/             <- the commit's working tree
    ├── feature/
    │   └── login/                <- branch "feature/login"
    └── HEAD/                     <- only with --include-rewritten:
                                     work abandoned on a detached head
```

That layout keeps two things apart on purpose. Repository content lives under
`tree/`, and ref directories live under `refs/`, never beside the root records.
A commit may legitimately contain a file called `COMMIT_INFO.txt`, and a branch
may legitimately be called `EXTRACTION.txt`. Without the separation repopsy
would overwrite the first and be displaced by the second, destroying evidence in
one case and hiding it in the other.

Naming a single branch with `-b` drops the `refs/` level, since snapshot
directory names come from a timestamp and a hash and cannot collide with a
record. A commit reachable from several branches is extracted under each of
them, so every branch directory is a complete account of that branch's history.
Nesting rather than flattening `/` to `_` keeps `feature/login` and
`feature_login` distinct.

Snapshot directory names carry the offset the commit itself records, never the
offset of the machine running repopsy, and the short hash comes from the full
commit hash rather than from git's `%h`, which honours `core.abbrev` in the
examined repository. The same repository therefore always explodes into the same
directory names, on any host.

## Root records

`EXTRACTION.txt` records the provenance of the run: which repopsy build produced
it, when it started and finished, the scope, the worker count and limit,
per-branch commits against snapshots written, and any failures with reasons.

`REFLOG.txt` records every movement of every ref: what moved, to what, when, by
whom, and git's own description of why. A reset or force-push leaves the commits
it replaced unreachable, so this is the record of the history that was rewritten
away. Reflogs are local and clone does not transfer them, so this file is empty
for a bare mirror however much was rewritten upstream. An empty log is not proof
that nothing happened.

`TAGS.txt` lists every tag with its target. An annotated tag also carries its
own tagger identity, date and signature, an attestation separate from the commit
it points at.

`IDENTITIES.txt` lists every distinct name and email pair, with per-role counts
and first and last seen, plus collisions: one email under several names, or one
name under several emails. Neither collision is visible commit by commit, and
that is how both impersonation and a reconfigured client look.

`REPOSITORY.txt` holds the repository's local configuration verbatim and its
installed hooks, with each hook's size, SHA-256, executable bit and content. Git
versions neither, so no commit contains them, and a malicious hook is a real
attack that never shows up in any history walk.

## Integrity

Every snapshot contains `SHA256SUMS`, listing the SHA-256 of each extracted file
in the format `sha256sum -c` reads. Paths are relative to the snapshot
directory, where the record itself lives, so verification runs from there with
no extra flags:

```bash
cd <repo>-exploded/refs/main/20231205_143022_abc1234
sha256sum -c SHA256SUMS
```

Together with `EXTRACTION.txt` this closes the chain of custody: the manifest
says how the snapshots were produced, and the checksums show they have not been
altered since.

## Forensic record

Each snapshot holds a `COMMIT_INFO.txt` recording everything git knows about
that commit.

It opens with the commit hash, the abbreviated hash and the tree hash. The tree
hash identifies the content independently of commit metadata, so you can spot
identical trees across rewritten or cherry-picked history. Then the refs
pointing at the commit, when any do.

Dates follow, in ISO 8601, carrying the UTC offset git recorded, plus the Unix
timestamp. That offset indicates the author's locale and working hours, so
repopsy preserves it rather than re-rendering the date in the timezone of
whoever runs the tool.

The signature block records the verification verdict *and* the signer's
identity: declared name, key ID, fingerprint, and the trust level git assigns
the key. If you record only the verdict, a valid signature made by an unexpected
key looks exactly like a legitimate one. Lineage follows, listing parent hashes
or an explicit note for a root commit.

Changed files lists every path the commit touched, with its status letter (`A`
added, `M` modified, `D` deleted, `R` renamed, `C` copied, `T` type changed),
line counts, a blob hash, and both file modes, calling out a mode change
explicitly: `100644 -> 100755` means a file became executable. The blob hash
names the content in the object store, so `git cat-file -p <hash>` retrieves it
from the original repository. For a deletion the hash is the removed content's,
since that is the only pointer left to what the file contained.

Change statistics are measured against the commit's first parent, merges
included, so changes from the other side of a merge are not counted twice. A
gitlink records only a pointer to a commit in another repository, so submodule
content cannot be in the snapshot; repopsy records the pointer rather than
silently omitting the entry.

Anomalies come last. A commit whose author date is *later* than its committer
date gets flagged, since that does not happen in normal use and indicates a
rewritten or forged date. A commit recovered with `--include-rewritten` is
flagged as unreachable, itself evidence that history was rewritten after it was
made. The record closes with the subject, full message, declared encoding, and
any git notes.

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

## Known limitations

- Some ref names are legal in git but cannot be directory names on Windows:
  reserved device names (`aux`, `CON`, `NUL`, `COM1`) and the characters `<`,
  `>`, `"`, `|`. There such a branch's snapshots fail while the rest of the run
  completes, and `EXTRACTION.txt` lists every failure with its reason. repopsy
  does not sanitise the name on purpose: a snapshot directory that does not
  match the ref it came from would misattribute evidence. Linux and macOS are
  unaffected.
- "All branches" means `refs/heads/`. Remote-tracking refs under `refs/remotes/`
  are not extracted, so a plain `git clone` yields a single branch. `TAGS.txt`
  records tags, but their targets are extracted only when a branch reaches them.
- Reflog recovery needs the original repository, since clone does not transfer
  reflogs. Entries also expire (`gc.reflogExpire`, 90 days by default).
- `refs/HEAD/` is produced only when you give no `-b`, since naming one branch
  means that branch's history.
- Submodule content is not captured. git stores only a pointer, and
  `COMMIT_INFO.txt` records it.
- Stashes are reported but not extracted. `REFLOG.txt` lists every stash entry
  with its hash and message, and `git stash show -p` retrieves the content from
  the original repository. A stash is a three-parent merge whose tree is a
  working state rather than a commit in any branch's history, so giving it a
  snapshot directory would misrepresent it as one.
