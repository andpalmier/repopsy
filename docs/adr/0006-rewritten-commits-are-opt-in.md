# Rewritten commits are recovered only when asked for

`--include-rewritten` walks each branch's reflog tips and extracts commits the
branch no longer reaches — what a reset or force-push replaced. It is off by
default. Recovered snapshots go in the branch's own directory and their
`COMMIT_INFO.txt` states that no branch reaches them.

Walking only `refs/heads` misses exactly the history someone tried to erase, so
this matters. It is nonetheless opt-in: on most repositories the set is empty,
and a flag that silently changes the snapshot count between runs of the same
repository is worse than one the examiner turns on deliberately.

## Considered options

**On by default.** Defensible for a forensic tool, where capturing everything is
the instinct. Rejected because reflog contents expire (`gc.reflogExpire`, 90 days
by default) and differ between two examiners looking at the same repository, so a
default-on recovery makes output depend on the machine's housekeeping.

**A separate output directory for recovered commits.** Rejected because
`git reflog show <branch>` attributes tips to the branch that held them, so the
attribution exists and discarding it would lose information.

## Consequences

Reflogs are local and are **not** transferred by clone. A bare mirror or a fresh
clone has nothing to recover however much history was rewritten upstream, so this
is evidence only when examining the original working repository. `REFLOG.txt`
says so in its own header, because an empty log otherwise reads as proof that
nothing was rewritten.

Recovered commits are extracted from the same reflog tips that `REFLOG.txt`
records, so the two corroborate each other.
