package anomaly

import (
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

var base = time.Date(2025, 1, 10, 12, 0, 0, 0, time.UTC)

func commit(hash string, days int, parents ...string) git.Commit {
	at := base.Add(time.Duration(days) * 24 * time.Hour)
	return git.Commit{
		Hash: hash, ShortHash: hash, ParentHashes: parents,
		Author: "Alice", AuthorEmail: "alice@company.io", AuthorDate: at,
		Committer: "Alice", CommitterEmail: "alice@company.io", CommitDate: at,
		FilesChanged: 1, Insertions: 1,
	}
}

func kinds(res Result, hash string) string {
	var k []string
	for _, a := range res.ByCommit[hash] {
		k = append(k, a.Kind)
	}
	return strings.Join(k, ",")
}

func TestCleanHistoryHasNoFindings(t *testing.T) {
	commits := []git.Commit{commit("a", 0), commit("b", 1, "a"), commit("c", 2, "b")}
	res := Detect(commits, []Ref{{Name: "refs/heads/main", Hashes: []string{"a", "b", "c"}}}, false, DefaultThresholds)
	if len(res.ByCommit) != 0 || len(res.Repository) != 0 {
		t.Errorf("findings on a clean history: %+v", res)
	}
}

func TestDates(t *testing.T) {
	skewed := commit("skew", 10, "a")
	skewed.AuthorDate = base.Add(-60 * 24 * time.Hour)
	back := commit("back", 3, "a")
	back.AuthorDate = back.CommitDate.Add(time.Hour)
	future := commit("future", 5) // committed after its child below
	child := commit("child", 4, "future")
	regress := commit("regress", 40, "late")
	late := commit("late", 39, "a")
	late.AuthorDate = base.Add(39 * 24 * time.Hour)
	regress.AuthorDate = base

	res := Detect([]git.Commit{commit("a", 0), skewed, back, future, child, late, regress}, nil, false, DefaultThresholds)
	for hash, want := range map[string]string{
		"skew":    DateSkew,
		"back":    AuthorAfterCommitter,
		"future":  DatedAfterChild,
		"regress": FirstParentRegress,
		"child":   "",
	} {
		if got := kinds(res, hash); !strings.Contains(got, want) || (want == "" && got != "") {
			t.Errorf("%s: kinds %q, want %q", hash, got, want)
		}
	}
}

func TestRootsAndRerooting(t *testing.T) {
	// Pull request history starting in January 2025, and a main branch that
	// starts over from a fresh root fourteen months later.
	commits := []git.Commit{
		commit("pr1", 0), commit("pr2", 10, "pr1"),
		commit("m1", 420), commit("m2", 421, "m1"),
	}
	refs := []Ref{
		{Name: "refs/pull/1/head", Hashes: []string{"pr1", "pr2"}},
		{Name: "refs/heads/main", Hashes: []string{"m1", "m2"}},
	}
	res := Detect(commits, refs, false, DefaultThresholds)
	if !strings.Contains(kinds(res, "m1"), Rerooted) || !strings.Contains(kinds(res, "m1"), MultipleRoots) {
		t.Errorf("m1 kinds = %q", kinds(res, "m1"))
	}
	if strings.Contains(kinds(res, "pr1"), Rerooted) {
		t.Error("the older history was flagged as re-rooted")
	}
	joined := strings.Join(res.Repository, "\n")
	if !strings.Contains(joined, "2 root commits") || !strings.Contains(joined, "refs/heads/main starts 2026-03-06, 420 days after refs/pull/1/head") {
		t.Errorf("repository findings:\n%s", joined)
	}

	// A shallow boundary is not a root, and a shallow clone cannot show
	// re-rooting at all.
	boundary := commit("m1", 420)
	boundary.ShallowBoundary = true
	res = Detect([]git.Commit{commits[0], commits[1], boundary, commits[3]}, refs, true, DefaultThresholds)
	if strings.Contains(kinds(res, "m1"), MultipleRoots) || strings.Contains(kinds(res, "m1"), Rerooted) {
		t.Errorf("shallow boundary flagged: %q", kinds(res, "m1"))
	}
}

func TestRerootingOffWhenDateSkewIsOff(t *testing.T) {
	commits := []git.Commit{
		commit("pr1", 0), commit("pr2", 10, "pr1"),
		commit("m1", 420), commit("m2", 421, "m1"),
	}
	refs := []Ref{
		{Name: "refs/pull/1/head", Hashes: []string{"pr1", "pr2"}},
		{Name: "refs/heads/main", Hashes: []string{"m1", "m2"}},
	}
	res := Detect(commits, refs, false, Thresholds{LargeFiles: 200, LargeLines: 20000, DateSkew: 0})
	if strings.Contains(kinds(res, "m1"), Rerooted) {
		t.Errorf("rerooted finding with date checks off: %q", kinds(res, "m1"))
	}
}

func TestLargeCommits(t *testing.T) {
	big := commit("big", 0)
	big.FilesChanged, big.Insertions = 287, 70000
	res := Detect([]git.Commit{big, commit("next", 1, "big")}, nil, false, DefaultThresholds)
	if a := res.ByCommit["big"]; len(a) != 1 || a[0].Kind != LargeCommit || !strings.Contains(a[0].Detail, "287 files") || !strings.Contains(a[0].Detail, "root commit") {
		t.Errorf("big = %+v", a)
	}
	res = Detect([]git.Commit{big}, nil, false, Thresholds{LargeFiles: 1000, LargeLines: 100000})
	if len(res.ByCommit) != 0 {
		t.Errorf("thresholds not honoured: %+v", res.ByCommit)
	}
}

func TestIdentities(t *testing.T) {
	placeholder := commit("p", 0)
	placeholder.AuthorEmail = "developer@example.com"
	webflow := commit("w", 1, "p")
	webflow.Committer, webflow.CommitterEmail = "GitHub", "noreply@github.com"
	signed := commit("s", 2, "w")
	signed.Committer, signed.CommitterEmail = "GitHub", "noreply@github.com"
	signed.Signature = git.Signature{Status: "E", Type: git.SigGPG}

	res := Detect([]git.Commit{placeholder, webflow, signed}, nil, false, DefaultThresholds)
	if kinds(res, "p") != PlaceholderIdentity || kinds(res, "w") != UnsignedWebFlow || kinds(res, "s") != "" {
		t.Errorf("p=%q w=%q s=%q", kinds(res, "p"), kinds(res, "w"), kinds(res, "s"))
	}
	// Author and committer both placeholders is one commit, not two.
	both := commit("both", 3, "s")
	both.AuthorEmail, both.CommitterEmail = "dev@example.com", "dev@example.com"
	res = Detect([]git.Commit{placeholder, webflow, signed, both}, nil, false, DefaultThresholds)
	if c := res.Count(); len(c) != 2 || c[0] != (KindCount{Kind: PlaceholderIdentity, Commits: 2}) {
		t.Errorf("Count = %+v", c)
	}
}
