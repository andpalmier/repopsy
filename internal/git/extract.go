// Package git provides functionality for interacting with git repositories.
package git

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
)

// dirPerms is the mode for directories created to hold a snapshot.
const dirPerms = 0o755

// ExtractCommit extracts the contents of a commit to the specified destination path
func (r *Repository) ExtractCommit(ctx context.Context, hash, destPath string) error {
	if err := os.MkdirAll(destPath, dirPerms); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	// Use 'git archive' piped to 'tar' to extract the commit
	// This avoids checking out the commit into the working directory
	return r.runArchiveToTar(ctx, []string{"archive", "--format=tar", hash}, destPath)
}

// runArchiveToTar executes git archive piped to tar for extraction
func (r *Repository) runArchiveToTar(ctx context.Context, archiveArgs []string, destPath string) error {
	archiveCmd := exec.CommandContext(ctx, "git", archiveArgs...)
	archiveCmd.Dir = r.Path

	// tar -x: extract
	// -f -: from stdin
	// -C destPath: change directory to destination before extracting
	tarCmd := exec.CommandContext(ctx, "tar", "-xf", "-", "-C", destPath)

	pipe, err := archiveCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to create pipe: %w", err)
	}
	tarCmd.Stdin = pipe

	var archiveStderr, tarStderr bytes.Buffer
	archiveCmd.Stderr = &archiveStderr
	tarCmd.Stderr = &tarStderr

	if err := archiveCmd.Start(); err != nil {
		return fmt.Errorf("failed to start git archive: %w", err)
	}
	if err := tarCmd.Start(); err != nil {
		_ = archiveCmd.Process.Kill()
		return fmt.Errorf("failed to start tar: %w", err)
	}

	archiveErr := archiveCmd.Wait()
	tarErr := tarCmd.Wait()

	if archiveErr != nil {
		return fmt.Errorf("git archive failed: %s", archiveStderr.String())
	}
	if tarErr != nil {
		return fmt.Errorf("tar extraction failed: %s", tarStderr.String())
	}
	return nil
}
