package git

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"time"
)

// StringHistory is the history of one tracked string across a set of refs.
type StringHistory struct {
	String string

	// Changes lists every commit that changes how often the string occurs,
	// oldest first: git log -S semantics, with merges compared against their
	// first parent.
	Changes []StringChange

	// Tips is the count at each ref's tip, in the order the refs were given.
	Tips []StringTip
}

// StringChange is one commit that changes the string's occurrence count.
type StringChange struct {
	Hash      string
	ShortHash string
	Date      time.Time // author date, with the offset git recorded
	Author    string
	Email     string
	Subject   string

	// Paths lists each file whose count changed, with before and after.
	Paths []StringCount
}

// Delta is the change in total occurrences the commit makes.
func (c StringChange) Delta() int {
	var d int
	for _, p := range c.Paths {
		d += p.After - p.Before
	}
	return d
}

// StringCount is a path's occurrence count before and after a change, or at a
// ref tip, where only After is set.
type StringCount struct {
	Path   string
	Before int
	After  int
}

// StringTip is the string's count at one ref's tip.
type StringTip struct {
	Ref   string
	Total int
	Paths []StringCount
}

// First returns the first commit that adds an occurrence, if any does.
func (h StringHistory) First() (StringChange, bool) {
	for _, c := range h.Changes {
		if c.Delta() > 0 {
			return c, true
		}
	}
	return StringChange{}, false
}

// TrackRef is a ref whose history and tip a string is tracked through.
type TrackRef struct {
	Label string
	Rev   string
}

const trackFormat = "%x1e%H%x00%aI%x00%an%x00%ae%x00%s%x00"

// TrackString records every commit reachable from refs that changes how often
// s occurs, and how often it occurs at each ref's tip.
//
// The string is matched literally. Nothing is passed to git as a pattern, so
// regular expression syntax in s means itself.
func (r *Repository) TrackString(ctx context.Context, s string, refs []TrackRef) (StringHistory, error) {
	h := StringHistory{String: s}
	if s == "" || strings.Contains(s, "\n") {
		return h, errors.New("a tracked string must be a single non-empty line")
	}

	revs := make([]string, len(refs))
	for i, ref := range refs {
		revs[i] = ref.Rev
	}
	if len(revs) > 0 {
		changes, err := r.stringChanges(ctx, s, revs)
		if err != nil {
			return h, err
		}
		h.Changes = changes
	}

	tipCounts := map[string]StringTip{}
	for _, ref := range refs {
		tip, err := r.output(ctx, "rev-parse", "--verify", "--end-of-options", ref.Rev+"^{commit}")
		if err != nil {
			return h, fmt.Errorf("failed to resolve %s: %w", ref.Label, err)
		}
		counted, ok := tipCounts[tip]
		if !ok {
			if counted, err = r.countAtTip(ctx, s, tip); err != nil {
				return h, err
			}
			tipCounts[tip] = counted
		}
		counted.Ref = ref.Label
		h.Tips = append(h.Tips, counted)
	}
	return h, nil
}

// stringChanges lists the commits git log -S finds, with the per-path counts
// on either side of each.
func (r *Repository) stringChanges(ctx context.Context, s string, revs []string) ([]StringChange, error) {
	cmd := r.command(ctx, "-c", "core.quotePath=false", "log", "-S"+s, "--format="+trackFormat, "--raw", "--no-abbrev",
		"--diff-merges=first-parent", "--no-textconv", "--no-ext-diff", "--no-show-signature", "--reverse", "--stdin")
	cmd.Stdin = strings.NewReader(strings.Join(revs, "\n") + "\n")
	out, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return nil, fmt.Errorf("git log -S failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git log -S failed: %w", err)
	}

	var changes []StringChange
	var blobs []string
	for record := range strings.SplitSeq(string(out), recordSep) {
		parts := strings.SplitN(record, fieldSep, 6)
		if len(parts) < 6 {
			continue
		}
		date, _ := parseGitDate(parts[1])
		c := StringChange{Hash: parts[0], ShortHash: shortHash(parts[0]), Date: date,
			Author: parts[2], Email: parts[3], Subject: parts[4]}
		for line := range strings.SplitSeq(parts[5], "\n") {
			// Without --pickaxe-all, git shows only the files whose count
			// changed, which are exactly the paths involved.
			if f, ok := parseRawLine(line); ok && strings.HasPrefix(line, ":") {
				c.Paths = append(c.Paths, StringCount{Path: f.Path})
				blobs = append(blobs, f.OldBlob, f.NewBlob)
			}
		}
		changes = append(changes, c)
	}

	// Count both sides of every change. An absent side (an added or deleted
	// file) is all zeros and counts as none.
	counts := map[string]int{}
	var fetch []string
	for _, b := range blobs {
		if _, done := counts[b]; done {
			continue
		}
		counts[b] = 0
		if !isNullHash(b) {
			fetch = append(fetch, b)
		}
	}
	needle := []byte(s)
	if err := r.catFileBatch(ctx, fetch, func(i int, content []byte) error {
		counts[fetch[i]] = bytes.Count(content, needle)
		return nil
	}); err != nil {
		return nil, err
	}

	i := 0
	for ci := range changes {
		for pi := range changes[ci].Paths {
			changes[ci].Paths[pi].Before = counts[blobs[i]]
			changes[ci].Paths[pi].After = counts[blobs[i+1]]
			i += 2
		}
	}

	// --reverse orders by the walk, not by date; across several refs that is
	// not chronological.
	sort.SliceStable(changes, func(a, b int) bool { return changes[a].Date.Before(changes[b].Date) })
	return changes, nil
}

// countAtTip counts occurrences of s in every file of a commit's tree.
func (r *Repository) countAtTip(ctx context.Context, s, tip string) (StringTip, error) {
	// -o prints every match as "<tip>:<path>\0<match>\n". -a searches binary
	// files too, as git log -S does. Line numbers and columns are switched off
	// explicitly, since configuration could otherwise add them.
	cmd := r.command(ctx, "grep", "-F", "-a", "-o", "-z", "--full-name", "--no-line-number", "--no-column",
		"-e", s, tip, "--")
	out, err := cmd.Output()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 && len(exitErr.Stderr) == 0 {
		return StringTip{}, nil // no match
	}
	if err != nil {
		return StringTip{}, fmt.Errorf("git grep failed: %w", err)
	}

	perPath := map[string]int{}
	var order []string
	prefix := tip + ":"
	for rest := string(out); rest != ""; {
		if !strings.HasPrefix(rest, prefix) {
			return StringTip{}, fmt.Errorf("unexpected git grep output")
		}
		rest = rest[len(prefix):]
		path, after, found := strings.Cut(rest, "\x00")
		if !found || len(after) < len(s)+1 {
			return StringTip{}, fmt.Errorf("unexpected git grep output")
		}
		rest = after[len(s)+1:]
		if perPath[path] == 0 {
			order = append(order, path)
		}
		perPath[path]++
	}

	var tipCount StringTip
	for _, p := range order {
		tipCount.Paths = append(tipCount.Paths, StringCount{Path: p, After: perPath[p]})
		tipCount.Total += perPath[p]
	}
	return tipCount, nil
}

// isNullHash reports whether a hash names no object.
func isNullHash(h string) bool {
	return strings.Trim(h, "0") == ""
}
