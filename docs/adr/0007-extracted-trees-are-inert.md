# Extracted trees are inert, and the examined repository's configuration is not honoured

Files under `tree/` are written without executable bits, a symlink is kept only
if it resolves inside `tree/`, and a path that would leave `tree/`, make it a
git repository or collide on a case-insensitive filesystem is skipped or
renamed. Every git command repopsy runs pins the configuration that names a
program or decides whether a signature verifies.

The trees come from repositories under examination, which are often hostile by
design. Before this, `tree/start.sh` kept `-rwxr-xr-x`, a symlink to
`/etc/passwd` was written as one, and a tree entry named `.git` would have made
`tree/` a repository whose own `config` runs `core.fsmonitor` the moment an
examiner types `git status` there. Separately, working from a copy of the
original repository, as the README recommends for reflogs, handed its
`.git/config` to repopsy's own git commands: `gpg.program` ran on the first
signed commit, and `gpg.ssh.allowedSignersFile` let the repository vouch for its
own SSH signatures. Both were reproduced in tests before being fixed.

## Considered options

**Keep modes and symlinks, and document the risk.** Rejected: the record of
modes already exists in git and now in `COMMIT_INFO.txt`, so reproducing them on
disk adds convenience and nothing an examiner needs. `--preserve-modes` remains
for when it is wanted.

**Write every symlink as a plain file.** Simplest and safe, but loses the
in-tree links a reader browsing `tree/` expects. Keeping a link only when its
resolution, through the other kept links, stays inside is decided from the tree
listing alone; a lexical check of each target is not enough (`s -> .` with
`l -> s/../x`).

**Skip colliding and `.git` paths rather than rename them.** Safer still, but it
drops evidence from the snapshot. Renaming keeps the content, and the record
names both paths.

**Pin by refusing to read repository-local config at all.** git has no switch
for that short of `GIT_DIR` tricks that break other things. Passing `-c` for
the specific keys, with the examiner's own global or system value, preserves a
legitimate setup such as a custom `gpg.program` in `~/.gitconfig`.

## Consequences

Decisions about paths are made from the tree listing, so output is the same on
every host, with one exception: names that differ only in Unicode normalisation
collide on macOS alone, and there the later one is skipped and recorded.

`SHA256SUMS` no longer lists kept symlinks, since `sha256sum -c` follows them
and hashes their target, which never matched the recorded digest.
