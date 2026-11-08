# repopsy

<p align="center">
    <a href="https://github.com/andpalmier/repopsy/blob/main/LICENSE"><img alt="Software License" src="https://img.shields.io/badge/License-AGPL--3.0-blue.svg"></a>
    <a href="https://godoc.org/github.com/andpalmier/repopsy"><img alt="GoDoc Card" src="https://godoc.org/github.com/andpalmier/repopsy?status.svg"></a>
    <a href="https://github.com/andpalmier/repopsy/actions/workflows/ci.yaml"><img alt="CI" src="https://github.com/andpalmier/repopsy/actions/workflows/ci.yaml/badge.svg"></a>
    <a href="https://x.com/intent/follow?screen_name=andpalmier"><img src="https://img.shields.io/twitter/follow/andpalmier?style=social&logo=x" alt="follow on X"></a>
</p>

<p align="center">
  <img src="repopsy_demo.gif" alt="Repopsy demo">
</p>

Repopsy is an OSINT tool that explodes a Git repository into one snapshot folder per commit. Each snapshot holds the commit's working tree, a forensic record, and a checksum record. At the output root it writes the reflog, tags, identities, repository state, and run provenance.

Repopsy reads trees with `git ls-tree` and `git cat-file`, not `git archive`, so a repository cannot use `export-ignore` to hide files from its own snapshot. The repositories it examines are treated as untrusted: extracted trees are inert, symlinks cannot escape `tree/`, and no path can leave it.

Requires git 2.31 or newer.

## Install

With Homebrew:

```bash
brew install --cask andpalmier/tap/repopsy
```

Homebrew casks are macOS only. On Linux, use `go install` or a prebuilt binary.

With Go:

```bash
go install github.com/andpalmier/repopsy/v2@latest
```

Prebuilt archives for Linux, macOS, and Windows are on the [Releases](https://github.com/andpalmier/repopsy/releases) page. Each archive is named `repopsy_<version>_<os>_<arch>`.

With Docker:

```bash
docker pull ghcr.io/andpalmier/repopsy:latest

docker run --rm \
  --user "$(id -u):$(id -g)" \
  -v "$(pwd):/repo:ro" \
  -v "$(pwd)/exploded:/data" \
  ghcr.io/andpalmier/repopsy:latest /repo
```

The repository mount is read-only. The `/data` mount receives the output. Keep `--user` so the container can write to a directory owned by your host user.

From source:

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
| `-w`, `--workers` | Parallel workers per branch, maximum 32 | CPUs, capped at 32 |
| `-n`, `--limit` | Maximum commits to extract | 0 (all) |
| `-b`, `--branch` | Branch to extract | all local branches |
| `-v`, `--verbose` | Show one line per commit | false |
| `--include-rewritten` | Also extract commits recovered from the reflog | false |
| `--refs PATTERN` | Extract refs matching `PATTERN`; repeatable | `refs/heads/` |
| `--all-refs` | Extract every ref except `refs/stash` and `refs/notes` | false |
| `--dedupe` | Store each commit once under `commits/` and link refs to it | false |
| `--preserve-modes` | Keep executable bits | false |
| `--no-tree` | Write records and `SHA256SUMS`, but no `tree/` | false |
| `--paths PATTERN` | Extract only matching paths; repeatable | everything |
| `--dry-run` | Report commits and estimated size, write nothing | false |
| `--json` | Also write each record as JSON | false |
| `--allowed-signers FILE` | Verify SSH signatures against this file | your git config |
| `--large-commit-files N` | Flag commits changing at least `N` files | 200 |
| `--large-commit-lines N` | Flag commits adding at least `N` lines | 20000 |
| `--date-skew DURATION` | Flag date gaps above this duration, such as `30d` or `72h`; also sets the rerooted-history threshold | `30d` |
| `--track STRING` | Record the history of `STRING` in `STRINGS.txt`; repeatable | none |
| `--scan-secrets` | Record masked credential-shaped strings in `SECRETS.txt` | false |
| `-h`, `--help` | Show help | false |
| `--version` | Show version information | false |

Flags must come before the repository path. `-b` cannot be combined with `--refs`, `--all-refs`, or `--dedupe`.

```bash
repopsy .
repopsy -n 5 /path/to/repo
repopsy -b main /path/to/repo
repopsy -w 8 -v /path/to/repo
repopsy --include-rewritten .

# A GitHub mirror with pull request refs, storing each commit once.
repopsy --refs 'refs/heads/' --refs 'refs/pull/*/head' --dedupe repo.git

# Estimate a run, or write records without trees.
repopsy --dry-run --all-refs repo.git
repopsy --no-tree --json repo.git

# Track a string through history.
repopsy --no-tree --track 'Mozilla/5.0 (X11' repo.git

# Compare identities across exploded repositories.
repopsy identities a-exploded b-exploded
```

### Choosing refs

By default repopsy extracts local branches under `refs/heads/`. `--refs` accepts the same patterns as `git for-each-ref`: a full ref name, a prefix ending in `/`, or a glob where `*` does not cross a slash. `--all-refs` selects everything except `refs/stash` and `refs/notes`.

Branches go under `refs/`. Other refs go under `other-refs/` by their path below `refs/`, so `refs/pull/12/head` becomes `other-refs/pull/12/head/`. `EXTRACTION.txt` lists every ref namespace and how many refs the run extracted. A ref that does not point at a commit is listed as skipped.

### Scale

Without `--dedupe`, a commit reachable from several refs is extracted once under each ref. This keeps every ref directory complete but duplicates shared history. `--dedupe` writes each distinct commit once under `commits/` and creates relative symlinks from each ref directory.

`--no-tree` keeps every record and still computes `SHA256SUMS`, using a digest cache so shared blobs are hashed once. `--paths` limits `tree/` to matching files or directories; `*` crosses directories, as in Git pathspecs. `--dry-run` reports commit counts and estimated tree bytes without creating the output directory.

## Acquiring a repository

A mirror is the best starting point because it maps every branch into `refs/heads/`:

```bash
git clone --mirror git@example.com:acme/demo.git demo.git
repopsy demo.git
```

A plain clone checks out one branch and leaves the others under `refs/remotes/`. Pass `--refs 'refs/remotes/'` to extract those. A GitHub mirror can also contain `refs/pull/*`.

Do not use `--depth`. A shallow clone cuts history at a boundary, and repopsy will warn and record the boundary commits, but it cannot recover the missing history. Run `git fetch --unshallow` first.

Reflogs and repository state do not survive a clone. To recover rewritten history or record the original repository state, examine a copy of the repository directory itself.

## Output structure

```text
<repo>-exploded/
├── EXTRACTION.txt
├── REFLOG.txt
├── TAGS.txt
├── IDENTITIES.txt
├── REPOSITORY.txt
├── STRINGS.txt                 # with --track
├── SECRETS.txt                 # with --scan-secrets
├── refs/
│   ├── main/
│   │   └── 20231205_143022_abc1234/
│   │       ├── COMMIT_INFO.txt
│   │       ├── SHA256SUMS
│   │       └── tree/
│   ├── feature/login/          # branch "feature/login"
│   └── HEAD/                   # with --include-rewritten
├── other-refs/
│   └── pull/12/head/           # ref "refs/pull/12/head"
└── commits/                    # with --dedupe
```

Repository content stays under `tree/`. Records stay beside it, so a file named `COMMIT_INFO.txt` in the repository cannot overwrite the record with the same name. Ref directories stay under `refs/` or `other-refs/`, so a branch named `EXTRACTION.txt` cannot replace the manifest.

With `-b`, snapshots go directly under the output directory because there is only one ref. Snapshot names use the commit's recorded date offset and short hash, so the same repository produces the same names on any host.

## Safety

Extracted trees are written so that browsing them cannot run code or escape the output directory.

- Files are written with mode `0644`. A file recorded as executable stays that way only with `--preserve-modes`.
- A symlink is kept only if its target, including any chain of symlinks, stays inside `tree/`. Otherwise repopsy writes a plain file containing the target and records the reason.
- Paths with empty, `.` or `..` components are skipped. A `.git` component is renamed to `.git~repopsy`. Case collisions are renamed on every host so output stays consistent.
- Files are created with `O_EXCL`. repopsy never overwrites an existing tree entry.
- Repopsy does not check out the repository, so its hooks and filters do not run. Git commands disable text conversion and external diff programs, and pin signing programs and allowed-signers settings.
- A symlinked `.git/config` or hook is recorded by target and not followed.

Every skipped or renamed path appears in `COMMIT_INFO.txt` and is summarized in `EXTRACTION.txt`.

## Root records

- `EXTRACTION.txt` records the build, start and finish times, scope, worker count, limit, shallow state, ref counts, failures, namespaces, signatures, anomalies, and path issues.
- `REFLOG.txt` records ref movements, including rewritten history when the repository still has reflogs.
- `TAGS.txt` lists tags and any tagger identity, date, and signature on annotated tags.
- `IDENTITIES.txt` lists name and email pairs with commit counts, first and last seen, collisions, placeholder identities, and GitHub noreply conflicts.
- `STRINGS.txt` appears with `--track` and shows where each string first appears, every commit that changes its count, and the count at each ref tip.
- `SECRETS.txt` appears with `--scan-secrets` and lists masked credential-shaped strings, their type, a hash prefix, and the commits and paths where they appear.
- `REPOSITORY.txt` records local configuration and installed hooks. Git does not version either one.

## Integrity

Each snapshot has `SHA256SUMS` in a format `sha256sum -c` accepts. Paths are relative to the snapshot directory:

```bash
cd <repo>-exploded/refs/main/20231205_143022_abc1234
sha256sum -c SHA256SUMS
```

Control characters in paths are renamed in `tree/`, and the original-to-written mapping is recorded so the checksum file stays valid.

## Forensic record

`COMMIT_INFO.txt` records the commit hash, short hash, tree hash, refs, author and committer dates with the offset Git recorded, and the Unix timestamp.

The signature block records Git's verification verdict and the signer identity, key ID, fingerprint, and trust level. Repopsy also reads the signature itself and records the type, issuer, and any claimed signer. A `Verified:` line states whether verification ran and why not. Use `--allowed-signers FILE` to verify SSH signatures.

The file also records parent hashes, changed files with status letters, line counts, blob hashes, modes, submodule pointers, tree issues, anomalies, the subject, the full message, encoding, and notes. Change statistics compare against the first parent, including for merges.

Anomalies flag author dates after committer dates, large date gaps, dates after a child commit, first-parent date regressions, multiple roots, rerooted history, large commits, placeholder identities, and unsigned GitHub web-flow commits. `EXTRACTION.txt` counts findings by kind.

## JSON output

`--json` adds JSON forms for `COMMIT_INFO`, `EXTRACTION`, `TAGS`, `IDENTITIES`, `STRINGS`, and `SECRETS`. `REFLOG` and `REPOSITORY` remain text-only. Each document has `schema_version: 1`. Field names use snake_case, lists are always present, and dates use ISO 8601 with the offset Git recorded.

## Identities across repositories

```bash
repopsy identities [--json] [--ignore PATTERN]... [--no-default-ignore] <exploded-dir>...
```

This reads `IDENTITIES` records from two or more exploded repositories and reports identities that appear in more than one. Emails are compared case-insensitively. GitHub noreply addresses are normalized to `github:<handle>`.

Bots are ignored by default: `*[bot]*`, `noreply@github.com`, `*dependabot*`, `github-actions*`, and `action@github.com`. `--ignore` adds patterns and `--no-default-ignore` removes the defaults. Patterns match the name, email, or GitHub handle. `*` is the only wildcard.

## Known limitations

- Some ref names are legal in Git but not valid Windows directory names. Those snapshots fail while the rest of the run completes, and `EXTRACTION.txt` records the failures.
- `--dedupe` needs symlink support. On Windows, enable Developer Mode or grant the symlink privilege.
- Two names that differ only in Unicode normalization collide on macOS. The later one is skipped and recorded.
- The issuer of an X.509 signature is not parsed. The signature type is still recorded.
- `--track` and `--scan-secrets` search extracted refs only.
- Reflog recovery needs the original repository because clone does not transfer reflogs.
- Submodule content is not captured. `COMMIT_INFO.txt` records the gitlink.
- Stashes are reported in `REFLOG.txt` but not extracted.

## Project layout

- `cmd`: command line parsing, help, execution, and the identities subcommand.
- `app`: run orchestration, planning, extraction, dry run, and reports.
- `git`: repository access, commit parsing, refs, signatures, tracking, and tree extraction.
- `extractor`: worker pool and per-commit snapshot writing.
- `snapshot`: output paths, record rendering, JSON, and shared file writing.
- `anomaly`, `identity`, `secrets`, and `console`: analysis helpers and terminal output.

Design decisions are in `docs/adr/`.

## Development

```bash
go test ./...
go test -race ./...
go vet ./...
```

## License

AGPL-3.0. See [LICENSE](LICENSE).
