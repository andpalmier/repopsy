package console

import (
	"errors"
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite the golden files in testdata")

// assertGolden compares got against testdata/<name>, rewriting it under -update.
func assertGolden(t *testing.T, name, got string) {
	t.Helper()
	path := filepath.Join("testdata", name)

	if *update {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatal(err)
		}
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading golden (run: go test ./internal/console -update): %v", err)
	}
	if got != string(want) {
		t.Errorf("output does not match %s\n--- got ---\n%s\n--- want ---\n%s", path, got, want)
	}
}

// newTestConsole returns a Console over a buffer. A strings.Builder is not an
// *os.File, so colour is off and the output is deterministic by construction
// rather than by whatever go test does with stdout.
func newTestConsole() (*Console, *strings.Builder) {
	var buf strings.Builder
	return New(&buf), &buf
}

// ── block layouts: pinned byte-for-byte, since alignment is the point ──

func TestBannerAllBranches(t *testing.T) {
	c, buf := newTestConsole()
	c.Banner(Header{RepoPath: "/repos/demo", OutputDir: "/out/demo-exploded", Workers: 8})
	assertGolden(t, "banner-all-branches.golden", buf.String())
}

func TestBannerSingleBranchWithLimit(t *testing.T) {
	c, buf := newTestConsole()
	c.Banner(Header{
		RepoPath: "/repos/demo", Branch: "main",
		OutputDir: "/out/demo-exploded", Workers: 4, Limit: 25,
	})
	assertGolden(t, "banner-single-branch.golden", buf.String())
}

func TestSummaryAllSucceeded(t *testing.T) {
	c, buf := newTestConsole()
	c.Summary("/out/demo-exploded", []Outcome{{ShortHash: "aaa1111"}, {ShortHash: "bbb2222"}}, false)
	assertGolden(t, "summary-success.golden", buf.String())
}

func TestSummaryWithFailuresVerbose(t *testing.T) {
	c, buf := newTestConsole()
	c.Summary("/out/demo-exploded", []Outcome{
		{ShortHash: "aaa1111"},
		{ShortHash: "bbb2222", Err: errors.New("git archive failed")},
		{ShortHash: "ccc3333", Err: errors.New("tar extraction failed")},
	}, true)
	assertGolden(t, "summary-failures-verbose.golden", buf.String())
}

func TestSummaryWithFailuresQuiet(t *testing.T) {
	c, buf := newTestConsole()
	c.Summary("/out/demo-exploded", []Outcome{
		{ShortHash: "aaa1111"},
		{ShortHash: "bbb2222", Err: errors.New("git archive failed")},
	}, false)

	out := buf.String()
	if !strings.Contains(out, "1 succeeded, 1 failed") {
		t.Errorf("missing the counts: %q", out)
	}
	// The per-commit list is verbose-only.
	if strings.Contains(out, "git archive failed") {
		t.Errorf("failure detail leaked into non-verbose output: %q", out)
	}
}

func TestSummaryOfNothing(t *testing.T) {
	c, buf := newTestConsole()
	c.Summary("/out/demo-exploded", nil, false)

	out := buf.String()
	if strings.Contains(out, "Completed with errors") {
		t.Errorf("reported errors for an empty run: %q", out)
	}
	if !strings.Contains(out, "Output: /out/demo-exploded") {
		t.Errorf("missing the output location: %q", out)
	}
}

// ── one-line messages: the caller supplies no layout, so assert the layout ──

func TestOneLineMessages(t *testing.T) {
	tests := []struct {
		name string
		emit func(*Console)
		want string
	}{
		{
			name: "branches found warns about cost",
			emit: func(c *Console) { c.BranchesFound(7) },
			want: "⚠ Extracting from 7 local branches - this may take some time and memory!\n\n",
		},
		{
			name: "branch started is numbered",
			emit: func(c *Console) { c.BranchStarted(2, 5, "feat/x") },
			want: "Branch [2/5]: feat/x\n",
		},
		{
			name: "branch list failure names the branch",
			emit: func(c *Console) { c.BranchListFailed("feat/x", errors.New("bad object")) },
			want: "⚠ Failed to list commits on feat/x: bad object\n",
		},
		{
			// The two-space indent lives here, not at the call site.
			name: "empty branch is indented",
			emit: func(c *Console) { c.BranchEmpty() },
			want: "  (no commits)\n",
		},
		{
			name: "branch commit count is indented",
			emit: func(c *Console) { c.BranchCommits(41) },
			want: "  Found 41 commits\n",
		},
		{
			name: "single-branch commit count is not indented",
			emit: func(c *Console) { c.CommitsToExtract(41) },
			want: "Found 41 commits to extract\n\n",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, buf := newTestConsole()
			tc.emit(c)
			if got := buf.String(); got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

// ── colour follows the stream being written to ──

func TestColourOffForNonTerminal(t *testing.T) {
	c, buf := newTestConsole()
	c.Banner(Header{RepoPath: "/r", OutputDir: "/o", Workers: 1})
	c.BranchesFound(2)
	c.Summary("/o", []Outcome{{ShortHash: "a", Err: errors.New("x")}}, false)

	if strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("ANSI escapes written to a non-terminal:\n%q", buf.String())
	}
}

func TestColourOnWhenEnabled(t *testing.T) {
	// Constructed directly: a pty is the only way New would turn colour on, and
	// what matters here is that tint honours the field rather than
	// fatih/color's process-wide guess.
	var buf strings.Builder
	c := &Console{w: &buf, color: true}
	c.BranchesFound(1)

	if !strings.Contains(buf.String(), "\x1b[") {
		t.Errorf("expected ANSI escapes when colour is enabled: %q", buf.String())
	}
	if !strings.Contains(buf.String(), "⚠") {
		t.Errorf("marker missing: %q", buf.String())
	}
}

func TestSupportsColor(t *testing.T) {
	if supportsColor(&strings.Builder{}) {
		t.Error("a strings.Builder is not a terminal")
	}
	// A regular file is an *os.File but not a terminal — this is the case that
	// used to get raw escapes written into it.
	f, err := os.CreateTemp(t.TempDir(), "out")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if supportsColor(f) {
		t.Error("a regular file is not a terminal")
	}
}

func TestNewDefaultsToStderr(t *testing.T) {
	if got := New(nil); got.w != os.Stderr {
		t.Errorf("New(nil) writes to %v, want os.Stderr", got.w)
	}
}

// ── progress ──

func TestProgressWritesToItsWriter(t *testing.T) {
	var buf strings.Builder
	p := NewProgress(&buf, 2, true)
	p.Done("aaa1111", "/out/main/20231205_143022_aaa1111")
	p.Failed("bbb2222", errors.New("boom"))
	p.Finish()

	out := buf.String()
	for _, want := range []string{
		"✓ aaa1111 → 20231205_143022_aaa1111", // path shortened by the console
		"✗ bbb2222: boom",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %q in:\n%q", want, out)
		}
	}
}

func TestProgressQuietOmitsPerCommitLines(t *testing.T) {
	var buf strings.Builder
	p := NewProgress(&buf, 1, false)
	p.Done("aaa1111", "/out/x/20231205_143022_aaa1111")
	p.Finish()

	if strings.Contains(buf.String(), "✓") {
		t.Errorf("per-commit line printed without verbose: %q", buf.String())
	}
}

// TestProgressNoEscapesForNonTerminal is the defect this candidate fixed:
// colour codes were enabled unconditionally, so "repopsy . 2> log" wrote raw
// ANSI into the file.
func TestProgressNoEscapesForNonTerminal(t *testing.T) {
	var buf strings.Builder
	p := NewProgress(&buf, 2, false)
	p.Done("a", "/out/a")
	p.Done("b", "/out/b")
	p.Finish()

	out := buf.String()
	if strings.Contains(out, "\x1b[") {
		t.Errorf("ANSI escapes written to a non-terminal: %q", out)
	}
	// And the markup must not leak through as literal text either.
	for _, leak := range []string{"[green]", "[cyan]", "[reset]"} {
		if strings.Contains(out, leak) {
			t.Errorf("colour markup leaked as literal text (%s): %q", leak, out)
		}
	}
}
