// Package git provides functionality for interacting with git repositories.
// It wraps the git CLI to provide a clean Go interface for common operations
// like listing commits and extracting commit trees.
package git

import (
	"time"
)

// Commit represents a single git commit with its metadata.
// It contains the essential information needed for identification
// and display purposes during extraction.
type Commit struct {
	// Hash is the full 40-character SHA-1 hash of the commit
	Hash string

	// ShortHash is the abbreviated 7-character hash for display
	ShortHash string

	// Author is the name of the commit author
	Author string

	// Date is the timestamp when the commit was created
	Date time.Time

	// Subject is the first line of the commit message
	Subject string
}

// String returns a human-readable representation of the commit.
func (c Commit) String() string {
	return c.ShortHash + " " + c.Subject
}

// FolderName generates an output folder name based on the specified format.
// Supported formats:
//   - "hash": just the short hash (e.g., "abc1234")
//   - "date-hash": date prefix with hash (e.g., "2024-01-15_abc1234")
//   - "index-hash": index prefix with hash (e.g., "001_abc1234")
func (c Commit) FolderName(format string, index int) string {
	switch format {
	case "date-hash":
		return c.Date.Format("2006-01-02") + "_" + c.ShortHash
	case "index-hash":
		return formatIndex(index) + "_" + c.ShortHash
	default: // "hash"
		return c.ShortHash
	}
}

// formatIndex formats an index number with leading zeros for proper sorting.
func formatIndex(index int) string {
	if index < 10 {
		return "00" + itoa(index)
	} else if index < 100 {
		return "0" + itoa(index)
	}
	return itoa(index)
}

// itoa converts an integer to string without importing strconv.
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	result := ""
	for n > 0 {
		result = string(rune('0'+n%10)) + result
		n /= 10
	}
	return result
}
