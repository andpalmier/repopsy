# A repository path normalizes to the repository top level

Whatever path is given on the command line is resolved to the top level of the repository that
contains it, so naming a subdirectory explodes the whole repository. Previously a subdirectory
was accepted as-is, which produced incoherent output: `git archive` inherited the subdirectory as
its tree root and wrote only that subtree's files, while the commit list and change statistics
still described whole commits — so every commit in the repository got a snapshot, including the
many that never touched the named subtree.

## Considered options

**Scope the whole explosion to the subtree** — filter the commit list to commits touching the
path and report statistics for that path only. This is a coherent and genuinely useful feature
(a subtree autopsy), and it is the alternative that will be proposed again. It was rejected here
only because it is new functionality; the incoherence above needed a decision either way, and
normalizing is the one that adds nothing.

**Reject subdirectories with an error**: honest, but needlessly hostile when the intent is
unambiguous.

## Consequences

Running `repopsy .` from anywhere inside a repository now behaves identically, and the resolved
top level is printed in the header so the promotion is visible rather than silent.
