// Package progress provides terminal progress reporting.
package progress

import (
	"fmt"
	"io"
	"os"

	"github.com/schollz/progressbar/v3"
)

// Reporter handles progress reporting to the terminal.
type Reporter struct {
	bar     *progressbar.ProgressBar
	verbose bool
	writer  io.Writer
}

// Config configures the progress reporter.
type Config struct {
	Total   int
	Verbose bool
	Writer  io.Writer
}

// New creates a new progress reporter.
func New(cfg Config) *Reporter {
	writer := cfg.Writer
	if writer == nil {
		writer = os.Stderr
	}

	bar := progressbar.NewOptions(cfg.Total,
		progressbar.OptionSetWriter(writer),
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
			_, _ = fmt.Fprint(writer, "\n")
		}),
	)

	return &Reporter{
		bar:     bar,
		verbose: cfg.Verbose,
		writer:  writer,
	}
}

// Start begins progress tracking.
func (r *Reporter) Start() {}

// Increment advances the progress by one item.
func (r *Reporter) Increment(message string) {
	if r.verbose && message != "" {
		_ = r.bar.Clear()
		_, _ = fmt.Fprintf(r.writer, "%s\n", message)
	}
	_ = r.bar.Add(1)
}

// Finish completes progress tracking.
func (r *Reporter) Finish() {
	_ = r.bar.Finish()
}

// Error reports an error during processing.
func (r *Reporter) Error(message string) {
	_ = r.bar.Clear()
	_, _ = fmt.Fprintf(r.writer, "✗ Error: %s\n", message)
}
