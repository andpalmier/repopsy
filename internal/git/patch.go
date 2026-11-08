package git

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// AddedLine is one line a commit adds, against its first parent.
type AddedLine struct {
	Commit string
	Path   string
	Text   string
}

// AddedLines streams every line the commits named by hashes add, against their
// first parent, to fn. Binary files contribute nothing.
//
// The patch is read by this process rather than searched by git with -G,
// because -G takes POSIX regular expressions: Perl syntax such as \d or (?:
// silently matches nothing there.
func (r *Repository) AddedLines(ctx context.Context, hashes []string, fn func(AddedLine)) (err error) {
	if len(hashes) == 0 {
		return nil
	}
	// Every option that could change the patch's shape is fixed explicitly, so
	// the examined repository's configuration cannot.
	cmd := r.command(ctx, "-c", "core.quotePath=false", "log", "--no-walk=unsorted", "--stdin",
		"--format=%x1e%H", "-p", "-U0", "--no-color", "--no-textconv", "--no-ext-diff",
		"--no-show-signature", "--no-relative", "--src-prefix=a/", "--dst-prefix=b/",
		"--diff-merges=first-parent")
	cmd.Stdin = strings.NewReader(strings.Join(hashes, "\n") + "\n")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start git log: %w", err)
	}
	defer func() {
		if err != nil {
			_ = cmd.Process.Kill()
		}
		if waitErr := cmd.Wait(); waitErr != nil && err == nil {
			err = fmt.Errorf("git log -p failed: %s", strings.TrimSpace(stderr.String()))
		}
	}()

	reader := bufio.NewReader(stdout)
	var commit, path string
	inHunk := false
	for {
		line, readErr := reader.ReadString('\n')
		line = strings.TrimSuffix(line, "\n")
		switch {
		case strings.HasPrefix(line, recordSep):
			commit, path, inHunk = strings.TrimPrefix(line, recordSep), "", false
		case strings.HasPrefix(line, "diff --git "):
			path, inHunk = "", false
		case !inHunk && strings.HasPrefix(line, "+++ "):
			path = patchPath(strings.TrimPrefix(line, "+++ "))
		case strings.HasPrefix(line, "@@"):
			inHunk = true
		case inHunk && strings.HasPrefix(line, "+") && path != "":
			fn(AddedLine{Commit: commit, Path: path, Text: line[1:]})
		}
		if readErr != nil {
			if errors.Is(readErr, io.EOF) {
				return nil
			}
			return readErr
		}
	}
}

// patchPath reads the path of a "+++ b/<path>" header, which git quotes in C
// style when it contains unusual bytes.
func patchPath(header string) string {
	if strings.HasPrefix(header, `"`) {
		if unquoted, err := strconv.Unquote(header); err == nil {
			header = unquoted
		}
	}
	if header == "/dev/null" {
		return ""
	}
	return strings.TrimPrefix(header, "b/")
}

// RefsContaining lists every ref, of any kind, that reaches commit.
func (r *Repository) RefsContaining(ctx context.Context, commit string) ([]string, error) {
	out, err := r.output(ctx, "for-each-ref", "--format=%(refname)", "--contains", commit)
	if err != nil || out == "" {
		return nil, err
	}
	return strings.Split(out, "\n"), nil
}
