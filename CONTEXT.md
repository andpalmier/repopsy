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
One commit's complete working tree written to its own directory, alongside that commit's
metadata and integrity record. The unit of output.
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

**Rewritten commit**:
A commit no ref reaches any more, recovered from a reflog that still remembers it. Its
presence is evidence that history was rewritten after it was made.
_Avoid_: dangling commit, orphan, lost commit

### Execution

**Worker**:
One concurrent extraction slot, counted **per branch**. Branches are exploded one at a time;
workers parallelise the commits within a single branch.
_Avoid_: thread, job, parallelism

### What is recorded

**Snapshot metadata**:
The forensic record of one commit, written inside its snapshot. Describes the commit, not the
extraction.
_Avoid_: commit info, header, sidecar

**Integrity record**:
The digest of every file in a snapshot, written inside it, so a reader can show the snapshot
has not been altered since extraction.
_Avoid_: checksums file, hashes, sums

**Root record**:
One of the files describing the run rather than any single commit, written at the root of an
exploded repository: the extraction manifest, the ref movement log, the tags, the identities,
and the repository state.
_Avoid_: summary, index, top-level file

**Extraction manifest**:
The root record of the run's own provenance: which build produced it, when, over what scope,
and what failed. Answers questions about the extraction; every other record answers questions
about the repository.
_Avoid_: metadata, log, receipt

**Repository state**:
The part of a repository that is not versioned and so appears in no commit — its local
configuration and its installed hooks.
_Avoid_: settings, environment, dotfiles

**Identity**:
One name and email pair appearing as an author or a committer. Two identities **collide** when
they share an email under different names, or a name under different emails.
_Avoid_: user, account, contributor

### Metadata

**Change statistics**:
The counts of files changed, insertions, and deletions in a commit's diff against its first
parent. A merge commit is measured the same way, against its first parent only, so changes
brought in from the other side of the merge are not counted twice.
_Avoid_: diffstat, numstat, commit size
