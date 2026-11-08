package snapshot

import (
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/anomaly"
	"github.com/andpalmier/repopsy/v2/internal/git"
)

func sampleManifest() Manifest {
	started, _ := time.Parse(time.RFC3339, "2023-12-05T15:10:10+01:00")
	finished, _ := time.Parse(time.RFC3339, "2023-12-05T15:10:19+01:00")
	return Manifest{
		ToolVersion: "1.4.0",
		ToolCommit:  "58bb650",
		ToolBuilt:   "2023-12-05T12:00:00Z",
		StartedAt:   started,
		FinishedAt:  finished,
		RepoPath:    "/repos/demo",
		Workers:     8,
		Branches: []BranchSummary{
			{Name: "main", Commits: 412, Extracted: 412},
			{Name: "feature/login", Commits: 37, Extracted: 37},
			{Name: "stale-branch", Skipped: "no commits"},
		},
		UniqueCommits: 420,
		Namespaces: []NamespaceSummary{
			{Name: "refs/heads", Refs: 3, Extracted: 3},
			{Name: "refs/pull", Refs: 240, Hint: "--refs 'refs/pull/*/head' adds 120"},
		},
		Signatures: SignatureSummary{
			Commits: 420, Signed: 55,
			ByType:      []Count{{Name: "GPG", N: 55}},
			ByVerdict:   []Count{{Name: "Cannot verify (missing key)", N: 55}},
			ByCommitter: []Count{{Name: "GitHub <noreply@github.com>", N: 55}},
			ByIssuer:    []Count{{Name: "GPG key ID B5690EEEBB952194 (RSA)", N: 55}},
		},
		Anomalies: AnomalySummary{
			Counts:     []anomaly.KindCount{{Kind: "multiple-roots", Commits: 2}},
			Repository: []string{"multiple-roots: 2 root commits: aaaaaaa (2025-01-10), bbbbbbb (2026-03-06)"},
		},
		PathIssues: []PathIssueSummary{
			{Issue: git.PathIssue{Path: ".git/config", WrittenAs: ".git~repopsy/config", Reason: "\".git\" would make the directory a git repository"}, Commits: 3, First: "ccccccc"},
		},
	}
}

func TestWriteManifestGolden(t *testing.T) {
	var buf strings.Builder
	if err := sampleManifest().Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertGolden(t, "manifest.golden", buf.String())
}

func TestWriteManifestWithFailures(t *testing.T) {
	m := sampleManifest()
	m.Branch = "main"
	m.Limit = 25
	m.Branches = []BranchSummary{{Name: "main", Commits: 3, Extracted: 1}}
	m.Failures = []Failure{
		{Branch: "main", ShortHash: "bbb2222", Reason: "git archive failed"},
		{Branch: "main", ShortHash: "ccc3333", Reason: "tar extraction failed"},
	}

	var buf strings.Builder
	if err := m.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	out := buf.String()

	for _, want := range []string{
		"Scope:          branch main",
		"Commit limit:   25",
		"Snapshots:      1",
		"Failures:       2",
		"bbb2222",
		"git archive failed",
		"tar extraction failed",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("manifest missing %q\n%s", want, out)
		}
	}
}

func TestManifestScopeAndLimitDefaults(t *testing.T) {
	var buf strings.Builder
	if err := sampleManifest().Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()

	// An empty Branch means every local branch, and a zero Limit means none.
	if !strings.Contains(out, "Scope:          all local branches") {
		t.Errorf("expected the all-branches scope: %s", out)
	}
	if !strings.Contains(out, "Commit limit:   none") {
		t.Errorf("expected no commit limit: %s", out)
	}
	// With no failures the FAILURES block is omitted entirely.
	if strings.Contains(out, "FAILURES") {
		t.Errorf("FAILURES block present with no failures: %s", out)
	}
}

func TestManifestOmitsUnsetBuildFields(t *testing.T) {
	m := sampleManifest()
	m.ToolCommit, m.ToolBuilt = "", ""

	var buf strings.Builder
	if err := m.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if strings.Contains(out, "Build commit:") || strings.Contains(out, "Built:") {
		t.Errorf("expected unset build fields to be omitted: %s", out)
	}
	if !strings.Contains(out, "Version:        1.4.0") {
		t.Errorf("version missing: %s", out)
	}
}

func TestManifestSnapshotsAndDuration(t *testing.T) {
	m := sampleManifest()
	if got := m.Snapshots(); got != 449 {
		t.Errorf("Snapshots() = %d, want 449", got)
	}
	if got := m.Duration(); got != "9s" {
		t.Errorf("Duration() = %q, want 9s", got)
	}
}

func TestManifestEmptyBranches(t *testing.T) {
	m := sampleManifest()
	m.Branches = nil

	var buf strings.Builder
	if err := m.Render(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(none)") {
		t.Errorf("expected an explicit (none) for no branches: %s", buf.String())
	}
	if m.Snapshots() != 0 {
		t.Errorf("Snapshots() = %d, want 0", m.Snapshots())
	}
}

func TestBranchLine(t *testing.T) {
	if got := branchLine(BranchSummary{Name: "main", Commits: 5, Extracted: 4}); !strings.Contains(got, "5 commits, 4 snapshots") {
		t.Errorf("branchLine = %q", got)
	}
	// A skipped branch reports why instead of counts.
	got := branchLine(BranchSummary{Name: "stale", Skipped: "no commits"})
	if !strings.Contains(got, "no commits") || strings.Contains(got, "snapshots") {
		t.Errorf("branchLine for a skipped branch = %q", got)
	}
}

func TestFailureLine(t *testing.T) {
	if got := failureLine(Failure{Branch: "main", ShortHash: "abc1234", Reason: "boom"}); !strings.Contains(got, "main") || !strings.Contains(got, "abc1234") {
		t.Errorf("failureLine = %q", got)
	}
	// Single-branch runs have no branch label to show.
	if got := failureLine(Failure{ShortHash: "abc1234", Reason: "boom"}); strings.HasPrefix(got, " ") {
		t.Errorf("failureLine without a branch = %q", got)
	}
}

func TestManifestScopeAndOptions(t *testing.T) {
	tests := []struct {
		mutate func(*Manifest)
		want   []string
	}{
		{func(m *Manifest) { m.RefPatterns = []string{"refs/heads/", "refs/pull/*/head"} },
			[]string{"Scope:          refs matching refs/heads/, refs/pull/*/head", "REFS\n----"}},
		{func(m *Manifest) { m.AllRefs = true }, []string{"Scope:          all refs except refs/stash and refs/notes"}},
		{func(m *Manifest) { m.NoTree, m.Paths = true, []string{"src"} }, []string{"Tree:           not written (--no-tree); limited to src"}},
		{func(m *Manifest) { m.PreserveModes, m.Dedupe = true, true },
			[]string{"executable bits kept", "Layout:         each commit once under commits/"}},
		{func(m *Manifest) { m.Shallow = []string{"abc123"} },
			[]string{"Shallow:        YES", "SHALLOW CLONE\n-------------", "\nabc123\n"}},
		{func(m *Manifest) { m.AllowedSigners = "/keys/allowed" }, []string{"SSH signers:    /keys/allowed"}},
		{func(m *Manifest) { m.Anomalies = AnomalySummary{} }, []string{"ANOMALIES\n---------\n(none)"}},
	}
	for _, tc := range tests {
		m := sampleManifest()
		tc.mutate(&m)
		var buf strings.Builder
		if err := m.Render(&buf); err != nil {
			t.Fatal(err)
		}
		for _, want := range tc.want {
			if !strings.Contains(buf.String(), want) {
				t.Errorf("missing %q in:\n%s", want, buf.String())
			}
		}
	}
}

func TestSignatureSummary(t *testing.T) {
	webflow := git.Commit{Committer: "GitHub", CommitterEmail: "noreply@github.com",
		Signature: git.Signature{Status: "E", Type: git.SigGPG, IssuerKeyID: "B5690EEEBB952194"}}
	ssh := git.Commit{Committer: "Dev", CommitterEmail: "dev@company.io",
		Signature: git.Signature{Status: "N", Type: git.SigSSH, NoAllowedSigners: true, IssuerFingerprint: "SHA256:x"}}
	good := git.Commit{Committer: "Dev", CommitterEmail: "dev@company.io", Signature: git.Signature{Status: "G", Type: git.SigGPG}}
	unsigned := git.Commit{Committer: "Dev", CommitterEmail: "dev@company.io", Signature: git.Signature{Status: "N"}}

	s := NewSignatureSummary([]git.Commit{webflow, webflow, unsigned})
	if s.Headline() != "2 of 3 commits signed, all committed by GitHub <noreply@github.com>; 0 verified here." {
		t.Errorf("Headline = %q", s.Headline())
	}
	s = NewSignatureSummary([]git.Commit{webflow, ssh, ssh, good})
	if s.Signed != 4 || s.Verified != 1 {
		t.Errorf("Signed %d Verified %d", s.Signed, s.Verified)
	}
	if s.ByType[0] != (Count{Name: "GPG", N: 2}) && s.ByType[0] != (Count{Name: "SSH", N: 2}) {
		t.Errorf("ByType = %v", s.ByType)
	}
	if s.ByVerdict[0].Name != "SSH signature present, not verified (no allowed signers file)" {
		t.Errorf("ByVerdict = %v", s.ByVerdict)
	}
	if got := NewSignatureSummary([]git.Commit{unsigned}).Headline(); got != "None of the 1 commits is signed." {
		t.Errorf("Headline = %q", got)
	}
}

func TestNewPathIssuesAggregates(t *testing.T) {
	issue := git.PathIssue{Path: "README", WrittenAs: "README~repopsy-1", Reason: "r"}
	commits := []git.Commit{
		{ShortHash: "a", Tree: git.TreeReport{Issues: []git.PathIssue{issue}}},
		{ShortHash: "b"},
		{ShortHash: "c", Tree: git.TreeReport{Issues: []git.PathIssue{issue}}},
	}
	got := NewPathIssues(commits)
	if len(got) != 1 || got[0].Commits != 2 || got[0].First != "a" {
		t.Errorf("got %+v", got)
	}
}
