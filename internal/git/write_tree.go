package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

// writeTree materialises entries under destPath, streaming their content from a
// single cat-file process.
func (r *Repository) writeTree(ctx context.Context, destPath string, entries []treeEntry, opts ExtractOptions) (ExtractResult, error) {
	plan := planTree(entries, opts)
	result := ExtractResult{Submodules: plan.submodules}

	// Symlink targets are needed before deciding which links to keep, and the
	// targets are blob content, so they are read first. They are tiny.
	targets := map[string]string{}
	var linkIDs []string
	var links []int
	for i, b := range plan.blobs {
		if b.asLink {
			linkIDs = append(linkIDs, b.sha)
			links = append(links, i)
		}
	}
	if err := r.catFileBatch(ctx, linkIDs, func(i int, content []byte) error {
		targets[plan.blobs[links[i]].written] = string(content)
		return nil
	}); err != nil {
		return result, err
	}
	kept := resolveLinks(&plan, targets)

	for _, b := range plan.blobs {
		if b.mode == modeExecutable {
			written := "file without the executable bit"
			if opts.PreserveModes {
				written = "executable file"
			}
			plan.report.Special = append(plan.report.Special, SpecialEntry{Path: b.path, Mode: b.mode, Written: written})
		}
	}
	sortSpecial(plan.report.Special, entries)

	// Kept symlinks are created last, so no file is ever written through one.
	var files []plannedEntry
	for _, b := range plan.blobs {
		if _, isLink := kept[b.written]; b.asLink && isLink {
			continue
		}
		files = append(files, b)
	}

	digests := make([]string, len(files))
	var fetch []string
	var fetchIdx []int
	for i, f := range files {
		if sum, ok := opts.Digests.get(f.sha); ok && opts.NoTree {
			digests[i] = sum
			continue
		}
		fetch = append(fetch, f.sha)
		fetchIdx = append(fetchIdx, i)
	}

	err := r.catFile(ctx, fetch, func(n int, content io.Reader, _ int64, found bool) error {
		if !found {
			return fmt.Errorf("object %s is missing", fetch[n])
		}
		i := fetchIdx[n]
		digest, issue, err := writeFileStream(destPath, files[i], content, opts.NoTree, opts.PreserveModes)
		if err != nil {
			return err
		}
		digests[i] = digest
		opts.Digests.put(files[i].sha, digest)
		if issue != "" {
			plan.report.Issues = append(plan.report.Issues, PathIssue{Path: files[i].path, Reason: issue})
			digests[i] = ""
		}
		return nil
	})
	if err != nil {
		return result, err
	}

	if !opts.NoTree {
		for _, b := range plan.blobs {
			target, isLink := kept[b.written]
			if !b.asLink || !isLink {
				continue
			}
			full, err := safeJoin(destPath, b.written)
			if err == nil {
				err = os.Symlink(target, full)
			}
			if err != nil {
				if !isPathProblem(err) {
					return result, fmt.Errorf("failed to create symlink %s: %w", b.path, err)
				}
				plan.report.Issues = append(plan.report.Issues, PathIssue{Path: b.path, Reason: "could not be written here: " + rootCause(err)})
			}
		}
	}

	for i, f := range files {
		if digests[i] != "" {
			result.Digests = append(result.Digests, FileDigest{Path: f.written, SHA256: digests[i]})
		}
	}
	plan.report.NotWritten = opts.NoTree
	result.Tree = plan.report
	return result, nil
}

// sortSpecial puts special entries back in tree order, since symlinks and
// executables are collected in separate passes.
func sortSpecial(special []SpecialEntry, entries []treeEntry) {
	order := make(map[string]int, len(entries))
	for i, e := range entries {
		order[e.path] = i
	}
	slices.SortStableFunc(special, func(a, b SpecialEntry) int { return order[a.Path] - order[b.Path] })
}

// writeFileStream writes one planned file while hashing it. A path the
// filesystem cannot hold is reported back as an issue rather than failing the
// snapshot; anything else, such as a full disk, is an error.
func writeFileStream(destPath string, f plannedEntry, content io.Reader, noTree, preserveModes bool) (digest, issue string, err error) {
	hasher := sha256.New()
	if noTree {
		if _, err := io.Copy(hasher, content); err != nil {
			return "", "", fmt.Errorf("failed to read %s: %w", f.path, err)
		}
		return hex.EncodeToString(hasher.Sum(nil)), "", nil
	}

	target, err := safeJoin(destPath, f.written)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(target), dirPerms); err != nil {
		if isPathProblem(err) {
			return "", "could not be written here: " + rootCause(err), nil
		}
		return "", "", fmt.Errorf("failed to create directory for %s: %w", f.path, err)
	}

	perms := os.FileMode(0o644)
	if preserveModes && f.mode == modeExecutable {
		perms = 0o755
	}
	// O_EXCL: a file already there can only be another entry of this tree that
	// the filesystem considers the same name. Overwriting it would destroy it.
	file, err := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL, perms)
	if err != nil {
		if isPathProblem(err) {
			return "", "could not be written here: " + rootCause(err), nil
		}
		return "", "", fmt.Errorf("failed to write %s: %w", f.path, err)
	}
	_, err = io.Copy(io.MultiWriter(file, hasher), content)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return "", "", fmt.Errorf("failed to write %s: %w", f.path, err)
	}
	return hex.EncodeToString(hasher.Sum(nil)), "", nil
}

// isPathProblem reports whether err means this filesystem cannot hold a path,
// as opposed to failing to write at all. On macOS two names that differ only in
// Unicode normalisation are one file, and a name that is not valid UTF-8 is
// refused outright.
func isPathProblem(err error) bool {
	return errors.Is(err, fs.ErrExist) || errors.Is(err, syscall.ENOTDIR) ||
		errors.Is(err, syscall.EILSEQ) || errors.Is(err, syscall.ENAMETOOLONG) ||
		errors.Is(err, syscall.EINVAL)
}

// rootCause strips the host path from a filesystem error, so the record names
// the problem rather than the examiner's directory layout.
func rootCause(err error) string {
	var pathErr *fs.PathError
	if errors.As(err, &pathErr) {
		return pathErr.Err.Error()
	}
	var linkErr *os.LinkError
	if errors.As(err, &linkErr) {
		return linkErr.Err.Error()
	}
	return err.Error()
}

// safeJoin resolves path under root, refusing anything that would escape it.
// planTree already refuses such paths, so this should never trigger — but a
// forensic tool is pointed at repositories it does not trust.
func safeJoin(root, path string) (string, error) {
	target := filepath.Join(root, filepath.FromSlash(path))
	if target != root && !strings.HasPrefix(target, root+string(filepath.Separator)) {
		return "", fmt.Errorf("tree entry %q escapes the snapshot directory", path)
	}
	return target, nil
}
