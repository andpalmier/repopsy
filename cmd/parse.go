package cmd

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/anomaly"
	"github.com/andpalmier/repopsy/v2/internal/app"
)

// options is the fully-resolved result of parsing a command line.
type options struct {
	cfg         app.Config
	showVersion bool
	showHelp    bool
}

// stringList is a repeatable string flag.
type stringList []string

func (l *stringList) String() string { return strings.Join(*l, ", ") }

func (l *stringList) Set(v string) error {
	*l = append(*l, v)
	return nil
}

// days is a duration flag that also accepts a whole number of days, "30d",
// since that is the scale date skew is reasoned about at.
type days struct{ d *time.Duration }

func (f days) String() string {
	if f.d == nil {
		return ""
	}
	if *f.d%(24*time.Hour) == 0 {
		return strconv.Itoa(int(*f.d/(24*time.Hour))) + "d"
	}
	return f.d.String()
}

func (f days) Set(v string) error {
	if n, ok := strings.CutSuffix(v, "d"); ok {
		count, err := strconv.Atoi(n)
		if err != nil || count < 0 {
			return fmt.Errorf("invalid number of days %q", v)
		}
		*f.d = time.Duration(count) * 24 * time.Hour
		return nil
	}
	d, err := time.ParseDuration(v)
	if err != nil {
		return err
	}
	*f.d = d
	return nil
}

// alias registers the same target under both a short and a long flag name, so
// the help text for a flag lives in exactly one place.
func alias[T any](reg func(*T, string, T, string), p *T, short, long string, def T, help string) {
	reg(p, short, def, help)
	reg(p, long, def, help)
}

// defaultWorkers is the worker count used when none is given, or when the given
// one is unusable. Never above maxWorkers, so the cap warning can only be
// triggered by an explicit request.
func defaultWorkers() int {
	return min(runtime.NumCPU(), maxWorkers)
}

// newFlagSet builds the flag set writing into o. Both parseArgs and printUsage
// use it, so flag names and help strings have a single source of truth.
// Output is discarded and Usage suppressed: callers report errors themselves.
func newFlagSet(o *options) *flag.FlagSet {
	fs := flag.NewFlagSet(appName, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Usage = func() {}

	alias(fs.StringVar, &o.cfg.OutputDir, "o", "output", "", "Output directory (default: ./<repo-name>-exploded)")
	alias(fs.IntVar, &o.cfg.Workers, "w", "workers", defaultWorkers(), fmt.Sprintf("Number of parallel workers per branch (max %d)", maxWorkers))
	alias(fs.IntVar, &o.cfg.Limit, "n", "limit", 0, "Maximum number of commits to extract (0 = all)")
	alias(fs.StringVar, &o.cfg.Branch, "b", "branch", "", "Branch to extract from (default: all local branches)")
	alias(fs.BoolVar, &o.cfg.Verbose, "v", "verbose", false, "Show detailed output per commit")
	fs.BoolVar(&o.cfg.IncludeRewritten, "include-rewritten", false,
		"Also extract commits recovered from the reflog that no branch reaches")
	fs.Var((*stringList)(&o.cfg.Refs), "refs",
		"Extract refs matching `PATTERN` instead of local branches (repeatable), e.g. 'refs/pull/*/head'")
	fs.BoolVar(&o.cfg.AllRefs, "all-refs", false, "Extract every ref except refs/stash and refs/notes")
	fs.BoolVar(&o.cfg.Dedupe, "dedupe", false,
		"Store each commit once under commits/ and link ref directories to it")
	fs.BoolVar(&o.cfg.PreserveModes, "preserve-modes", false,
		"Keep executable bits on extracted files (off: tree/ is never executable)")
	fs.BoolVar(&o.cfg.NoTree, "no-tree", false, "Write the records and SHA256SUMS but no tree/")
	fs.Var((*stringList)(&o.cfg.Paths), "paths",
		"Extract only paths matching `PATTERN` into tree/ (repeatable): a directory, a file, or a glob")
	fs.BoolVar(&o.cfg.DryRun, "dry-run", false, "Report commits per ref and the estimated size, write nothing")
	fs.BoolVar(&o.cfg.JSON, "json", false, "Also write each record as JSON")
	fs.StringVar(&o.cfg.AllowedSigners, "allowed-signers", "", "Verify SSH signatures against this allowed signers `FILE`")
	fs.IntVar(&o.cfg.Thresholds.LargeFiles, "large-commit-files", anomaly.DefaultThresholds.LargeFiles,
		"Flag commits changing at least this many files (0 = off)")
	fs.IntVar(&o.cfg.Thresholds.LargeLines, "large-commit-lines", anomaly.DefaultThresholds.LargeLines,
		"Flag commits adding at least this many lines (0 = off)")
	o.cfg.Thresholds.DateSkew = anomaly.DefaultThresholds.DateSkew
	fs.Var(days{&o.cfg.Thresholds.DateSkew}, "date-skew",
		"Flag author/committer date gaps above this `DURATION`, e.g. 30d or 72h (0 = off)")
	fs.Var((*stringList)(&o.cfg.Track), "track",
		"Record where `STRING` first appears and every commit changing its count in STRINGS.txt (repeatable)")
	fs.BoolVar(&o.cfg.ScanSecrets, "scan-secrets", false,
		"Record credential-shaped strings the history adds in SECRETS.txt, masked, never in full")
	alias(fs.BoolVar, &o.showHelp, "h", "help", false, "Show help message")
	fs.BoolVar(&o.showVersion, "version", false, "Show version information")

	return fs
}

// parseArgs turns an argument list into resolved options. Warnings about
// adjusted values are written to warn; nothing else is printed.
func parseArgs(args []string, warn io.Writer) (options, error) {
	var o options
	fs := newFlagSet(&o)

	if err := fs.Parse(args); err != nil {
		return options{}, err
	}

	// Help and version short-circuit before the repository path is required.
	if o.showHelp || o.showVersion {
		return o, nil
	}

	rest := fs.Args()
	if len(rest) == 0 {
		return options{}, errors.New("repository path is required")
	}
	o.cfg.RepoPath = rest[0]

	if err := validate(o.cfg); err != nil {
		return options{}, err
	}

	// Resolve the worker count here rather than downstream, so the value the
	// header reports is the value that is actually used.
	switch {
	case o.cfg.Workers > maxWorkers:
		_, _ = fmt.Fprintf(warn, "⚠ Workers capped at %d (requested %d)\n", maxWorkers, o.cfg.Workers)
		o.cfg.Workers = maxWorkers
	case o.cfg.Workers < 1:
		_, _ = fmt.Fprintf(warn, "⚠ Workers must be at least 1, using %d\n", defaultWorkers())
		o.cfg.Workers = defaultWorkers()
	}

	return o, nil
}

// validate refuses combinations of flags that cannot mean anything.
func validate(cfg app.Config) error {
	switch {
	case cfg.Branch != "" && (len(cfg.Refs) > 0 || cfg.AllRefs):
		return errors.New("-b names one branch; it cannot be combined with --refs or --all-refs")
	case len(cfg.Refs) > 0 && cfg.AllRefs:
		return errors.New("--refs and --all-refs are alternatives; give one")
	case cfg.Branch != "" && cfg.Dedupe:
		return errors.New("--dedupe stores a commit once across refs; with -b there is only one")
	case cfg.NoTree && cfg.PreserveModes:
		return errors.New("--preserve-modes has no effect with --no-tree")
	case cfg.Thresholds.LargeFiles < 0 || cfg.Thresholds.LargeLines < 0 || cfg.Thresholds.DateSkew < 0:
		return errors.New("anomaly thresholds cannot be negative")
	}
	for _, s := range cfg.Track {
		if s == "" || strings.ContainsAny(s, "\n\r") {
			return fmt.Errorf("--track needs a single non-empty line, got %q", s)
		}
	}
	return nil
}
