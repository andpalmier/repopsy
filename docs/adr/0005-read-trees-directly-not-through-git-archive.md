# Trees are read directly, not through git archive

`ExtractCommit` reads a commit's tree with `git ls-tree -r -z` and streams blob
content from a single `git cat-file --batch`, writing files, symlinks and modes
itself. It no longer pipes `git archive` into `tar`.

The reason is not performance. `git archive` honours `export-ignore` from the
`.gitattributes` in the tree being archived, which means **a repository can
withhold files from its own snapshot**. The commit's recorded file list still
names them, so the metadata and the extracted tree silently disagree. For a
forensic tool that is the worst available failure: the subject of the
investigation decides what evidence gets collected.

It cannot be overridden. Explicit pathspecs still honour it,
`--worktree-attributes` still honours it, and git 2.55 has no
`--no-export-ignore`. Not using `git archive` is the only fix.

## Considered options

**Detect and report the discrepancy** — extract as before, then compare against
`git ls-tree` and record which files were withheld. Honest, and cheaper to
build, but it leaves the snapshot incomplete and asks the reader to notice a
warning. Recording that evidence is missing is worse than collecting it.

**Keep `git archive` and accept the gap**, documenting it as a limitation. This
is what the previous architecture review recommended on the weaker grounds of
retiring the `tar` dependency, rated "worth exploring". The hazard changes that
calculation.

## Consequences

`tar` is no longer a runtime dependency, declared or otherwise. Verified by
running with `tar` removed from `PATH`.

The tree listing is now available at extraction time, which is what lets gitlink
entries be recorded (`160000`) rather than silently omitted, and what would make
per-file checksums nearly free.

Requests to `cat-file` are written from a goroutine while responses are read,
because a round trip per blob cost more than the rest of the extraction put
together. Measured at parity with the old pipeline: 1.298s against 1.278s for 41
commits, with output byte-identical across 93 snapshots.

Modes, symlinks and gitlinks are now this code's responsibility rather than
tar's. A path that would escape the snapshot directory is refused; git rejects
such tree entries itself, but a forensic tool is pointed at repositories it does
not trust.
