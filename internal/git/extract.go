// Package git provides functionality for interacting with git repositories.
package git

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

// ExtractCommit extracts the contents of a commit to the specified destination path
func (r *Repository) ExtractCommit(hash, destPath string) error {
	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}
	// Use 'git archive' piped to 'tar' to extract the commit
	// This avoids checking out the commit into the working directory
	return r.runArchiveToTar([]string{"archive", "--format=tar", hash}, destPath)
}

// ExtractCommitExcludingBinaries extracts commit contents, excluding binary files
func (r *Repository) ExtractCommitExcludingBinaries(hash, destPath string, excludeBinaries bool) error {
	if !excludeBinaries {
		return r.ExtractCommit(hash, destPath)
	}

	// Get all files in this commit
	allFiles, err := r.listFiles(hash)
	if err != nil {
		return r.ExtractCommit(hash, destPath)
	}

	// Get binary files
	binaryFiles, err := r.listBinaryFiles(hash)
	if err != nil {
		return r.ExtractCommit(hash, destPath)
	}

	// If no binaries, standard extraction is faster
	if len(binaryFiles) == 0 {
		return r.ExtractCommit(hash, destPath)
	}

	// Filter to only non-binary files
	var textFiles []string
	for _, file := range allFiles {
		if !binaryFiles[file] {
			textFiles = append(textFiles, file)
		}
	}

	// If all files are binary, nothing to extract
	if len(textFiles) == 0 {
		return os.MkdirAll(destPath, 0755)
	}

	if err := os.MkdirAll(destPath, 0755); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Build archive command with explicit pathspecs (files to include)
	archiveArgs := []string{"archive", "--format=tar", hash, "--"}
	archiveArgs = append(archiveArgs, textFiles...)

	return r.runArchiveToTar(archiveArgs, destPath)
}

// runArchiveToTar executes git archive piped to tar for extraction
func (r *Repository) runArchiveToTar(archiveArgs []string, destPath string) error {
	archiveCmd := exec.Command("git", archiveArgs...)
	archiveCmd.Dir = r.Path

	// tar -x: extract
	// -f -: from stdin
	// -C destPath: change directory to destination before extracting
	tarCmd := exec.Command("tar", "-xf", "-", "-C", destPath)

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
		archiveCmd.Process.Kill()
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

// listFiles returns all files in a commit
func (r *Repository) listFiles(hash string) ([]string, error) {
	cmd := exec.Command("git", "ls-tree", "-r", "--name-only", hash)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list files: %w", err)
	}

	var files []string
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		if line := scanner.Text(); line != "" {
			files = append(files, line)
		}
	}
	return files, nil
}

// listBinaryFiles returns a set of file paths that are binary in the given commit
func (r *Repository) listBinaryFiles(hash string) (map[string]bool, error) {
	cmd := exec.Command("git", "diff-tree", "--numstat", "-r", "--root", hash)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("failed to list binary files: %w", err)
	}

	binaryFiles := make(map[string]bool)
	scanner := bufio.NewScanner(bytes.NewReader(output))
	for scanner.Scan() {
		line := scanner.Text()
		// Binary files show as: -\t-\tfilename
		if strings.HasPrefix(line, "-\t-\t") {
			filename := strings.TrimPrefix(line, "-\t-\t")
			binaryFiles[filename] = true
		}
	}

	return binaryFiles, nil
}
