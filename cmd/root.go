// Package cmd is repopsy's command line: it parses arguments, reports failures,
// and hands a resolved configuration to the app package.
package cmd

const (
	appName = "repopsy"

	// maxWorkers is the safety cap on parallel workers per branch. Each worker
	// runs git ls-tree and git cat-file, so this bounds concurrent Git processes.
	maxWorkers = 32
)
