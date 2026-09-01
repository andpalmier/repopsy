package snapshot

import (
	"strings"
	"testing"
	"time"
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
