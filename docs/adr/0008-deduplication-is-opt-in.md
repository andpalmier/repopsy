# Storing each commit once is opt-in

`--dedupe` writes each distinct commit once under `commits/` and makes each
ref's entry a relative symlink to it. Without it, a commit is extracted under
every ref that reaches it, as before.

A GitHub mirror with its pull request refs makes the default expensive: one
investigation produced 793 and 781 snapshots for two refs sharing almost all of
their history, 19 GB of mostly duplicate trees. Deduplicating is clearly right
for that case.

It is not the default because it changes what the output is made of. `find`
without `-L`, `rsync` without `-L`, several archivers and Windows without the
symlink privilege see links where directories used to be. A layout that some
tools silently treat as empty is a worse failure for evidence than one that is
large.

## Considered options

**Default on, with `--no-dedupe`.** Rejected for the reason above: the existing
layout is what scripts written against repopsy expect.

**A ref-to-commit index file instead of symlinks.** Portable, but every path a
reader knows (`refs/main/<snapshot>/tree/`) would stop resolving.

**Hard links.** Directories cannot be hard-linked, and hard-linking every file
multiplies inodes without the clarity of one link per snapshot.

## Consequences

Refs are now walked with `git rev-list` and each distinct commit's metadata is
fetched once, in both layouts, so hundreds of refs that share history no longer
mean parsing that history hundreds of times. `EXTRACTION.txt` reports the
distinct commit count beside the per-ref counts either way.

Because every selected ref is walked before any reflog is recovered from, a
commit one branch was reset away from but another branch still reaches is no
longer reported as rewritten history.
