package git

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"path"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// TreeSize returns how many bytes of content, and how many files, extracting
// a commit's tree would write. Symlinks count as the length of their target,
// gitlinks as nothing, matching what extraction writes.
func (r *Repository) TreeSize(ctx context.Context, hash string, filter *PathFilter) (bytes int64, files int, err error) {
	out, err := r.command(ctx, "ls-tree", "-r", "-l", "-z", "--end-of-options", hash).Output()
	if err != nil {
		return 0, 0, fmt.Errorf("git ls-tree failed: %w", err)
	}
	for record := range strings.SplitSeq(string(out), "\x00") {
		// "<mode> <type> <sha> <size>\t<path>", the size padded.
		meta, p, found := strings.Cut(record, "\t")
		fields := strings.Fields(meta)
		if !found || len(fields) != 4 || (filter != nil && !filter.Match(p)) {
			continue
		}
		if size, err := strconv.ParseInt(fields[3], 10, 64); err == nil {
			bytes += size
			files++
		}
	}
	return bytes, files, nil
}

// treeEntry is one row of git ls-tree output.
type treeEntry struct {
	mode string
	sha  string
	path string
}

// listTree returns every blob and gitlink in a commit's tree. -z terminates
// records with NUL, so paths containing newlines or quotes stay intact.
func (r *Repository) listTree(ctx context.Context, hash string) ([]treeEntry, error) {
	cmd := r.command(ctx, "ls-tree", "-r", "-z", "--end-of-options", hash)

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

// plannedEntry is a blob with the decisions made about it.
type plannedEntry struct {
	treeEntry

	// written is where the entry goes, relative to the tree directory and
	// slash-separated.
	written string

	// asLink creates a symlink. A symlink that would resolve outside the tree
	// is written as a plain file holding its target instead.
	asLink bool
}

// treePlan is everything decided about a tree before any of it is written.
type treePlan struct {
	blobs      []plannedEntry
	submodules []Submodule
	report     TreeReport
}

// planTree decides where every entry is written and how. It works from the
// listing alone, so the outcome does not depend on the host's filesystem.
//
// A path is never written outside the tree directory: components git itself
// refuses (empty, ".", "..") cause the entry to be skipped, and a component
// that would make the directory a git repository (".git" and its spellings on
// case-insensitive, HFS+ and NTFS filesystems) is renamed. Two paths that
// differ only in case would land on one file on macOS and Windows, so the later
// one is renamed on every host. Symlinks are kept only while they resolve
// inside the tree. Everything that is not written as recorded is reported.
func planTree(entries []treeEntry, opts ExtractOptions) treePlan {
	var plan treePlan
	mapper := newPathMapper()

	for _, e := range entries {
		if opts.Paths != nil && !opts.Paths.Match(e.path) {
			plan.report.Filtered++
			continue
		}
		if e.mode == modeSubmodule {
			plan.submodules = append(plan.submodules, Submodule{Path: e.path, Commit: e.sha})
			continue
		}

		written, renamed, reason := mapper.place(e.path)
		if written == "" {
			plan.report.Issues = append(plan.report.Issues, PathIssue{Path: e.path, Reason: reason})
			continue
		}
		if renamed {
			plan.report.Issues = append(plan.report.Issues, PathIssue{Path: e.path, WrittenAs: written, Reason: reason})
		}
		plan.blobs = append(plan.blobs, plannedEntry{treeEntry: e, written: written, asLink: e.mode == modeSymlink})
	}
	return plan
}

// pathMapper assigns each tree path the path it is written to.
type pathMapper struct {
	// dirs maps an original directory prefix to the prefix it was written as,
	// so every entry under one directory lands in the same place.
	dirs map[string]string

	// taken maps the case-folded form of every written path to whether it is
	// a directory.
	taken map[string]bool
}

func newPathMapper() *pathMapper {
	return &pathMapper{dirs: map[string]string{}, taken: map[string]bool{}}
}

// place returns where p is written, whether that differs from p, and why. An
// empty result means the entry cannot be written at all.
func (m *pathMapper) place(p string) (written string, renamed bool, reason string) {
	components := strings.Split(p, "/")
	var reasons []string
	parent := ""

	for i, c := range components {
		if c == "" || c == "." || c == ".." {
			return "", false, fmt.Sprintf("path component %q is not a valid name", c)
		}
		leaf := i == len(components)-1
		origPrefix := strings.Join(components[:i+1], "/")

		if !leaf {
			if w, ok := m.dirs[origPrefix]; ok {
				parent = w
				continue
			}
		}

		name := c
		if isDotGit(c) {
			name = c + renameSuffix
			reasons = append(reasons, fmt.Sprintf("%q would make the directory a git repository", c))
		}
		if safe := safePathComponent(name); safe != name {
			name = safe + renameSuffix
			reasons = append(reasons, fmt.Sprintf("%q contains a control character that cannot appear in a line-based record", c))
		}
		candidate := joinSlash(parent, name)
		for n := 1; ; n++ {
			if _, clash := m.taken[strings.ToLower(candidate)]; !clash {
				break
			}
			if n == 1 {
				reasons = append(reasons, fmt.Sprintf("%q collides with another entry on case-insensitive filesystems", c))
			}
			candidate = joinSlash(parent, name+renameSuffix+"-"+strconv.Itoa(n))
		}
		m.taken[strings.ToLower(candidate)] = !leaf
		if !leaf {
			m.dirs[origPrefix] = candidate
		}
		parent = candidate
	}

	// Rejects absolute paths everywhere, and reserved names such as NUL on
	// Windows. Only Windows can reach this with a path that passed the checks
	// above, so only there does it make output differ by host.
	if !filepath.IsLocal(filepath.FromSlash(parent)) {
		return "", false, "not a valid local path on this system"
	}
	return parent, parent != p, strings.Join(reasons, "; ")
}

// safePathComponent replaces control characters that would break line-based
// records such as SHA256SUMS. It is deterministic and only runs on paths that
// would otherwise make the extracted tree impossible to verify.
func safePathComponent(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			b.WriteByte('_')
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

func joinSlash(dir, name string) string {
	if dir == "" {
		return name
	}
	return dir + "/" + name
}

// hfsIgnorable are the code points HFS+ drops when comparing names, which made
// ".g\u200cit" equal to ".git" there. git refuses them for the same reason.
var hfsIgnorable = strings.NewReplacer(
	"\u200c", "", "\u200d", "", "\u200e", "", "\u200f", "",
	"\u202a", "", "\u202b", "", "\u202c", "", "\u202d", "", "\u202e", "",
	"\u206a", "", "\u206b", "", "\u206c", "", "\u206d", "", "\u206e", "", "\u206f", "",
	"\ufeff", "",
)

// isDotGit reports whether a path component names a git directory on some
// filesystem: any case of ".git", with HFS+-ignorable code points, with the
// trailing dots and spaces NTFS drops, or as the NTFS short name "git~1".
func isDotGit(c string) bool {
	c = strings.TrimRight(hfsIgnorable.Replace(c), ". ")
	return strings.EqualFold(c, ".git") || strings.EqualFold(c, "git~1")
}

// resolveLinks decides which symlinks stay symlinks. A link is kept only if
// following it, through any other kept link, never leaves the tree. A link
// that is not kept is written as a plain file, which can resolve nothing, so
// links are dropped until the remaining set is closed.
//
// Checking each target lexically is not enough: with "s -> ." a link
// "l -> s/../x" looks like it names "x", but s/.. is the tree's parent.
func resolveLinks(plan *treePlan, targets map[string]string) map[string]string {
	kept := map[string]string{}
	for p, t := range targets {
		kept[p] = t
	}
	// Checked in path order: dropping one link can make another safe (two
	// links pointing at each other), so the order decides which one stays,
	// and it must not depend on map iteration.
	paths := make([]string, 0, len(targets))
	for p := range targets {
		paths = append(paths, p)
	}
	slices.Sort(paths)
	reasons := map[string]string{}
	for changed := true; changed; {
		changed = false
		for _, p := range paths {
			t, ok := kept[p]
			if !ok {
				continue
			}
			if why := linkEscapes(kept, p, t); why != "" {
				delete(kept, p)
				reasons[p] = why
				changed = true
			}
		}
	}

	for i := range plan.blobs {
		b := &plan.blobs[i]
		if !b.asLink {
			continue
		}
		if why, unsafe := reasons[b.written]; unsafe {
			b.asLink = false
			plan.report.Special = append(plan.report.Special, SpecialEntry{
				Path: b.path, Mode: b.mode, Target: targets[b.written],
				Written: "plain file holding the target, because " + why,
			})
			continue
		}
		plan.report.Special = append(plan.report.Special, SpecialEntry{
			Path: b.path, Mode: b.mode, Target: targets[b.written], Written: "symlink",
		})
	}
	return kept
}

// linkEscapes returns why the link at p, pointing at target, leaves the tree,
// or "" if it stays inside.
func linkEscapes(links map[string]string, p, target string) string {
	if isAbsTarget(target) {
		return "the target is absolute"
	}
	cur := strings.Split(path.Dir(p), "/")
	if path.Dir(p) == "." {
		cur = nil
	}
	queue := strings.Split(target, "/")
	for depth := 0; len(queue) > 0; {
		c := queue[0]
		queue = queue[1:]
		switch c {
		case "", ".":
			continue
		case "..":
			if len(cur) == 0 {
				return "the target resolves outside the tree"
			}
			cur = cur[:len(cur)-1]
			continue
		}
		cur = append(cur, c)
		next, isLink := links[strings.Join(cur, "/")]
		if !isLink {
			continue
		}
		if depth++; depth > maxLinkDepth {
			return "the target does not resolve (symlink loop)"
		}
		if isAbsTarget(next) {
			return "the target resolves through an absolute symlink"
		}
		cur = cur[:len(cur)-1]
		queue = append(strings.Split(next, "/"), queue...)
	}
	return ""
}

// isAbsTarget reports whether a symlink target is absolute, or could be read
// as a path on Windows, where a backslash is a separator and "C:" a drive.
// Treated alike on every host so the outcome does not depend on it.
func isAbsTarget(t string) bool {
	return strings.HasPrefix(t, "/") || strings.Contains(t, `\`) ||
		(len(t) >= 2 && t[1] == ':')
}
