package cmd

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/internal/gittest"
	"github.com/andpalmier/repopsy/internal/snapshot"
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

	snapshots, err := filepath.Glob(filepath.Join(outDir, "*", "*", snapshot.MetadataFilename))
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
