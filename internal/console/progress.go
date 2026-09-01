package console

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/schollz/progressbar/v3"
)

// Progress is the extraction progress bar.
//
// Increment is called concurrently by every worker. progressbar guards Add
// internally but not Clear, and the verbose path pairs a Clear with a separate
// write that must not interleave, so all terminal access here is serialised.
type Progress struct {
	mu      sync.Mutex
	bar     *progressbar.ProgressBar
	verbose bool
	writer  io.Writer
}

// NewProgress creates a progress bar over total items, writing to w (defaulting
// to standard error). When verbose, each completed item also prints a line.
func NewProgress(w io.Writer, total int, verbose bool) *Progress {
	if w == nil {
		w = os.Stderr
	}

	// progressbar only translates the [green]…[reset] markup when colour codes
	// are enabled, and prints it literally otherwise — so the theme has to match
	// the decision. Enabling it unconditionally wrote raw escapes into
	// redirected output.
	colored := supportsColor(w)
	description := "Extracting"
	theme := progressbar.Theme{
		Saucer:        "=",
		SaucerHead:    ">",
		SaucerPadding: " ",
		BarStart:      "[",
		BarEnd:        "]",
	}
	if colored {
		description = "[cyan]Extracting[reset]"
		theme.Saucer = "[green]=[reset]"
		theme.SaucerHead = "[green]>[reset]"
	}

	bar := progressbar.NewOptions(total,
		progressbar.OptionSetWriter(w),
		progressbar.OptionEnableColorCodes(colored),
		progressbar.OptionShowBytes(false),
		progressbar.OptionSetWidth(30),
		progressbar.OptionSetDescription(description),
		progressbar.OptionSetTheme(theme),
		progressbar.OptionOnCompletion(func() {
			_, _ = fmt.Fprint(w, "\n")
		}),
	)

	return &Progress{bar: bar, verbose: verbose, writer: w}
}

// Done records a commit extracted successfully. How much of the path to show is
// a presentation decision, so the caller passes all of it.
func (p *Progress) Done(shortHash, snapshotPath string) {
	p.increment(fmt.Sprintf("✓ %s → %s", shortHash, filepath.Base(snapshotPath)))
}

// Failed records a commit that could not be extracted.
func (p *Progress) Failed(shortHash string, err error) {
	p.increment(fmt.Sprintf("✗ %s: %v", shortHash, err))
}

// increment advances the bar by one item, printing message when verbose.
// Safe for concurrent use.
func (p *Progress) increment(message string) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.verbose && message != "" {
		_ = p.bar.Clear()
		_, _ = fmt.Fprintf(p.writer, "%s\n", message)
	}
	_ = p.bar.Add(1)
}

// Finish completes the bar.
func (p *Progress) Finish() {
	p.mu.Lock()
	defer p.mu.Unlock()
	_ = p.bar.Finish()
}
