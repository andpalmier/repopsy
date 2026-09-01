// Package console owns every byte repopsy writes to the terminal.
//
// Output formatting used to be split between this package, which drew the
// progress bar, and app, which printed the banner, the per-branch lines and the
// summary directly to os.Stderr. Changing how the tool looked meant editing
// both. It all lives here now, behind one io.Writer.
package console

import (
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
)

// Console writes repopsy's human-facing output.
type Console struct {
	w io.Writer
}

// New returns a Console writing to w, defaulting to standard error.
func New(w io.Writer) *Console {
	if w == nil {
		w = os.Stderr
	}
	return &Console{w: w}
}

// Writer exposes the underlying writer so the progress bar can share it.
func (c *Console) Writer() io.Writer { return c.w }

var (
	cyan    = color.New(color.FgCyan, color.Bold).SprintFunc()
	magenta = color.New(color.FgMagenta, color.Bold).SprintFunc()
	yellow  = color.New(color.FgYellow, color.Bold).SprintFunc()
	red     = color.New(color.FgRed, color.Bold).SprintFunc()
	green   = color.New(color.FgGreen, color.Bold).SprintFunc()
)

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
	fmt.Fprintln(c.w, "")
	fmt.Fprintln(c.w, cyan("┌─────────────────────────────────────────┐"))
	fmt.Fprintln(c.w, cyan("│                 repopsy                 │"))
	fmt.Fprintln(c.w, cyan("│ Repository Autopsy tool by @andpalmier  │"))
	fmt.Fprintln(c.w, cyan("└─────────────────────────────────────────┘"))
	fmt.Fprintln(c.w, "")

	fmt.Fprintf(c.w, "Repository:  %s\n", magenta(h.RepoPath))
	if h.Branch != "" {
		fmt.Fprintf(c.w, "Branch:      %s\n", h.Branch)
	} else {
		// "all" used to imply every branch in the repository. Only local
		// branches are ever listed, which on a fresh clone is usually one.
		fmt.Fprintf(c.w, "Branches:    all local\n")
	}
	fmt.Fprintf(c.w, "Output:      %s\n", h.OutputDir)
	fmt.Fprintf(c.w, "Workers:     %d\n", h.Workers)
	if h.Limit > 0 {
		fmt.Fprintf(c.w, "Limit:       %d commits\n", h.Limit)
	}
	fmt.Fprintln(c.w, "")
}

// Warnf prints a warning line prefixed with a marker.
func (c *Console) Warnf(format string, args ...any) {
	fmt.Fprintf(c.w, "%s %s\n", yellow("⚠"), fmt.Sprintf(format, args...))
}

// Infof prints a plain informational line.
func (c *Console) Infof(format string, args ...any) {
	fmt.Fprintf(c.w, format+"\n", args...)
}

// Outcome is one snapshot's result, as far as reporting is concerned. The
// console deliberately does not know about extraction — only about outcomes.
type Outcome struct {
	ShortHash string
	Err       error
}

// Summary prints the closing report: failures if any, then the output location.
func (c *Console) Summary(outputDir string, outcomes []Outcome, verbose bool) {
	fmt.Fprintln(c.w, "")

	var succeeded, failed int
	for _, o := range outcomes {
		if o.Err != nil {
			failed++
		} else {
			succeeded++
		}
	}

	if failed > 0 {
		fmt.Fprintf(c.w, "%s Completed with errors: %d succeeded, %d failed\n", red("⚠"), succeeded, failed)
		if verbose {
			fmt.Fprintln(c.w, "Failed commits:")
			for _, o := range outcomes {
				if o.Err != nil {
					fmt.Fprintf(c.w, "  - %s: %v\n", o.ShortHash, o.Err)
				}
			}
		}
	}

	fmt.Fprintf(c.w, "\n%s Output: %s\n", green("➜"), outputDir)
}
