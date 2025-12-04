// Package progress provides terminal progress reporting for long-running operations.
// It displays a progress bar with percentage, counts, and ETA.
package progress

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"time"
)

// Reporter handles progress reporting to the terminal.
// It is safe for concurrent use from multiple goroutines.
type Reporter struct {
	// Configuration
	total   int
	quiet   bool
	verbose bool
	writer  io.Writer

	// State (protected by mutex)
	mu        sync.Mutex
	current   int
	startTime time.Time
	lastPrint time.Time
}

// Config configures the progress reporter.
type Config struct {
	// Total is the total number of items to process
	Total int

	// Quiet suppresses all output when true
	Quiet bool

	// Verbose enables per-item output when true
	Verbose bool

	// Writer is where to write output (defaults to os.Stderr)
	Writer io.Writer
}

// New creates a new progress reporter with the given configuration.
func New(cfg Config) *Reporter {
	writer := cfg.Writer
	if writer == nil {
		writer = os.Stderr
	}

	return &Reporter{
		total:     cfg.Total,
		quiet:     cfg.Quiet,
		verbose:   cfg.Verbose,
		writer:    writer,
		startTime: time.Now(),
	}
}

// Start begins progress tracking and displays the initial state.
func (r *Reporter) Start() {
	if r.quiet {
		return
	}

	r.mu.Lock()
	r.startTime = time.Now()
	r.mu.Unlock()

	r.print()
}

// Increment advances the progress by one item.
// If message is non-empty and verbose mode is enabled, it prints the message.
func (r *Reporter) Increment(message string) {
	r.mu.Lock()
	r.current++
	current := r.current
	r.mu.Unlock()

	if r.quiet {
		return
	}

	if r.verbose && message != "" {
		// In verbose mode, print each item on its own line
		fmt.Fprintf(r.writer, "\r\033[K[%d/%d] %s\n", current, r.total, message)
		r.print() // Reprint progress bar
	} else {
		// Rate limit updates to avoid flickering
		r.mu.Lock()
		shouldPrint := time.Since(r.lastPrint) > 100*time.Millisecond || current == r.total
		r.mu.Unlock()

		if shouldPrint {
			r.print()
		}
	}
}

// Finish completes progress tracking and prints final state.
func (r *Reporter) Finish() {
	if r.quiet {
		return
	}

	r.mu.Lock()
	elapsed := time.Since(r.startTime)
	r.mu.Unlock()

	// Clear progress line and print completion message
	fmt.Fprintf(r.writer, "\r\033[K✓ Completed %d commits in %s\n", r.total, formatDuration(elapsed))
}

// Error reports an error during processing.
func (r *Reporter) Error(message string) {
	if r.quiet {
		return
	}
	fmt.Fprintf(r.writer, "\r\033[K✗ Error: %s\n", message)
}

// print renders the current progress state to the terminal.
func (r *Reporter) print() {
	r.mu.Lock()
	current := r.current
	elapsed := time.Since(r.startTime)
	r.lastPrint = time.Now()
	r.mu.Unlock()

	// Calculate percentage
	percent := 0
	if r.total > 0 {
		percent = current * 100 / r.total
	}

	// Calculate ETA
	eta := "calculating..."
	if current > 0 {
		avgTime := elapsed / time.Duration(current)
		remaining := time.Duration(r.total-current) * avgTime
		if remaining > 0 {
			eta = formatDuration(remaining)
		} else {
			eta = "almost done"
		}
	}

	// Build progress bar
	barWidth := 30
	filled := barWidth * current / r.total
	if filled > barWidth {
		filled = barWidth
	}
	bar := strings.Repeat("█", filled) + strings.Repeat("░", barWidth-filled)

	// Print progress line (with carriage return to overwrite)
	fmt.Fprintf(r.writer, "\r\033[K[%s] %3d%% (%d/%d) ETA: %s",
		bar, percent, current, r.total, eta)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	if d < time.Second {
		return "< 1s"
	}

	seconds := int(d.Seconds())
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}

	minutes := seconds / 60
	seconds = seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, seconds)
	}

	hours := minutes / 60
	minutes = minutes % 60
	return fmt.Sprintf("%dh %dm", hours, minutes)
}
