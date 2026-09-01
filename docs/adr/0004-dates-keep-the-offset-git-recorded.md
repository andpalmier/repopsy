# Dates keep the offset git recorded

Commit dates are read with `%aI`/`%cI` and rendered with the UTC offset git
stored, and snapshot directory names are timestamped in that same offset. The
epoch forms `%at`/`%ct` are not used for display, because rendering them
substitutes the timezone of whatever machine runs repopsy.

This was a defect, not a preference. The same commit rendered as
`2026-09-01T07:15:05Z`, `…T16:15:05+09:00` or `…T00:15:05-07:00` depending on
the examiner's laptop, and snapshot directories were renamed to match — so two
investigators exploding one repository produced folder trees that could not be
diffed and whose hashes did not agree.

## Considered options

**Render in the analyst's local time** (the previous behaviour): destroys the
recorded offset, which is evidence in its own right — it indicates the author's
locale and working hours — and replaces it with a fact about the examiner.

**Normalise everything to UTC**: reproducible, and a reasonable choice. Rejected
because it still discards the recorded offset, and the offset is exactly the kind
of detail a forensic record exists to preserve. UTC can always be derived from
the offset; the offset cannot be derived from UTC.

## Consequences

Snapshot directory names change for any repository whose commits were not made
at the examiner's offset. This is a breaking change to the output layout.

Dates in `COMMIT_INFO.txt` may appear "wrong" to a reader expecting local time.
They are the author's local time, which is the point. The Unix timestamp is
printed alongside and is offset-independent.

`EXTRACTION.txt` deliberately does the opposite: its `Started` and `Finished`
times are the examiner's own wall clock, because they record when the extraction
happened, not anything about the repository.
