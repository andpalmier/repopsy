package console

import (
	"fmt"
	"io"
	"os"
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

	bar := progressbar.NewOptions(total,
		progressbar.OptionSetWriter(w),
		progressbar.OptionEnableColorCodes(true),
		progressbar.OptionShowBytes(false),
		progressbar.OptionSetWidth(30),
		progressbar.OptionSetDescription("[cyan]Extracting[reset]"),
		progressbar.OptionSetTheme(progressbar.Theme{
			Saucer:        "[green]=[reset]",
			SaucerHead:    "[green]>[reset]",
			SaucerPadding: " ",
			BarStart:      "[",
			BarEnd:        "]",
		}),
		progressbar.OptionOnCompletion(func() {
			_, _ = fmt.Fprint(w, "\n")
		}),
	)

	return &Progress{bar: bar, verbose: verbose, writer: w}
}

// Increment advances the bar by one item. Safe for concurrent use.
func (p *Progress) Increment(message string) {
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
