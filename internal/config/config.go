// Package config holds centralized constants and configuration defaults
// for the repopsy application.
package config

// Concurrency defaults and limits
const (
	// Default workers per branch
	DefaultWorkers = 4

	// Default number of branches to process concurrently
	DefaultConcurrentBranches = 4

	// Default scanner buffer size (1MB)
	DefaultBufferSize = 1024 * 1024

	// Maximum workers limit (safety cap)
	MaxWorkersLimit = 32

	// Maximum concurrent branches limit
	MaxConcurrentBranches = 8

	// Maximum total concurrent operations
	MaxConcurrency = 32

	// Minimum buffer size
	MinBufferSize = 4096
)

// Output directory defaults
const (
	// Suffix appended to repo name when creating output directory
	OutputSuffix = "-exploded"

	// Directory permissions for created directories
	OutputDirPerms = 0o755

	// File permissions for test files
	TestFilePerms = 0o600
)

// Git constants
const (
	// Folder timestamp format
	FolderTimestampFormat = "20060102_150405"
)
