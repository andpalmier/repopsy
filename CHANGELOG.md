# Changelog

Versions follow [semantic versioning](https://semver.org) and are taken from git
tags at release time. Earlier releases are described on the
[Releases](https://github.com/andpalmier/repopsy/releases) page.

## 2.2.0 (2026-11-08)

### Safety

- Files under `tree/` are no longer executable. The git mode of every executable
  and symlink is recorded in a new `TREE` section of `COMMIT_INFO.txt`.
  `--preserve-modes` restores the old behaviour.
- A symlink is kept only if it resolves inside `tree/`, through any other kept
  symlink. One that would lead outside is written as a plain file holding its
  target, and recorded.
- Tree paths with `..` or empty components are skipped, `.git` components (in
  every spelling HFS+ and NTFS treat as `.git`) are renamed, and paths that
  collide on case-insensitive filesystems are renamed identically on every
  host. Files are created with `O_EXCL`, so nothing is ever overwritten. Every
  skipped or renamed path is recorded in `COMMIT_INFO.txt` and `EXTRACTION.txt`.
- Config and hook files that are symbolic links are recorded by target and not
  read, so an untrusted repository cannot copy an unrelated local file into
  `REPOSITORY.txt`. Hooks are read from git's configured hooks path; a hooks
  path outside the repository is recorded but not read.
- Tree path components containing control characters are renamed and recorded,
  so a newline in a repository file name cannot break `SHA256SUMS`.
- The examined repository's own configuration can no longer make repopsy's git
  commands run a program (`gpg.program` and the other signing programs,
  `core.fsmonitor`, textconv and external diff drivers) or vouch for its own SSH
  signatures (`gpg.ssh.allowedSignersFile`). This mattered when working from a
  copy of the original repository.

### Fixed

- A shallow clone is detected and reported: a terminal warning, `Shallow: YES`
  and the boundary commits in `EXTRACTION.txt`, and boundary commits marked
  "parents not present (shallow boundary)" instead of "root commit".
- An SSH-signed commit was recorded as "Not signed" when no allowed signers file
  was configured, because that is what git reports. It is now "SSH signature
  present, not verified (no allowed signers file)".
- `--include-rewritten` could report a commit as unreachable ("no branch
  reaches this commit") when a branch processed later did reach it.
- `IDENTITIES.txt` counted a commit once per branch reaching it; it now counts
  each commit once, as its header says.
- `SHA256SUMS` listed symlinks, which `sha256sum -c` follows, so those lines
  never verified. Symlinks kept as symlinks are now left out; their targets are
  in `COMMIT_INFO.txt`.
- A branch whose name another ref shares, such as a tag `main`, was written
  under the name git disambiguates it to, e.g. `refs/heads/main/` or
  `refs/refs/heads/main/` rather than `refs/main/`, so its directory depended
  on unrelated refs. Branches now always go to `refs/<name>/`.
- A repository-local `log.showSignature` can no longer corrupt the commit
  listing.
- Commit messages containing the old record separator are no longer truncated;
  commit listing now uses NUL-delimited records.
- File names containing non-ASCII bytes or control characters are recorded
  exactly, and `repopsy identities` reports distinct commits rather than
  adding author and committer appearances.
- Failed snapshot links, root-record writes, and interrupted extraction now
  return an error instead of exiting successfully with partial output.
- `--date-skew 0` now turns off the re-rooted-history check as documented.

### Added

- Signature details known without the key: type (GPG, SSH, X.509), issuing key
  ID or fingerprint, algorithm, any signer identity the signature states, and a
  plain `Verified:` line. `--allowed-signers FILE` verifies SSH signatures.
  `EXTRACTION.txt` gains a signature summary by type, verdict, committer and
  issuing key. `TAGS.txt` records the signature type of signed tags.
- `--refs PATTERN` (repeatable) and `--all-refs` choose which refs to extract.
  Refs outside `refs/heads/` go under `other-refs/`. `EXTRACTION.txt` lists
  every ref namespace and how much of it was extracted, and the terminal points
  out unextracted pull request refs.
- `--dedupe` stores each commit once under `commits/`, with ref directories
  linking to it. Refs are now walked with `git rev-list`, and each distinct
  commit is parsed once whatever the layout. `EXTRACTION.txt` reports the
  number of distinct commits.
- `--no-tree`, `--paths PATTERN` and `--dry-run` for large repositories.
- `--json` writes a JSON twin of every record, with a documented, versioned
  schema.
- Anomaly checks, one line each in `COMMIT_INFO.txt` and summarised in
  `EXTRACTION.txt`: date skew, commits dated after their children, first-parent
  date regressions, multiple roots, re-rooted histories, large commits,
  placeholder identities, and unsigned commits claiming GitHub as committer.
  Thresholds are set with `--large-commit-files`, `--large-commit-lines` and
  `--date-skew`. `IDENTITIES.txt` lists placeholder identities and GitHub
  noreply conflicts.
- `--track STRING` writes `STRINGS.txt`: where a string first appears, every
  commit changing its count, and its count at each ref tip.
- `repopsy identities DIR...` reports identities shared between exploded
  repositories, by email and by GitHub account.
- `--scan-secrets` writes `SECRETS.txt`: credential-shaped strings the history
  adds, masked, with a short hash, where they still live and which refs reach
  them. Values are never printed or stored.

### Changed

- New lines and sections in existing records are additions; no existing field
  changes meaning. `COMMIT_INFO.txt` gains `Signature Type`, `Issuer`,
  `Claimed Signer` and `Verified` in its verification block, and `TREE` and
  `ANOMALIES` sections when they apply. `EXTRACTION.txt` gains `Shallow`,
  `Tree` and `Layout` lines, a `Unique commits` count, and sections for the
  shallow boundary, ref namespaces, signatures, anomalies and path issues;
  its per-ref section is headed `REFS` instead of `BRANCHES` when `--refs` or
  `--all-refs` is used. `IDENTITIES.txt` gains two sections.
- Terminal output now lists every ref before extraction starts.
