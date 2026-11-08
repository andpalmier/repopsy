package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// hostileCommit builds a commit from a hand-written tree, which is the only way
// to get entries git itself would never create. Each line is
// "<mode> <path> <content>"; a path containing "/" becomes nested trees.
func (r *repo) hostileCommit(entries ...string) string {
	r.Helper()
	type node struct {
		blobs map[string][2]string // name -> mode, sha
		dirs  map[string]*node
		order []string
	}
	newNode := func() *node { return &node{blobs: map[string][2]string{}, dirs: map[string]*node{}} }
	root := newNode()
	for _, e := range entries {
		parts := strings.SplitN(e, " ", 3)
		mode, path, content := parts[0], parts[1], parts[2]
		sha := r.hashObject(content)
		n := root
		comps := strings.Split(path, "/")
		for _, c := range comps[:len(comps)-1] {
			if n.dirs[c] == nil {
				n.dirs[c] = newNode()
				n.order = append(n.order, "d:"+c)
			}
			n = n.dirs[c]
		}
		leaf := comps[len(comps)-1]
		n.blobs[leaf] = [2]string{mode, sha}
		n.order = append(n.order, "b:"+leaf)
	}
	var write func(n *node) string
	write = func(n *node) string {
		var lines []string
		for _, o := range n.order {
			kind, name := o[:1], o[2:]
			if kind == "d" {
				lines = append(lines, fmt.Sprintf("040000 tree %s\t%s", write(n.dirs[name]), name))
			} else {
				b := n.blobs[name]
				lines = append(lines, fmt.Sprintf("%s blob %s\t%s", b[0], b[1], name))
			}
		}
		return r.gitStdin(strings.Join(lines, "\x00")+"\x00", "mktree", "-z")
	}
	return r.gitStdin("", "commit-tree", write(root), "-m", "hostile")
}

func (r *repo) hashObject(content string) string {
	r.Helper()
	return r.gitStdin(content, "hash-object", "-w", "--stdin")
}

func (r *repo) gitStdin(stdin string, args ...string) string {
	r.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = r.Dir
	cmd.Stdin = strings.NewReader(stdin)
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	out, err := cmd.Output()
	if err != nil {
		r.Fatalf("git %v: %v", args, err)
	}
	return strings.TrimSpace(string(out))
}

func (r *repo) Helper()                           { r.t.Helper() }
func (r *repo) Fatalf(format string, args ...any) { r.t.Fatalf(format, args...) }

// extractInto extracts hash into a "tree" directory inside a fresh parent, so
// a test can check that nothing appeared beside it.
func extractInto(t *testing.T, r *repo, hash string, opts ExtractOptions) (string, ExtractResult) {
	t.Helper()
	parent := t.TempDir()
	dest := filepath.Join(parent, "tree")
	res, err := r.open().Extract(context.Background(), hash, dest, opts)
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	// Nothing may be written outside the destination.
	siblings, _ := os.ReadDir(parent)
	if len(siblings) != 1 {
		t.Fatalf("extraction wrote outside its directory: %v", siblings)
	}
	return dest, res
}

func special(res ExtractResult, path string) (SpecialEntry, bool) {
	for _, s := range res.Tree.Special {
		if s.Path == path {
			return s, true
		}
	}
	return SpecialEntry{}, false
}

func TestExtractNeverWritesAnEscapingSymlink(t *testing.T) {
	b := newRepo(t)
	hash := b.hostileCommit(
		"100644 docs/readme.md hello",
		"120000 docs/ok readme.md",
		"120000 docs/up ../docs/readme.md",
		"120000 abs /etc/passwd",
		"120000 escape ../../outside",
		"120000 deep/escape ../../../outside",
		// Each target looks harmless on its own, but s/.. is the tree's parent.
		"120000 s .",
		"120000 chain s/../x",
		"120000 win ..\\..\\x",
		"120000 drive C:x",
		"120000 loop1 loop2",
		"120000 loop2 loop1",
	)
	dest, res := extractInto(t, b, hash, ExtractOptions{})

	// Links that stay inside remain links.
	// Of two links pointing at each other, the first in path order becomes a
	// file, which leaves the second a safe link to it, on every run.
	for _, p := range []string{"docs/ok", "docs/up", "s", "loop2"} {
		info, err := os.Lstat(filepath.Join(dest, p))
		if err != nil || info.Mode()&os.ModeSymlink == 0 {
			t.Errorf("%s should stay a symlink (err %v)", p, err)
		}
	}

	// Links that leave become plain files holding their target.
	for p, target := range map[string]string{
		"abs": "/etc/passwd", "escape": "../../outside", "deep/escape": "../../../outside",
		"chain": "s/../x", "win": "..\\..\\x", "drive": "C:x", "loop1": "loop2",
	} {
		info, err := os.Lstat(filepath.Join(dest, p))
		if err != nil {
			t.Errorf("%s: %v", p, err)
			continue
		}
		if !info.Mode().IsRegular() {
			t.Errorf("%s is %v, want a regular file", p, info.Mode())
			continue
		}
		if got, _ := os.ReadFile(filepath.Join(dest, p)); string(got) != target {
			t.Errorf("%s holds %q, want %q", p, got, target)
		}
		s, ok := special(res, p)
		if !ok || s.Target != target || !strings.HasPrefix(s.Written, "plain file holding the target") {
			t.Errorf("%s special entry = %+v", p, s)
		}
	}

	// Every link left on disk resolves inside the tree.
	_ = filepath.Walk(dest, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode()&os.ModeSymlink != 0 {
			resolved, err := filepath.EvalSymlinks(p)
			if err == nil && !strings.HasPrefix(resolved, mustEval(t, dest)) {
				t.Errorf("%s resolves to %s, outside the tree", p, resolved)
			}
		}
		return nil
	})

	// sha256sum would follow a symlink, so only regular files are listed.
	for _, d := range res.Digests {
		if d.Path == "docs/up" || d.Path == "s" {
			t.Errorf("symlink %s listed in the digests", d.Path)
		}
	}
}

func mustEval(t *testing.T, p string) string {
	t.Helper()
	r, err := filepath.EvalSymlinks(p)
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func TestExtractContainsHostilePaths(t *testing.T) {
	b := newRepo(t)
	hash := b.hostileCommit(
		"100644 ../escape.txt nope",
		"100644 ok.txt fine",
		"100755 .git/config [core]\n\tfsmonitor = touch /tmp/pwned\n",
		"100644 sub/.GIT/HEAD ref",
		"100644 hfs/.g\u200cit/x hfs",
		"100644 ntfs/git~1/x ntfs",
		"100644 ntfs/.git./y ntfs",
		"100644 .github/ok.yml ok",
		"100644 .gitignore ok",
		"100644 README first",
		"100644 readme second",
		"100644 Docs/a.txt a",
		"100644 docs/b.txt b",
		"100644 line\nbreak.txt newline",
	)
	dest, res := extractInto(t, b, hash, ExtractOptions{})

	issues := map[string]PathIssue{}
	for _, i := range res.Tree.Issues {
		issues[i.Path] = i
	}

	// ".." is skipped and reported.
	if i, ok := issues["../escape.txt"]; !ok || i.WrittenAs != "" {
		t.Errorf("../escape.txt issue = %+v, %v", i, ok)
	}

	// A .git directory in any case is renamed, so the tree is never a repository.
	for orig, want := range map[string]string{
		".git/config":      ".git~repopsy/config",
		"sub/.GIT/HEAD":    "sub/.GIT~repopsy/HEAD",
		"hfs/.g\u200cit/x": "hfs/.g\u200cit~repopsy/x",
		"ntfs/git~1/x":     "ntfs/git~1~repopsy/x",
		"ntfs/.git./y":     "ntfs/.git.~repopsy/y",
		"line\nbreak.txt":  "line_break.txt~repopsy",
	} {
		if i := issues[orig]; i.WrittenAs != want {
			t.Errorf("%s written as %q, want %q", orig, i.WrittenAs, want)
		}
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(want))); err != nil {
			t.Errorf("%s: %v", want, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dest, ".git")); err == nil {
		t.Error("a .git directory was written into the tree")
	}
	for _, lookalike := range []string{".github/ok.yml", ".gitignore"} {
		if _, renamed := issues[lookalike]; renamed {
			t.Errorf("%s was renamed", lookalike)
		}
	}

	// Case collisions keep both files, on every host.
	if i := issues["readme"]; i.WrittenAs != "readme~repopsy-1" {
		t.Errorf("readme written as %q", i.WrittenAs)
	}
	if i := issues["docs/b.txt"]; i.WrittenAs != "docs~repopsy-1/b.txt" {
		t.Errorf("docs/b.txt written as %q", i.WrittenAs)
	}
	for p, want := range map[string]string{"README": "first", "readme~repopsy-1": "second", "ok.txt": "fine"} {
		if got, err := os.ReadFile(filepath.Join(dest, p)); err != nil || string(got) != want {
			t.Errorf("%s = %q (%v), want %q", p, got, err, want)
		}
	}

	// The integrity record names the paths as written, so sha256sum -c works.
	paths := map[string]bool{}
	for _, d := range res.Digests {
		paths[d.Path] = true
	}
	if !paths["readme~repopsy-1"] || !paths[".git~repopsy/config"] || !paths["line_break.txt~repopsy"] || paths["../escape.txt"] {
		t.Errorf("digest paths = %v", paths)
	}
}

func TestExtractNoTreeWritesNothingButHashesTheSame(t *testing.T) {
	b := newRepo(t)
	b.Write("a.txt", "alpha\n", 0o644)
	b.Write("bin/run", "#!/bin/sh\n", 0o755)
	hash := b.Commit("content")

	_, full := extractInto(t, b, hash, ExtractOptions{})

	dest := filepath.Join(t.TempDir(), "tree")
	cache := NewDigestCache()
	res, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{NoTree: true, Digests: cache})
	if err != nil {
		t.Fatalf("Extract: %v", err)
	}
	if _, err := os.Stat(dest); !os.IsNotExist(err) {
		t.Errorf("--no-tree created the tree directory (err %v)", err)
	}
	if !res.Tree.NotWritten {
		t.Error("NotWritten not set")
	}
	if fmt.Sprint(res.Digests) != fmt.Sprint(full.Digests) {
		t.Errorf("digests differ:\nno-tree %v\nfull    %v", res.Digests, full.Digests)
	}
	want := sha256.Sum256([]byte("alpha\n"))
	if res.Digests[0].SHA256 != hex.EncodeToString(want[:]) {
		t.Errorf("digest of a.txt = %s", res.Digests[0].SHA256)
	}

	// A second commit sharing the blobs is served from the cache.
	again, err := b.open().Extract(context.Background(), hash, dest, ExtractOptions{NoTree: true, Digests: cache})
	if err != nil || fmt.Sprint(again.Digests) != fmt.Sprint(full.Digests) {
		t.Errorf("cached extraction = %v, %v", again.Digests, err)
	}
}

func TestPathFilter(t *testing.T) {
	tests := []struct {
		pattern string
		match   []string
		miss    []string
	}{
		{"src", []string{"src", "src/a.go", "src/x/y.go"}, []string{"srcs/a.go", "lib/src/a.go"}},
		{"./src/", []string{"src/a.go"}, []string{"other"}},
		{"*.go", []string{"a.go", "x/y/z.go"}, []string{"a.gox", "a.md"}},
		{"cmd/*/main.go", []string{"cmd/x/main.go", "cmd/x/y/main.go"}, []string{"cmd/main.go"}},
		{"file?.txt", []string{"file1.txt"}, []string{"file10.txt"}},
		{"[ab].txt", []string{"a.txt", "b.txt"}, []string{"c.txt"}},
		{"[!ab].txt", []string{"c.txt"}, []string{"a.txt"}},
		{"a+b(c).txt", []string{"a+b(c).txt"}, []string{"aab(c).txt"}},
	}
	for _, tc := range tests {
		f, err := NewPathFilter([]string{tc.pattern})
		if err != nil {
			t.Fatalf("%s: %v", tc.pattern, err)
		}
		for _, p := range tc.match {
			if !f.Match(p) {
				t.Errorf("%q should match %q", tc.pattern, p)
			}
		}
		for _, p := range tc.miss {
			if f.Match(p) {
				t.Errorf("%q should not match %q", tc.pattern, p)
			}
		}
	}
	for _, bad := range []string{"", "/", "a[b"} {
		if _, err := NewPathFilter([]string{bad}); err == nil {
			t.Errorf("pattern %q accepted", bad)
		}
	}
}
