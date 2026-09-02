# Branch output directories mirror ref paths

A branch's snapshots are written to a directory tree mirroring its ref path, so `feat/x`
explodes into `<out>/feat/x/` rather than a flattened name. Git already refuses to create
`refs/heads/main/x` while `refs/heads/main` exists, so nesting inherits git's own guarantee
that no two branches can claim the same directory — no escaping scheme has to be invented or
maintained to keep them apart.

## Considered options

**Flatten `/` to `_`** (what this replaces): silently merged `feat/x` and `feat_x` into a single
`feat_x/` directory, interleaving two branches' snapshots with no warning.

**Percent-encode or otherwise escape `/`**: correct, but it invents a private encoding that has
to be documented, kept reversible, and defended against the next special case. Nesting needs no
encoding at all.

**Detect collisions and append a discriminator**: keeps flat names readable, but only pays off
in the rare colliding case while adding collision-tracking state to every run.

## Consequences

Snapshot paths change for any branch containing `/`, which is a breaking change to the output
layout.

Ref names legal in git but illegal as Windows path components — reserved device names (`aux`,
`CON`) and the characters `<`, `>`, `"`, `|` — still produce unusable directories on Windows.
This is a pre-existing limitation, not one introduced here: the flattening it replaced
substituted only `/` and offered no Windows protection either. It is recorded as a known
limitation rather than fixed.
