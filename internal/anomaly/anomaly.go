// Package anomaly finds integrity and provenance problems in a history: dates
// that cannot be true, roots that should not be there, commits too large to be
// ordinary work, and identities that are not real.
//
// Each finding is one line with a stable kind and a short reason. The checks
// need the whole extracted history, since most of them compare a commit with
// its neighbours or a ref with the others.
//
// Deliberately out of scope: anything that profiles the people behind the
// identities, such as inferring location from timezone offsets.
package anomaly

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/identity"
)

// Kinds of finding. They are stable identifiers, safe to grep for.
const (
	AuthorAfterCommitter = "author-after-committer"
	DateSkew             = "date-skew"
	DatedAfterChild      = "dated-after-child"
	FirstParentRegress   = "first-parent-date-regression"
	MultipleRoots        = "multiple-roots"
	Rerooted             = "rerooted-history"
	LargeCommit          = "large-commit"
	PlaceholderIdentity  = "placeholder-identity"
	UnsignedWebFlow      = "unsigned-web-flow"
)

// Thresholds tune the checks that need one.
type Thresholds struct {
	// LargeFiles and LargeLines flag a commit touching at least this many
	// files, or inserting at least this many lines.
	LargeFiles int
	LargeLines int

	// DateSkew flags a committer date this far after the author date, and an
	// author date this far behind its first parent's. It is also how much
	// younger a ref's history must be than another's to count as re-rooted.
	DateSkew time.Duration
}

// DefaultThresholds are tuned to stay quiet on ordinary histories: a rebase
// moves committer dates by days, rarely by a month, and ordinary commits touch
// tens of files, not hundreds.
var DefaultThresholds = Thresholds{LargeFiles: 200, LargeLines: 20000, DateSkew: 30 * 24 * time.Hour}

// Ref is one extracted ref and the commits it reaches.
type Ref struct {
	Name   string
	Hashes []string
}

// Result is every finding.
type Result struct {
	// ByCommit lists each commit's findings, in a fixed order.
	ByCommit map[string][]git.Anomaly

	// Repository holds findings about the history as a whole.
	Repository []string
}

// Count tallies, for each kind, how many commits have at least one finding of
// it, most frequent first.
func (r Result) Count() []KindCount {
	n := map[string]int{}
	for _, list := range r.ByCommit {
		kinds := map[string]bool{}
		for _, a := range list {
			kinds[a.Kind] = true
		}
		for k := range kinds {
			n[k]++
		}
	}
	counts := make([]KindCount, 0, len(n))
	for k, v := range n {
		counts = append(counts, KindCount{Kind: k, Commits: v})
	}
	sort.Slice(counts, func(i, j int) bool {
		if counts[i].Commits != counts[j].Commits {
			return counts[i].Commits > counts[j].Commits
		}
		return counts[i].Kind < counts[j].Kind
	})
	return counts
}

// KindCount is how many findings of one kind there are.
type KindCount struct {
	Kind    string
	Commits int
}

// Detect runs every check over commits, which must be the distinct commits
// extracted, and refs, which say which ref reaches which of them.
func Detect(commits []git.Commit, refs []Ref, shallow bool, t Thresholds) Result {
	res := Result{ByCommit: map[string][]git.Anomaly{}}
	add := func(hash, kind, format string, args ...any) {
		res.ByCommit[hash] = append(res.ByCommit[hash], git.Anomaly{Kind: kind, Detail: fmt.Sprintf(format, args...)})
	}

	byHash := make(map[string]*git.Commit, len(commits))
	for i := range commits {
		byHash[commits[i].Hash] = &commits[i]
	}

	var roots []*git.Commit
	for i := range commits {
		c := &commits[i]
		if len(c.ParentHashes) == 0 && !c.ShallowBoundary {
			roots = append(roots, c)
		}

		if c.Backdated() {
			add(c.Hash, AuthorAfterCommitter, "authored %s after it was committed", durationText(c.AuthorDate.Sub(c.CommitDate)))
		} else if d := c.CommitDate.Sub(c.AuthorDate); t.DateSkew > 0 && d > t.DateSkew {
			add(c.Hash, DateSkew, "committed %s after it was authored", durationText(d))
		}

		for i, ph := range c.ParentHashes {
			p, ok := byHash[ph]
			if !ok {
				continue
			}
			if d := p.CommitDate.Sub(c.CommitDate); d > 0 {
				add(p.Hash, DatedAfterChild, "committed %s after its child %s", durationText(d), c.ShortHash)
			}
			if d := p.AuthorDate.Sub(c.AuthorDate); i == 0 && t.DateSkew > 0 && d > t.DateSkew {
				add(c.Hash, FirstParentRegress, "authored %s before its first parent %s", durationText(d), p.ShortHash)
			}
		}

		root := len(c.ParentHashes) == 0 && !c.ShallowBoundary
		if (t.LargeFiles > 0 && c.FilesChanged >= t.LargeFiles) || (t.LargeLines > 0 && c.Insertions >= t.LargeLines) {
			detail := fmt.Sprintf("%d files changed, +%d -%d lines", c.FilesChanged, c.Insertions, c.Deletions)
			switch {
			case len(commits) == 1:
				detail += "; the only commit, as an import of history squashed elsewhere looks"
			case root:
				detail += "; a root commit, as an import of history squashed elsewhere looks"
			}
			add(c.Hash, LargeCommit, "%s", detail)
		}

		for _, role := range []struct{ label, name, email string }{
			{"author", c.Author, c.AuthorEmail},
			{"committer", c.Committer, c.CommitterEmail},
		} {
			if why := identity.Placeholder(role.name, role.email); why != "" {
				add(c.Hash, PlaceholderIdentity, "%s %s <%s>: %s", role.label, role.name, role.email, why)
			}
		}
		if identity.IsWebFlow(c.Committer, c.CommitterEmail) && !c.Signature.Signed() {
			add(c.Hash, UnsignedWebFlow, "claims GitHub as committer but is unsigned; GitHub signs the commits it creates")
		}
	}

	if len(roots) > 1 {
		sort.Slice(roots, func(i, j int) bool { return roots[i].AuthorDate.Before(roots[j].AuthorDate) })
		var names []string
		for _, r := range roots {
			names = append(names, fmt.Sprintf("%s (%s)", r.ShortHash, r.AuthorDate.Format(time.DateOnly)))
			add(r.Hash, MultipleRoots, "one of %d root commits in the extracted history", len(roots))
		}
		res.Repository = append(res.Repository, fmt.Sprintf("%s: %d root commits: %s", MultipleRoots, len(roots), strings.Join(names, ", ")))
	}

	if shallow {
		res.Repository = append(res.Repository, Rerooted+": not checked, because a shallow clone hides the real roots")
	} else {
		rerooted(refs, byHash, t, &res, add)
	}

	for h := range res.ByCommit {
		sort.SliceStable(res.ByCommit[h], func(i, j int) bool { return res.ByCommit[h][i].Kind < res.ByCommit[h][j].Kind })
	}
	return res
}

// rerooted flags refs whose history starts much later than that of another ref
// they share no root with: what a history rebuilt from a squashed snapshot
// looks like next to refs that kept the original, such as pull request refs.
func rerooted(refs []Ref, byHash map[string]*git.Commit, t Thresholds, res *Result, add func(hash, kind, format string, args ...any)) {
	if t.DateSkew <= 0 {
		return
	}
	type history struct {
		roots  map[string]bool
		oldest time.Time
	}
	spans := make([]history, len(refs))
	for i, ref := range refs {
		s := history{roots: map[string]bool{}}
		for _, h := range ref.Hashes {
			c, ok := byHash[h]
			if !ok {
				continue
			}
			if len(c.ParentHashes) == 0 {
				s.roots[h] = true
			}
			if s.oldest.IsZero() || c.AuthorDate.Before(s.oldest) {
				s.oldest = c.AuthorDate
			}
		}
		spans[i] = s
	}

	for i, ref := range refs {
		var older []int
		for j := range refs {
			if i == j || spans[i].oldest.IsZero() || spans[j].oldest.IsZero() {
				continue
			}
			if spans[i].oldest.Sub(spans[j].oldest) > t.DateSkew && disjoint(spans[i].roots, spans[j].roots) {
				older = append(older, j)
			}
		}
		if len(older) == 0 {
			continue
		}
		oldest := older[0]
		for _, j := range older {
			if spans[j].oldest.Before(spans[oldest].oldest) {
				oldest = j
			}
		}
		line := fmt.Sprintf("%s starts %s, %s after %s (%s), and shares no root with it or %d other ref(s)",
			ref.Name, spans[i].oldest.Format(time.DateOnly), durationText(spans[i].oldest.Sub(spans[oldest].oldest)),
			refs[oldest].Name, spans[oldest].oldest.Format(time.DateOnly), len(older)-1)
		res.Repository = append(res.Repository, Rerooted+": "+line)
		for root := range spans[i].roots {
			add(root, Rerooted, "root of %s, whose history starts %s after that of %s, with no common root",
				ref.Name, durationText(spans[i].oldest.Sub(spans[oldest].oldest)), refs[oldest].Name)
		}
	}
}

func disjoint(a, b map[string]bool) bool {
	for k := range a {
		if b[k] {
			return false
		}
	}
	return true
}

// durationText renders a duration in the largest sensible unit.
func durationText(d time.Duration) string {
	switch {
	case d >= 48*time.Hour:
		return fmt.Sprintf("%d days", int(d.Hours()/24))
	case d >= 2*time.Hour:
		return fmt.Sprintf("%d hours", int(d.Hours()))
	case d >= 2*time.Minute:
		return fmt.Sprintf("%d minutes", int(d.Minutes()))
	default:
		return fmt.Sprintf("%d seconds", int(d.Seconds()))
	}
}
