package cmd

import (
	"io"
	"strings"
	"testing"
)

// want describes the resolved options a parse is expected to produce.
type want struct {
	repoPath  string
	outputDir string
	workers   int
	limit     int
	branch    string
	verbose   bool
	help      bool
	version   bool
}

func TestParseArgs(t *testing.T) {
	def := defaultWorkers()

	tests := []struct {
		name     string
		args     []string
		wantErr  string // substring; empty means no error expected
		wantWarn string // substring; empty means no warning expected
		want     want
	}{
		{
			name: "path only uses default workers",
			args: []string{"."},
			want: want{repoPath: ".", workers: def},
		},
		{
			name: "short flags",
			args: []string{"-o", "out", "-w", "4", "-n", "5", "-b", "main", "-v", "/repo"},
			want: want{repoPath: "/repo", outputDir: "out", workers: 4, limit: 5, branch: "main", verbose: true},
		},
		{
			name: "long flags",
			args: []string{"--output", "out", "--workers", "4", "--limit", "5", "--branch", "main", "--verbose", "/repo"},
			want: want{repoPath: "/repo", outputDir: "out", workers: 4, limit: 5, branch: "main", verbose: true},
		},
		{
			name:     "workers above the cap are clamped with a warning",
			args:     []string{"-w", "5000", "."},
			wantWarn: "capped at 32",
			want:     want{repoPath: ".", workers: maxWorkers},
		},
		{
			name:     "long form workers are clamped too",
			args:     []string{"--workers", "5000", "."},
			wantWarn: "capped at 32",
			want:     want{repoPath: ".", workers: maxWorkers},
		},
		{
			name: "workers exactly at the cap is not a warning",
			args: []string{"-w", "32", "."},
			want: want{repoPath: ".", workers: maxWorkers},
		},
		{
			name:     "zero workers falls back with a warning",
			args:     []string{"-w", "0", "."},
			wantWarn: "at least 1",
			want:     want{repoPath: ".", workers: def},
		},
		{
			name:     "negative workers falls back with a warning",
			args:     []string{"-w", "-3", "."},
			wantWarn: "at least 1",
			want:     want{repoPath: ".", workers: def},
		},
		{
			name:    "missing repository path is an error",
			args:    []string{},
			wantErr: "repository path is required",
		},
		{
			name:    "missing path with flags present is still an error",
			args:    []string{"-v"},
			wantErr: "repository path is required",
		},
		{
			name:    "unknown flag is an error",
			args:    []string{"-bogus", "."},
			wantErr: "not defined",
		},
		{
			name:    "non-numeric worker count is an error",
			args:    []string{"-w", "many", "."},
			wantErr: "invalid value",
		},
		{
			name: "help short-circuits before the path check",
			args: []string{"-h"},
			want: want{help: true, workers: def},
		},
		{
			name: "long help short-circuits too",
			args: []string{"--help"},
			want: want{help: true, workers: def},
		},
		{
			name: "version short-circuits before the path check",
			args: []string{"--version"},
			want: want{version: true, workers: def},
		},
		{
			// Documents Go's flag behaviour: parsing stops at the first
			// non-flag argument, so trailing flags are positional and ignored.
			name: "flags after the path are not parsed as flags",
			args: []string{".", "-v"},
			want: want{repoPath: ".", workers: def},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var warn strings.Builder
			opts, err := parseArgs(tc.args, &warn)

			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("expected error containing %q, got nil", tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("expected error containing %q, got %q", tc.wantErr, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if tc.wantWarn == "" {
				if warn.Len() != 0 {
					t.Errorf("expected no warning, got %q", warn.String())
				}
			} else if !strings.Contains(warn.String(), tc.wantWarn) {
				t.Errorf("expected warning containing %q, got %q", tc.wantWarn, warn.String())
			}

			got := want{
				repoPath:  opts.cfg.RepoPath,
				outputDir: opts.cfg.OutputDir,
				workers:   opts.cfg.Workers,
				limit:     opts.cfg.Limit,
				branch:    opts.cfg.Branch,
				verbose:   opts.cfg.Verbose,
				help:      opts.showHelp,
				version:   opts.showVersion,
			}
			if got != tc.want {
				t.Errorf("got  %+v\nwant %+v", got, tc.want)
			}
		})
	}
}

// TestParseArgsIsRepeatable is the point of parsing into a local flag set:
// with flags on package-level globals, a second call saw the first call's
// values and re-registering panicked.
func TestParseArgsIsRepeatable(t *testing.T) {
	first, err := parseArgs([]string{"-v", "-n", "7", "/a"}, io.Discard)
	if err != nil {
		t.Fatalf("first parse: %v", err)
	}

	second, err := parseArgs([]string{"/b"}, io.Discard)
	if err != nil {
		t.Fatalf("second parse: %v", err)
	}

	if second.cfg.Verbose {
		t.Error("second parse inherited Verbose from the first")
	}
	if second.cfg.Limit != 0 {
		t.Errorf("second parse inherited Limit %d from the first", second.cfg.Limit)
	}
	if second.cfg.RepoPath != "/b" {
		t.Errorf("second parse got RepoPath %q, want /b", second.cfg.RepoPath)
	}
	if !first.cfg.Verbose || first.cfg.Limit != 7 || first.cfg.RepoPath != "/a" {
		t.Errorf("second parse mutated the first result: %+v", first.cfg)
	}
}

func TestDefaultWorkersNeverExceedsCap(t *testing.T) {
	if got := defaultWorkers(); got > maxWorkers || got < 1 {
		t.Errorf("defaultWorkers() = %d, want between 1 and %d", got, maxWorkers)
	}
}

func TestPrintUsageListsFlags(t *testing.T) {
	var buf strings.Builder
	printUsage(&buf)

	out := buf.String()
	for _, want := range []string{"repopsy", "Usage:", "-workers", "-output", "-branch", "max 32"} {
		if !strings.Contains(out, want) {
			t.Errorf("usage output missing %q", want)
		}
	}
}

func TestPrintVersionOmitsUnsetFields(t *testing.T) {
	var full strings.Builder
	printVersion(&full, "1.2.3", "abc1234", "2026-01-01")
	for _, want := range []string{"1.2.3", "abc1234", "2026-01-01"} {
		if !strings.Contains(full.String(), want) {
			t.Errorf("version output missing %q", want)
		}
	}

	var bare strings.Builder
	printVersion(&bare, "dev", "none", "unknown")
	if strings.Contains(bare.String(), "commit:") || strings.Contains(bare.String(), "built:") {
		t.Errorf("expected placeholder fields to be omitted, got %q", bare.String())
	}
}
