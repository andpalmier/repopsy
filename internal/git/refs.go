package git

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// ReflogEntry is one movement of one ref, as recorded in the local reflog.
//
// The reflog is the only record of history that was rewritten away: a reset or
// force-push leaves the replaced commits unreachable but still logged here.
type ReflogEntry struct {
	Ref    string    // the ref that moved, e.g. refs/heads/main
	Commit string    // the value the ref moved to
	At     time.Time // when it moved
	Actor  string
	Email  string
	Action string // git's own description, e.g. "reset: moving to HEAD~2"
}

// reflogFormat asks for the selector, which carries both the ref and the date
// when --date is given, plus the identity and action.
const reflogFormat = "%gD%x00%H%x00%gn%x00%ge%x00%gs"

// Reflog returns every reflog entry for every ref, newest first per ref.
//
// Reflogs are local to a repository and are not transferred by clone, so this
// is empty for a bare repository or a fresh clone. It is evidence only when
// examining the original working repository.
func (r *Repository) Reflog(ctx context.Context) ([]ReflogEntry, error) {
	output, err := gitOutput(ctx, r.Path, "reflog", "--all", "--date=iso-strict", "--format="+reflogFormat)
	if err != nil {
		return nil, fmt.Errorf("failed to read the reflog: %w", err)
	}
	if output == "" {
		return nil, nil
	}

	var entries []ReflogEntry
	for line := range strings.SplitSeq(output, "\n") {
		if entry, ok := parseReflogLine(line); ok {
			entries = append(entries, entry)
		}
	}
	return entries, nil
}

// parseReflogLine reads one reflog record. With --date, git renders the
// selector as "<ref>@{<date>}"; ref names may not contain "@{", so splitting on
// the last occurrence is unambiguous.
func parseReflogLine(line string) (ReflogEntry, bool) {
	fields := strings.Split(line, fieldSep)
	if len(fields) < 5 {
		return ReflogEntry{}, false
	}

	entry := ReflogEntry{
		Commit: fields[1],
		Actor:  fields[2],
		Email:  fields[3],
		Action: fields[4],
	}

	selector := fields[0]
	if i := strings.LastIndex(selector, "@{"); i >= 0 {
		entry.Ref = selector[:i]
		if at, err := time.Parse(time.RFC3339, strings.TrimSuffix(selector[i+2:], "}")); err == nil {
			entry.At = at
		}
	} else {
		entry.Ref = selector
	}
	return entry, true
}

// Tag is a tag ref. An annotated tag carries its own tagger identity, date and
// signature — an attestation independent of the commit it points at, and often
// the strongest claim a repository makes about who published what.
type Tag struct {
	Name string

	// Target is the commit the tag ultimately refers to.
	Target string

	// Annotated distinguishes a tag object from a lightweight tag, which is
	// just a ref and carries no metadata of its own.
	Annotated bool

	// Object is the tag object's own hash, set only when Annotated.
	Object string

	Tagger      string
	TaggerEmail string
	TaggerDate  time.Time
	Subject     string

	// Signed reports whether the tag object carries a signature.
	Signed bool
}

const tagFormat = "%(refname:short)%00%(objecttype)%00%(objectname)%00%(*objectname)%00" +
	"%(taggername)%00%(taggeremail)%00%(taggerdate:iso-strict)%00%(contents:subject)%00%(contents:signature)"

// Tags returns every tag in the repository, sorted by name.
func (r *Repository) Tags(ctx context.Context) ([]Tag, error) {
	output, err := gitOutput(ctx, r.Path, "for-each-ref", "refs/tags", "--format="+tagFormat)
	if err != nil {
		return nil, fmt.Errorf("failed to list tags: %w", err)
	}
	if output == "" {
		return nil, nil
	}

	var tags []Tag
	for line := range strings.SplitSeq(output, "\n") {
		if tag, ok := parseTagLine(line); ok {
			tags = append(tags, tag)
		}
	}
	return tags, nil
}

func parseTagLine(line string) (Tag, bool) {
	f := strings.Split(line, fieldSep)
	if len(f) < 9 {
		return Tag{}, false
	}

	tag := Tag{
		Name:        f[0],
		Annotated:   f[1] == "tag",
		Tagger:      f[4],
		TaggerEmail: strings.Trim(f[5], "<>"),
		Subject:     f[7],
		Signed:      f[8] != "",
	}

	// A lightweight tag points straight at the commit; an annotated tag points
	// at a tag object which dereferences to it.
	if tag.Annotated {
		tag.Object, tag.Target = f[2], f[3]
	} else {
		tag.Target = f[2]
	}

	if date, err := time.Parse(time.RFC3339, f[6]); err == nil {
		tag.TaggerDate = date
	}
	return tag, true
}

// ReflogTips returns the distinct commits a branch's ref has ever pointed at,
// newest first. Walking history from these recovers commits that a reset or
// force-push made unreachable.
func (r *Repository) ReflogTips(ctx context.Context, branch string) ([]string, error) {
	output, err := gitOutput(ctx, r.Path, "reflog", "show", "--format=%H", branch)
	if err != nil {
		// A branch with no reflog is normal, not an error.
		return nil, nil
	}
	if output == "" {
		return nil, nil
	}

	seen := make(map[string]bool)
	var tips []string
	for tip := range strings.SplitSeq(output, "\n") {
		if tip != "" && !seen[tip] {
			seen[tip] = true
			tips = append(tips, tip)
		}
	}
	return tips, nil
}
