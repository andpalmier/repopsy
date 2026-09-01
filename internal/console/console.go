// Package console owns every byte repopsy writes to the terminal — the words,
// the indentation, the glyphs and the colour, not just the writer.
//
// Callers name what happened; they never supply layout. That way changing how
// the tool looks means editing this package and nothing else.
package console

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"golang.org/x/term"
)

// Console writes repopsy's human-facing output.
type Console struct {
	w     io.Writer
	color bool
}

// New returns a Console writing to w, defaulting to standard error.
func New(w io.Writer) *Console {
	if w == nil {
		w = os.Stderr
	}
	return &Console{w: w, color: supportsColor(w)}
}

// supportsColor reports whether w is a terminal that can render escapes.
//
// Colour has to follow the stream actually being written to. fatih/color's
// global decides from os.Stdout, but repopsy writes everything to os.Stderr, so
// the two disagree whenever exactly one of them is redirected.
func supportsColor(w io.Writer) bool {
	f, ok := w.(*os.File)
	if !ok {
		return false
	}
	return term.IsTerminal(int(f.Fd()))
}

// tint colours s, or returns it unchanged when this Console has colour off.
// The Color instance is switched on explicitly so it obeys supportsColor rather
// than fatih/color's process-wide guess.
func (c *Console) tint(attr color.Attribute, s string) string {
	if !c.color {
		return s
	}
	col := color.New(attr, color.Bold)
	col.EnableColor()
	return col.Sprint(s)
}

// printf writes to the console's stream.
//
// A failed write is unactionable: the console is the only channel available to
// report that the console cannot be written to. The error is therefore dropped
// deliberately here, once, rather than ignored at two dozen call sites.
func (c *Console) printf(format string, args ...any) {
	_, _ = fmt.Fprintf(c.w, format, args...)
}

// println writes one line to the console's stream.
func (c *Console) println(line string) {
	_, _ = fmt.Fprintln(c.w, line)
}

// warnf prints a warning line prefixed with the shared marker.
func (c *Console) warnf(format string, args ...any) {
	c.printf("%s %s\n", c.tint(color.FgYellow, "⚠"), fmt.Sprintf(format, args...))
}

// Header describes the run that is about to start.
type Header struct {
	RepoPath  string
	Branch    string // empty means all local branches
	OutputDir string
	Workers   int
	Limit     int // 0 means no limit
}

// Banner prints the startup banner and the resolved configuration.
func (c *Console) Banner(h Header) {
	c.println("")
	for _, line := range []string{
		"┌─────────────────────────────────────────┐",
		"│                 repopsy                 │",
		"│ Repository Autopsy tool by @andpalmier  │",
		"└─────────────────────────────────────────┘",
	} {
		c.println(c.tint(color.FgCyan, line))
	}
	c.println("")

	c.printf("Repository:  %s\n", c.tint(color.FgMagenta, h.RepoPath))
	if h.Branch != "" {
		c.printf("Branch:      %s\n", h.Branch)
	} else {
		// "all" used to imply every branch in the repository. Only local
		// branches are ever listed, which on a fresh clone is usually one.
		c.printf("Branches:    all local\n")
	}
	c.printf("Output:      %s\n", h.OutputDir)
	c.printf("Workers:     %d\n", h.Workers)
	if h.Limit > 0 {
		c.printf("Limit:       %d commits\n", h.Limit)
	}
	c.println("")
}

// BranchesFound announces how many local branches will be exploded.
func (c *Console) BranchesFound(n int) {
	c.warnf("Extracting from %d local branches - this may take some time and memory!\n", n)
}

// BranchStarted announces the branch about to be exploded.
func (c *Console) BranchStarted(index, total int, name string) {
	c.printf("Branch [%d/%d]: %s\n", index, total, name)
}

// RewrittenFound reports commits recovered from the reflog, whose existence is
// itself evidence that history was rewritten.
func (c *Console) RewrittenFound(n int) {
	c.printf("  Recovered %d unreachable commits from the reflog\n", n)
}

// DetachedHeadFound reports work abandoned on a detached head, which belongs to
// no branch and so no branch reflog recovers.
func (c *Console) DetachedHeadFound(n int) {
	c.printf("HEAD (detached): recovered %d commits belonging to no branch\n", n)
}

// BranchListFailed reports that a branch's commits could not be listed.
func (c *Console) BranchListFailed(branch string, err error) {
	c.warnf("Failed to list commits on %s: %v", branch, err)
}

// BranchEmpty reports that the current branch has no commits.
func (c *Console) BranchEmpty() {
	c.println("  (no commits)")
}

// BranchCommits reports how many commits the current branch will contribute.
func (c *Console) BranchCommits(n int) {
	c.printf("  Found %d commits\n", n)
}

// CommitsToExtract reports the commit count for a single-branch run.
func (c *Console) CommitsToExtract(n int) {
	c.printf("Found %d commits to extract\n\n", n)
}

// ReportFailed reports that one of the output root's records could not be
// written. The snapshots are already on disk, so this is a warning.
func (c *Console) ReportFailed(name string, err error) {
	c.warnf("Failed to write %s: %v", name, err)
}

// Outcome is one snapshot's result, as far as reporting is concerned. The
// console deliberately does not know about extraction — only about outcomes.
type Outcome struct {
	ShortHash string
	Err       error
}

// Summary prints the closing report: failures if any, then the output location.
func (c *Console) Summary(outputDir string, outcomes []Outcome, verbose bool) {
	c.println("")

	var succeeded, failed int
	for _, o := range outcomes {
		if o.Err != nil {
			failed++
		} else {
			succeeded++
		}
	}

	if failed > 0 {
		c.printf("%s Completed with errors: %d succeeded, %d failed\n",
			c.tint(color.FgRed, "⚠"), succeeded, failed)
		if verbose {
			c.println("Failed commits:")
			for _, o := range outcomes {
				if o.Err != nil {
					c.printf("  - %s: %v\n", o.ShortHash, o.Err)
				}
			}
		}
	}

	c.printf("\n%s Output: %s\n", c.tint(color.FgGreen, "➜"), outputDir)
}
