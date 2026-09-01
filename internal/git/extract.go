// Package git provides functionality for interacting with git repositories.
package git

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
)

const (
	// dirPerms is the mode for directories created to hold a snapshot.
	dirPerms = 0o755

	// git's tree entry modes.
	modeExecutable = "100755"
	modeSymlink    = "120000"
	modeSubmodule  = "160000"
)

// Submodule is a gitlink: a pointer to a commit in another repository.
//
// git stores only the pointer, so a snapshot cannot contain the submodule's
// content. Recording the pointer is the difference between an incomplete
// snapshot and a silently incomplete one.
type Submodule struct {
	Path   string
	Commit string
}

// ExtractResult describes what one extraction produced.
type ExtractResult struct {
	// Files is the number of blobs written.
	Files int

	// Submodules are the gitlinks encountered, whose content is not captured.
	Submodules []Submodule

	// Digests is the SHA-256 of every file written, in tree order. The bytes
	// pass through this process anyway, so hashing them is nearly free — and it
	// lets a reader prove the snapshot was not altered after extraction.
	Digests []FileDigest
}

// FileDigest is one extracted file and the hash of its content.
type FileDigest struct {
	Path   string
	SHA256 string
}

// treeEntry is one row of git ls-tree output.
type treeEntry struct {
	mode string
	sha  string
	path string
}

// ExtractCommit writes a commit's complete tree into destPath.
//
// The tree is read with ls-tree and cat-file rather than piped through
// "git archive | tar", for two reasons. git archive honours export-ignore from
// the repository's own .gitattributes, which lets the subject of an
// investigation withhold files from its own snapshot — it cannot be overridden,
// as pathspecs and --worktree-attributes both still respect it and there is no
// --no-export-ignore. And it removes tar as an undeclared runtime dependency.
// See docs/adr/0005.
func (r *Repository) ExtractCommit(ctx context.Context, hash, destPath string) (ExtractResult, error) {
	entries, err := r.listTree(ctx, hash)
	if err != nil {
		return ExtractResult{}, err
	}

	if err := os.MkdirAll(destPath, dirPerms); err != nil {
		return ExtractResult{}, fmt.Errorf("failed to create output directory: %w", err)
	}

	return r.writeTree(ctx, destPath, entries)
}

// listTree returns every blob and gitlink in a commit's tree. -z terminates
// records with NUL, so paths containing newlines or quotes stay intact.
func (r *Repository) listTree(ctx context.Context, hash string) ([]treeEntry, error) {
	cmd := exec.CommandContext(ctx, "git", "ls-tree", "-r", "-z", hash)
	cmd.Dir = r.Path

	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return nil, fmt.Errorf("git ls-tree failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, fmt.Errorf("git ls-tree failed: %w", err)
	}

	var entries []treeEntry
	for record := range strings.SplitSeq(string(output), "\x00") {
		// "<mode> <type> <sha>\t<path>"
		meta, path, found := strings.Cut(record, "\t")
		if !found {
			continue
		}
		fields := strings.Fields(meta)
		if len(fields) != 3 {
			continue
		}
		entries = append(entries, treeEntry{mode: fields[0], sha: fields[2], path: path})
	}
	return entries, nil
}

// writeTree materialises entries under destPath, streaming their content from a
// single cat-file process.
func (r *Repository) writeTree(ctx context.Context, destPath string, entries []treeEntry) (ExtractResult, error) {
	var result ExtractResult

	// Gitlinks have no content to fetch, so they are handled without cat-file.
	var blobs []treeEntry
	for _, e := range entries {
		if e.mode == modeSubmodule {
			result.Submodules = append(result.Submodules, Submodule{Path: e.path, Commit: e.sha})
			continue
		}
		blobs = append(blobs, e)
	}
	if len(blobs) == 0 {
		return result, nil
	}

	cmd := exec.CommandContext(ctx, "git", "cat-file", "--batch")
	cmd.Dir = r.Path
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return result, fmt.Errorf("failed to open cat-file stdin: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return result, fmt.Errorf("failed to open cat-file stdout: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return result, fmt.Errorf("failed to start git cat-file: %w", err)
	}
	defer func() { _ = cmd.Wait() }()

	// Requests are written while responses are read, rather than in lockstep:
	// a round trip per blob costs more than the whole extraction. cat-file
	// answers in order, so the reader still knows which entry it is looking at.
	// Neither pipe can deadlock because stdout is drained throughout.
	go func() {
		defer stdin.Close()
		w := bufio.NewWriter(stdin)
		for _, e := range blobs {
			if _, err := fmt.Fprintln(w, e.sha); err != nil {
				return
			}
		}
		_ = w.Flush()
	}()

	reader := bufio.NewReader(stdout)
	for _, e := range blobs {
		content, err := readBatchObject(reader)
		if err != nil {
			return result, fmt.Errorf("failed to read %s: %w", e.path, err)
		}
		if err := writeEntry(destPath, e, content); err != nil {
			return result, err
		}
		sum := sha256.Sum256(content)
		result.Digests = append(result.Digests, FileDigest{Path: e.path, SHA256: hex.EncodeToString(sum[:])})
		result.Files++
	}

	return result, nil
}

// readBatchObject reads one "<sha> <type> <size>\n<content>\n" response.
func readBatchObject(reader *bufio.Reader) ([]byte, error) {
	header, err := reader.ReadString('\n')
	if err != nil {
		return nil, err
	}
	fields := strings.Fields(header)
	if len(fields) != 3 {
		return nil, fmt.Errorf("unexpected cat-file header %q", strings.TrimSpace(header))
	}
	size, err := strconv.Atoi(fields[2])
	if err != nil {
		return nil, fmt.Errorf("unexpected cat-file size in %q", strings.TrimSpace(header))
	}

	content := make([]byte, size)
	if _, err := io.ReadFull(reader, content); err != nil {
		return nil, err
	}
	// Each object is followed by a newline that is not part of the content.
	if _, err := reader.Discard(1); err != nil {
		return nil, err
	}
	return content, nil
}

// writeEntry materialises one blob as a file or a symlink.
func writeEntry(destPath string, e treeEntry, content []byte) error {
	target, err := safeJoin(destPath, e.path)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(target), dirPerms); err != nil {
		return fmt.Errorf("failed to create directory for %s: %w", e.path, err)
	}

	// A symlink's blob content is the path it points at.
	if e.mode == modeSymlink {
		if err := os.Symlink(string(content), target); err != nil {
			return fmt.Errorf("failed to create symlink %s: %w", e.path, err)
		}
		return nil
	}

	perms := os.FileMode(0o644)
	if e.mode == modeExecutable {
		perms = 0o755
	}
	if err := os.WriteFile(target, content, perms); err != nil {
		return fmt.Errorf("failed to write %s: %w", e.path, err)
	}
	return nil
}

// safeJoin resolves path under root, refusing anything that would escape it.
// git rejects ".." and absolute paths in tree entries, so this should never
// trigger — but a forensic tool is pointed at repositories it does not trust.
func safeJoin(root, path string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(path))
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("tree entry %q escapes the snapshot directory", path)
	}
	return target, nil
}
