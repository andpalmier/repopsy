package cmd

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/gittest"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

var testBuild = build{version: "1.2.3", commit: "abc1234", date: "2023-12-05T12:00:00Z"}

// invoke runs the CLI and returns its exit code and both streams.
func invoke(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var stdout, stderr strings.Builder
	code := run(args, &stdout, &stderr, testBuild)
	return code, stdout.String(), stderr.String()
}

func TestRunHelp(t *testing.T) {
	for _, flag := range []string{"-h", "--help"} {
		code, stdout, stderr := invoke(t, flag)
		if code != 0 {
			t.Errorf("%s exited %d, want 0", flag, code)
		}
		// Help is requested output, so it belongs on stdout.
		if !strings.Contains(stdout, "Usage:") || !strings.Contains(stdout, "repopsy") {
			t.Errorf("%s did not print usage to stdout: %q", flag, stdout)
		}
		if stderr != "" {
			t.Errorf("%s wrote to stderr: %q", flag, stderr)
		}
	}
}

func TestRunVersion(t *testing.T) {
	code, stdout, stderr := invoke(t, "--version")
	if code != 0 {
		t.Errorf("exit %d, want 0", code)
	}
	for _, want := range []string{"1.2.3", "abc1234", "2023-12-05T12:00:00Z"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("version output missing %q: %q", want, stdout)
		}
	}
	if stderr != "" {
		t.Errorf("wrote to stderr: %q", stderr)
	}
}

func TestRunUsageErrors(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{name: "no arguments", args: nil, want: "repository path is required"},
		{name: "flags but no path", args: []string{"-v"}, want: "repository path is required"},
		{name: "unknown flag", args: []string{"-bogus", "."}, want: "not defined"},
		{name: "non-numeric workers", args: []string{"-w", "many", "."}, want: "invalid value"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, stdout, stderr := invoke(t, tc.args...)
			if code != 1 {
				t.Errorf("exit %d, want 1", code)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr missing %q: %q", tc.want, stderr)
			}
			// Diagnostics belong on stderr so stdout stays pipeable.
			if stdout != "" {
				t.Errorf("wrote a diagnostic to stdout: %q", stdout)
			}
			// The usage text follows the error, so the user can act on it.
			if !strings.Contains(stderr, "Usage:") {
				t.Errorf("stderr did not include usage: %q", stderr)
			}
		})
	}
}

func TestRunExplodesARepository(t *testing.T) {
	repo := gittest.New(t).WithCommits(3)
	outDir := filepath.Join(t.TempDir(), "out")

	code, stdout, stderr := invoke(t, "-o", outDir, repo.Dir)
	if code != 0 {
		t.Fatalf("exit %d, want 0\nstderr: %s", code, stderr)
	}
	if stdout != "" {
		t.Errorf("progress belongs on stderr, not stdout: %q", stdout)
	}
	// The banner and summary reached the caller's stream rather than the
	// process's, which is what makes this assertable at all.
	if !strings.Contains(stderr, "repopsy") || !strings.Contains(stderr, "Output:") {
		t.Errorf("stderr missing the banner or summary: %q", stderr)
	}

	snapshots, err := filepath.Glob(filepath.Join(outDir, snapshot.RefsDir, "*", "*", snapshot.MetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 3 {
		t.Errorf("got %d snapshots, want 3", len(snapshots))
	}

	// Every root record is written.
	for _, name := range []string{"EXTRACTION.txt", "REFLOG.txt", "TAGS.txt", "IDENTITIES.txt", "REPOSITORY.txt"} {
		if _, err := os.Stat(filepath.Join(outDir, name)); err != nil {
			t.Errorf("missing %s: %v", name, err)
		}
	}
}

func TestRunNoColourWhenOutputIsCaptured(t *testing.T) {
	repo := gittest.New(t).WithCommits(1)
	_, _, stderr := invoke(t, "-o", filepath.Join(t.TempDir(), "out"), repo.Dir)

	// A strings.Builder is not a terminal, so nothing should emit escapes —
	// neither the banner nor the progress bar.
	if strings.Contains(stderr, "\x1b[") {
		t.Errorf("ANSI escapes written to a non-terminal: %q", stderr)
	}
	for _, markup := range []string{"[green]", "[cyan]", "[reset]"} {
		if strings.Contains(stderr, markup) {
			t.Errorf("colour markup leaked as literal text (%s): %q", markup, stderr)
		}
	}
}

func TestRunSingleBranchIsFlat(t *testing.T) {
	repo := gittest.New(t).WithCommits(2)
	outDir := filepath.Join(t.TempDir(), "out")

	code, _, stderr := invoke(t, "-b", repo.Branch(), "-o", outDir, repo.Dir)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr)
	}

	// Naming a branch means no branch directory.
	snapshots, err := filepath.Glob(filepath.Join(outDir, "*", snapshot.MetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 2 {
		t.Errorf("got %d snapshots directly under the output directory, want 2", len(snapshots))
	}
}

func TestRunFailures(t *testing.T) {
	repo := gittest.New(t).WithCommits(1)

	tests := []struct {
		name string
		args func(t *testing.T) []string
		want string
	}{
		{
			name: "path is not a repository",
			args: func(t *testing.T) []string {
				return []string{"-o", filepath.Join(t.TempDir(), "out"), t.TempDir()}
			},
			want: "not a git repository",
		},
		{
			name: "path does not exist",
			args: func(t *testing.T) []string {
				return []string{"-o", filepath.Join(t.TempDir(), "out"), filepath.Join(t.TempDir(), "nope")}
			},
			want: "failed to access path",
		},
		{
			name: "output directory already exists",
			args: func(t *testing.T) []string {
				existing := t.TempDir() // TempDir already exists
				return []string{"-o", existing, repo.Dir}
			},
			want: "output directory already exists",
		},
		{
			name: "unknown branch",
			args: func(t *testing.T) []string {
				return []string{"-b", "no-such-branch", "-o", filepath.Join(t.TempDir(), "out"), repo.Dir}
			},
			want: "failed to list commits",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			code, _, stderr := invoke(t, tc.args(t)...)
			if code != 1 {
				t.Errorf("exit %d, want 1", code)
			}
			if !strings.Contains(stderr, tc.want) {
				t.Errorf("stderr missing %q: %q", tc.want, stderr)
			}
		})
	}
}

func TestRunWorkerCapWarningReachesTheUser(t *testing.T) {
	repo := gittest.New(t).WithCommits(1)

	code, _, stderr := invoke(t, "-w", "5000", "-o", filepath.Join(t.TempDir(), "out"), repo.Dir)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "capped at 32") {
		t.Errorf("the cap warning did not reach the user: %q", stderr)
	}
	// And the header reports the value actually used.
	if !strings.Contains(stderr, "Workers:     32") {
		t.Errorf("header did not report the clamped worker count: %q", stderr)
	}
}

func TestRunIsRepeatable(t *testing.T) {
	repo := gittest.New(t).WithCommits(1)

	// Two invocations in one process: flags live on a local FlagSet, and signal
	// handlers are stopped on return, so nothing leaks between runs.
	for i := range 2 {
		code, _, stderr := invoke(t, "-o", filepath.Join(t.TempDir(), "out"), repo.Dir)
		if code != 0 {
			t.Fatalf("invocation %d exited %d\nstderr: %s", i, code, stderr)
		}
	}
}

// TestRunRepositoryFilesNeverOverwriteRecords guards a bug that destroyed
// evidence: a commit may legitimately contain a file called COMMIT_INFO.txt or
// SHA256SUMS, and repopsy used to write its own record over it, then publish an
// integrity record that no longer matched the file it described.
func TestRunRepositoryFilesNeverOverwriteRecords(t *testing.T) {
	repo := gittest.New(t)
	const ownContent = "THE REPOSITORY'S OWN FILE\n"
	repo.Write(snapshot.MetadataFilename, ownContent, 0o644)
	repo.Write(snapshot.ChecksumFilename, "the repository's own sums\n", 0o644)
	repo.Write("other.txt", "ordinary\n", 0o644)
	repo.Commit("files named like repopsy's records")

	outDir := filepath.Join(t.TempDir(), "out")
	if code, _, stderr := invoke(t, "-b", repo.Branch(), "-o", outDir, repo.Dir); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr)
	}

	snaps, err := filepath.Glob(filepath.Join(outDir, "*", snapshot.MetadataFilename))
	if err != nil || len(snaps) != 1 {
		t.Fatalf("expected one snapshot, got %v (%v)", snaps, err)
	}
	snapDir := filepath.Dir(snaps[0])

	// repopsy's record is the record.
	record, err := os.ReadFile(filepath.Join(snapDir, snapshot.MetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(record), "COMMIT INFORMATION") {
		t.Errorf("the snapshot's record was displaced by repository content: %q", string(record)[:40])
	}

	// And the repository's own file survives, byte for byte, inside the tree.
	own, err := os.ReadFile(filepath.Join(snapshot.TreePath(snapDir), snapshot.MetadataFilename))
	if err != nil {
		t.Fatalf("the repository's own file was lost: %v", err)
	}
	if string(own) != ownContent {
		t.Errorf("the repository's own file = %q, want %q", own, ownContent)
	}

	// The integrity record must describe what is actually on disk. It used to
	// claim hashes for files it had overwritten.
	sums, err := os.ReadFile(filepath.Join(snapDir, snapshot.ChecksumFilename))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(sums)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 checksummed files, got %d: %q", len(lines), sums)
	}
	for _, line := range lines {
		want, path, found := strings.Cut(line, "  ")
		if !found {
			t.Errorf("malformed line %q", line)
			continue
		}
		if !strings.HasPrefix(path, snapshot.TreeDir+"/") {
			t.Errorf("checksum path %q is not inside the tree", path)
		}
		content, err := os.ReadFile(filepath.Join(snapDir, path))
		if err != nil {
			t.Errorf("%s: %v", path, err)
			continue
		}
		sum := sha256.Sum256(content)
		if got := hex.EncodeToString(sum[:]); got != want {
			t.Errorf("%s: recorded %s, actual %s", path, want, got)
		}
	}
}

// TestRunBranchNamesNeverDisplaceRootRecords guards the same class one level up:
// git permits a branch called EXTRACTION.txt, whose directory used to take the
// manifest's place and leave the run with no provenance at all.
func TestRunBranchNamesNeverDisplaceRootRecords(t *testing.T) {
	repo := gittest.New(t).WithCommits(1)
	records := []string{"EXTRACTION.txt", "REFLOG.txt", "TAGS.txt", "IDENTITIES.txt", "REPOSITORY.txt"}
	for _, name := range records {
		repo.Git("branch", name)
	}

	outDir := filepath.Join(t.TempDir(), "out")
	if code, _, stderr := invoke(t, "-o", outDir, repo.Dir); code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr)
	}

	for _, name := range records {
		// The record is a file with content.
		info, err := os.Stat(filepath.Join(outDir, name))
		if err != nil {
			t.Errorf("%s missing from the output root: %v", name, err)
			continue
		}
		if info.IsDir() {
			t.Errorf("%s is a directory — a branch displaced the record", name)
			continue
		}
		if info.Size() == 0 {
			t.Errorf("%s is empty", name)
		}

		// And the identically named branch still got its snapshots.
		snaps, err := filepath.Glob(filepath.Join(outDir, snapshot.RefsDir, name, "*", snapshot.MetadataFilename))
		if err != nil || len(snaps) == 0 {
			t.Errorf("branch %s produced no snapshots under %s/", name, snapshot.RefsDir)
		}
	}
}

// TestRunUnwritableOutputFailsFast covers how a CI failure once
// presented: an unwritable output directory was only discovered once extraction
// was under way, so one cause produced a message per snapshot and per root
// record, after the run had already spent its time.
func TestRunUnwritableOutputFailsFast(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}

	repo := gittest.New(t).WithCommits(3)

	parent := t.TempDir()
	if err := os.Chmod(parent, 0o500); err != nil { // readable, not writable
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(parent, 0o700) })

	code, _, stderr := invoke(t, "-o", filepath.Join(parent, "out"), repo.Dir)
	if code != 1 {
		t.Fatalf("exit %d, want 1\nstderr: %s", code, stderr)
	}
	if !strings.Contains(stderr, "cannot create output directory") {
		t.Errorf("stderr does not name the cause: %q", stderr)
	}

	// One cause, reported once. Nothing should have been extracted first.
	if n := strings.Count(stderr, "permission denied"); n != 1 {
		t.Errorf("reported the same cause %d times, want 1:\n%s", n, stderr)
	}
	// Markers that only appear once work has begun. Chosen so they cannot occur
	// inside a temporary path: the test name itself once matched "Extracting".
	for _, absent := range []string{"% [", "Found ", "extractions failed", "Failed to write"} {
		if strings.Contains(stderr, absent) {
			t.Errorf("work started before the destination was checked (%q present):\n%s", absent, stderr)
		}
	}
}
