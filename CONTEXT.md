# Repopsy

Repopsy performs a forensic autopsy on a git repository: it takes the history apart so each
commit's full working tree can be read, compared, and archived as an ordinary directory.

## Language

### The operation

**Explode**:
The whole-repository operation: taking a repository apart into one snapshot per commit. This is
the product-level verb — a repository is exploded.
_Avoid_: expand, dump

**Extract**:
The single-commit operation: writing one commit's tree into one snapshot. This is the
internal verb — a commit is extracted. Exploding a repository is many extractions.
_Avoid_: expand, checkout, export

**Snapshot**:
One commit's complete working tree written to its own directory, alongside the commit's
metadata. The unit of output.
_Avoid_: folder, output dir, dest path, commit dir

### What gets exploded

**Repository**:
A git repository, identified by its top level. Naming any directory inside a repository
names the repository itself, not the subtree.
_Avoid_: repo path, working dir

**Branch**:
A local branch — a ref under `refs/heads/`. Remote-tracking refs and tags are not branches,
so "all branches" means all local branches and may well be one on a fresh clone.
_Avoid_: ref, head

### Execution

**Worker**:
One concurrent extraction slot, counted **per branch**. Branches are exploded one at a time;
workers parallelise the commits within a single branch.
_Avoid_: thread, job, parallelism

### Metadata

**Change statistics**:
The counts of files changed, insertions, and deletions in a commit's diff against its first
parent. A merge commit is measured the same way, against its first parent only, so changes
brought in from the other side of the merge are not counted twice.
_Avoid_: diffstat, numstat, commit size
