package snapshot

import (
	"fmt"
	"io"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/anomaly"
	"github.com/andpalmier/repopsy/v2/internal/git"
)

// Manifest records how an explosion was produced. Without it, a folder of
// snapshots says nothing about which repository it came from, when, or with
// which tool — none of which can be recovered afterwards.
type Manifest struct {
	ToolVersion string
	ToolCommit  string
	ToolBuilt   string

	StartedAt  time.Time
	FinishedAt time.Time

	RepoPath string

	// Branch is the single branch requested, or empty when all local branches
	// were exploded.
	Branch  string
	Workers int
	Limit   int // 0 means no limit

	Branches []BranchSummary
	Failures []Failure

	// RefPatterns are the --refs patterns, empty for the default of local
	// branches. AllRefs records --all-refs.
	RefPatterns []string
	AllRefs     bool

	// Namespaces lists every ref namespace in the repository and how much of
	// each was extracted, so what was left out is on the record.
	Namespaces []NamespaceSummary

	// Shallow lists the boundary commits of a shallow clone.
	Shallow []string

	// UniqueCommits counts distinct commits, however many refs reach each.
	UniqueCommits int

	// Options that change what a snapshot contains.
	Dedupe         bool
	NoTree         bool
	PreserveModes  bool
	Paths          []string
	AllowedSigners string

	Signatures SignatureSummary
	Anomalies  AnomalySummary
	PathIssues []PathIssueSummary
}

// NamespaceSummary is one ref namespace, e.g. refs/pull.
type NamespaceSummary struct {
	Name      string
	Refs      int
	Extracted int
	Hint      string // how to include what was left out, if anything
}

// AnomalySummary aggregates the findings written into each COMMIT_INFO.txt.
type AnomalySummary struct {
	Counts     []anomaly.KindCount
	Repository []string
}

// PathIssueSummary is one path problem and how many commits it affected.
type PathIssueSummary struct {
	Issue   git.PathIssue
	Commits int
	First   string // short hash of the first affected commit
}

// BranchSummary is one branch's contribution to the explosion.
type BranchSummary struct {
	Name      string
	Commits   int
	Extracted int

	// Rewritten counts snapshots recovered from the reflog, which no branch
	// reaches any more.
	Rewritten int

	// Unparsed counts records git emitted for this ref that could not be
	// parsed, meaning commits are missing from the listing entirely.
	Unparsed int

	Skipped string // why the branch produced nothing, if it did not
}

// Failure is one snapshot that could not be produced.
type Failure struct {
	Branch    string
	ShortHash string
	Reason    string
}

// Scope describes which refs were extracted.
func (m Manifest) Scope() string {
	switch {
	case m.Branch != "":
		return "branch " + m.Branch
	case m.AllRefs:
		return "all refs except " + strings.Join(ExcludedFromAllRefs, " and ")
	case len(m.RefPatterns) > 0:
		return "refs matching " + strings.Join(m.RefPatterns, ", ")
	default:
		return "all local branches"
	}
}

// ExcludedFromAllRefs are left out of --all-refs. A stash is a working state
// rather than history (REFLOG.txt lists it), and notes refs hold notes, which
// every COMMIT_INFO.txt already carries.
var ExcludedFromAllRefs = []string{"refs/stash", "refs/notes"}

// RefsHeading names the per-ref section: BRANCHES when every row is one.
func (m Manifest) RefsHeading() string {
	if m.Branch != "" || (len(m.RefPatterns) == 0 && !m.AllRefs) {
		return "BRANCHES\n--------"
	}
	return "REFS\n----"
}

// TreeMode describes what each snapshot's tree/ holds.
func (m Manifest) TreeMode() string {
	var parts []string
	switch {
	case m.NoTree:
		parts = append(parts, "not written (--no-tree)")
	case m.PreserveModes:
		parts = append(parts, "written, executable bits kept (--preserve-modes)")
	default:
		parts = append(parts, "written, executable bits removed")
	}
	if len(m.Paths) > 0 {
		parts = append(parts, "limited to "+strings.Join(m.Paths, ", "))
	}
	return strings.Join(parts, "; ")
}

// Snapshots totals the snapshots actually written.
func (m Manifest) Snapshots() int {
	var n int
	for _, b := range m.Branches {
		n += b.Extracted
	}
	return n
}

// Duration is how long the explosion took.
func (m Manifest) Duration() string {
	return m.FinishedAt.Sub(m.StartedAt).Round(time.Millisecond).String()
}

// Filename implements Report.
func (Manifest) Filename() string { return "EXTRACTION.txt" }

// Render writes the provenance record.
func (m Manifest) Render(w io.Writer) error {
	return render(w, manifestTemplate, m.Filename(), m)
}

var manifestTemplate = template.Must(template.New("manifest").Funcs(reportFuncs).Funcs(template.FuncMap{
	"branchLine":    branchLine,
	"failureLine":   failureLine,
	"namespaceLine": namespaceLine,
	"countLine":     func(c Count) string { return fmt.Sprintf("  %6d  %s", c.N, c.Name) },
	"kindLine":      func(k anomaly.KindCount) string { return fmt.Sprintf("  %6d  %s", k.Commits, k.Kind) },
	"pathIssueLine": pathIssueLine,
}).Parse(manifestTemplateStr))

// namespaceLine renders one ref namespace.
func namespaceLine(n NamespaceSummary) string {
	line := fmt.Sprintf("%-24s %5d refs, %5d extracted", n.Name, n.Refs, n.Extracted)
	if n.Hint != "" {
		line += "  (" + n.Hint + ")"
	}
	return line
}

// pathIssueLine renders one aggregated path problem.
func pathIssueLine(p PathIssueSummary) string {
	return fmt.Sprintf("%s  in %d commit(s), first %s", issueLine(p.Issue), p.Commits, p.First)
}

// NewPathIssues aggregates the path problems of the distinct commits, so one
// odd path in a long history is one line rather than one per commit.
func NewPathIssues(commits []git.Commit) []PathIssueSummary {
	index := map[git.PathIssue]int{}
	var out []PathIssueSummary
	for _, c := range commits {
		for _, issue := range c.Tree.Issues {
			if i, ok := index[issue]; ok {
				out[i].Commits++
				continue
			}
			index[issue] = len(out)
			out = append(out, PathIssueSummary{Issue: issue, Commits: 1, First: c.ShortHash})
		}
	}
	return out
}

// Count is one tally line.
type Count struct {
	Name string
	N    int
}

// SignatureSummary tallies signatures across the distinct commits.
type SignatureSummary struct {
	Commits  int
	Signed   int
	Verified int

	ByType      []Count
	ByVerdict   []Count
	ByCommitter []Count
	ByIssuer    []Count
}

// Headline is the summary in one sentence.
func (s SignatureSummary) Headline() string {
	switch {
	case s.Commits == 0:
		return "(no commits)"
	case s.Signed == 0:
		return fmt.Sprintf("None of the %d commits is signed.", s.Commits)
	}
	line := fmt.Sprintf("%d of %d commits signed", s.Signed, s.Commits)
	if len(s.ByCommitter) == 1 {
		line += ", all committed by " + s.ByCommitter[0].Name
	}
	return line + fmt.Sprintf("; %d verified here.", s.Verified)
}

// NewSignatureSummary tallies the signatures of commits, which should be the
// distinct commits so none is counted twice.
func NewSignatureSummary(commits []git.Commit) SignatureSummary {
	s := SignatureSummary{Commits: len(commits)}
	byType, byVerdict, byCommitter, byIssuer := map[string]int{}, map[string]int{}, map[string]int{}, map[string]int{}
	for _, c := range commits {
		sig := c.Signature
		if !sig.Signed() {
			continue
		}
		s.Signed++
		if sig.Verified() {
			s.Verified++
		}
		kind := sig.Type
		if kind == "" {
			kind = "(type not read)"
		}
		byType[kind]++
		byVerdict[FormatSignature(sig)]++
		byCommitter[fmt.Sprintf("%s <%s>", c.Committer, c.CommitterEmail)]++
		issuer := sig.Describe()
		if issuer == "" {
			issuer = "(issuer not stated)"
		}
		byIssuer[kind+" "+issuer]++
	}
	s.ByType, s.ByVerdict = sortedCounts(byType), sortedCounts(byVerdict)
	s.ByCommitter, s.ByIssuer = sortedCounts(byCommitter), sortedCounts(byIssuer)
	return s
}

// sortedCounts orders a tally by count, then by name, so it is deterministic.
func sortedCounts(m map[string]int) []Count {
	out := make([]Count, 0, len(m))
	for k, v := range m {
		out = append(out, Count{Name: k, N: v})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].N != out[j].N {
			return out[i].N > out[j].N
		}
		return out[i].Name < out[j].Name
	})
	return out
}

const manifestTemplateStr = `REPOPSY EXTRACTION MANIFEST
===========================

Provenance record for this exploded repository. Snapshot directories and their
COMMIT_INFO.txt files describe individual commits; this file describes how the
extraction itself was performed.

TOOL
----
Version:        {{.ToolVersion}}
{{if .ToolCommit}}Build commit:   {{.ToolCommit}}
{{end}}{{if .ToolBuilt}}Built:          {{.ToolBuilt}}
{{end}}
EXTRACTION
----------
Started:        {{.StartedAt | formatDate}}
Finished:       {{.FinishedAt | formatDate}}
Duration:       {{.Duration}}

SOURCE
------
Repository:     {{.RepoPath}}
Scope:          {{.Scope}}
Workers:        {{.Workers}}
Commit limit:   {{if .Limit}}{{.Limit}}{{else}}none{{end}}
Shallow:        {{if .Shallow}}YES - history is truncated, see SHALLOW CLONE below{{else}}no{{end}}
Tree:           {{.TreeMode}}
Layout:         {{if .Dedupe}}each commit once under commits/, ref directories link to it{{else}}one snapshot per ref and commit{{end}}
{{if .AllowedSigners}}SSH signers:    {{.AllowedSigners}}
{{end}}
{{.RefsHeading}}
{{if .Branches}}{{range .Branches}}{{branchLine .}}
{{end}}{{else}}(none)
{{end}}
RESULTS
-------
Snapshots:      {{.Snapshots}}
Unique commits: {{.UniqueCommits}}
Failures:       {{len .Failures}}
{{if .Failures}}
FAILURES
--------
{{range .Failures}}{{failureLine .}}
{{end}}{{end}}{{if .Shallow}}
SHALLOW CLONE
-------------
This repository is a shallow clone, so its history stops at the commits below:
their parents are missing. They read as root commits, and refs can look
unrelated when they are not. Fetch the full history ("git fetch --unshallow")
before drawing conclusions from roots or ancestry.

{{range .Shallow}}{{.}}
{{end}}{{end}}{{if .Namespaces}}
REF NAMESPACES
--------------
Every ref namespace in the repository, and how many of its refs were extracted.

{{range .Namespaces}}{{namespaceLine .}}
{{end}}{{end}}
SIGNATURES
----------
{{.Signatures.Headline}}
{{if .Signatures.Signed}}
By type:
{{range .Signatures.ByType}}{{countLine .}}
{{end}}
By verdict:
{{range .Signatures.ByVerdict}}{{countLine .}}
{{end}}
Signed commits by committer:
{{range .Signatures.ByCommitter}}{{countLine .}}
{{end}}
Signed commits by issuer key (claimed unless verified):
{{range .Signatures.ByIssuer}}{{countLine .}}
{{end}}{{end}}
ANOMALIES
---------
{{if or .Anomalies.Counts .Anomalies.Repository}}{{range .Anomalies.Repository}}{{.}}
{{end}}{{if .Anomalies.Counts}}
Commits flagged, by kind (each COMMIT_INFO.txt says why):
{{range .Anomalies.Counts}}{{kindLine .}}
{{end}}{{end}}{{else}}(none)
{{end}}{{if .PathIssues}}
PATH ISSUES
-----------
Tree entries not written where git records them, across all commits.

{{range .PathIssues}}{{pathIssueLine .}}
{{end}}{{end}}`

// branchLine renders one branch row: name, commits found, snapshots written.
func branchLine(b BranchSummary) string {
	if b.Skipped != "" {
		return fmt.Sprintf("%-40s %s", b.Name, b.Skipped)
	}
	line := fmt.Sprintf("%-40s %d commits, %d snapshots", b.Name, b.Commits, b.Extracted)
	if b.Rewritten > 0 {
		line += fmt.Sprintf(" (%d recovered from the reflog)", b.Rewritten)
	}
	if b.Unparsed > 0 {
		line += fmt.Sprintf("  ** %d UNPARSED RECORD(S): COMMITS ARE MISSING **", b.Unparsed)
	}
	return line
}

// failureLine renders one failed snapshot.
func failureLine(f Failure) string {
	if f.Branch != "" {
		return fmt.Sprintf("%-24s %-10s %s", f.Branch, f.ShortHash, f.Reason)
	}
	return fmt.Sprintf("%-10s %s", f.ShortHash, f.Reason)
}
