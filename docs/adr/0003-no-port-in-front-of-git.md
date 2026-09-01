# No port in front of git

Code that shells out to git calls `exec` directly rather than through an
injected interface, and tests build real repositories in `t.TempDir()`. git is a
local-substitutable dependency: the stand-in for the real thing *is* the real
thing, cheaply, so there is nothing for a port to switch between.

This is recorded because it is the obvious suggestion to make about this
codebase and it keeps being the wrong one. Most packages are thinly tested, and
"most packages untested, therefore mock the subprocess" is a very short
inference to draw.

## Considered options

**A `GitRunner` interface with a real and a mock adapter.** The mock can only
assert on the argv it was handed, which couples every test to the exact shape of
the git command rather than to what the command achieves — testing past the
interface. Those tests then break on any refactor of the command while catching
none of the bugs that actually occurred here: a worker cap that was never
applied, a progress bar racing on `Clear`, two branch names colliding onto one
directory, and merge commits losing their change statistics because
`git log --numstat` and `git show --numstat` disagree about merges. Every one of
those was found by running real git and comparing real output. A mock would have
reproduced the bugs faithfully and reported success.

**Ports and adapters for the filesystem.** Rejected for the same reason: the
stand-in is `t.TempDir()`.

## Consequences

Tests in packages that touch git need `git` on PATH and cost tens of
milliseconds each rather than microseconds. That is the price of testing against
the thing the tool actually depends on, and at this suite's size it is not worth
optimising away.

Rendering is kept separate from I/O so it can be tested without either: the
snapshot module renders metadata to an `io.Writer`, and the console writes
through one. That is seam placement, not dependency inversion — no interface is
introduced, and nothing is mocked.
