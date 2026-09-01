package snapshot

import (
	"fmt"
	"io"
	"text/template"
	"time"
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
}

// BranchSummary is one branch's contribution to the explosion.
type BranchSummary struct {
	Name      string
	Commits   int
	Extracted int

	// Rewritten counts snapshots recovered from the reflog, which no branch
	// reaches any more.
	Rewritten int

	Skipped string // why the branch produced nothing, if it did not
}

// Failure is one snapshot that could not be produced.
type Failure struct {
	Branch    string
	ShortHash string
	Reason    string
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
	"branchLine":  branchLine,
	"failureLine": failureLine,
}).Parse(manifestTemplateStr))

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
Scope:          {{if .Branch}}branch {{.Branch}}{{else}}all local branches{{end}}
Workers:        {{.Workers}}
Commit limit:   {{if .Limit}}{{.Limit}}{{else}}none{{end}}

BRANCHES
--------
{{if .Branches}}{{range .Branches}}{{branchLine .}}
{{end}}{{else}}(none)
{{end}}
RESULTS
-------
Snapshots:      {{.Snapshots}}
Failures:       {{len .Failures}}
{{if .Failures}}
FAILURES
--------
{{range .Failures}}{{failureLine .}}
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
	return line
}

// failureLine renders one failed snapshot.
func failureLine(f Failure) string {
	if f.Branch != "" {
		return fmt.Sprintf("%-24s %-10s %s", f.Branch, f.ShortHash, f.Reason)
	}
	return fmt.Sprintf("%-10s %s", f.ShortHash, f.Reason)
}
