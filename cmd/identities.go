package cmd

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/andpalmier/repopsy/v2/internal/identity"
)

const (
	identitiesCommand = "identities"

	identitiesUsage = `repopsy identities - identities shared between exploded repositories

Reads the IDENTITIES records of several exploded repositories and reports every
identity that appears in more than one: by email, compared case-insensitively,
and by GitHub account, read from <id>+<handle>@users.noreply.github.com
addresses, so one account is matched however its email was written.

Automation that commits everywhere is ignored by default: ` + "%s" + `.
Patterns match the name, email or GitHub handle, case-insensitively, and * is
the only wildcard.

Usage:
  repopsy identities [flags] <exploded-dir> <exploded-dir>...

Flags:
`
)

// runIdentities is the identities subcommand. Its result is the requested
// output, so it goes to stdout.
func runIdentities(args []string, stdout, stderr io.Writer) int {
	var ignore []string
	var noDefault, asJSON, help bool
	fs := flag.NewFlagSet(identitiesCommand, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	fs.Var((*stringList)(&ignore), "ignore", "Also ignore identities matching `PATTERN` (repeatable)")
	fs.BoolVar(&noDefault, "no-default-ignore", false, "Do not ignore the default list of bots")
	fs.BoolVar(&asJSON, "json", false, "Write JSON instead of text")
	alias(fs.BoolVar, &help, "h", "help", false, "Show help message")

	usage := func(w io.Writer) {
		_, _ = fmt.Fprintf(w, identitiesUsage, strings.Join(identity.DefaultIgnore, ", "))
		fs.SetOutput(w)
		fs.PrintDefaults()
	}

	err := fs.Parse(args)
	if err == nil && help {
		usage(stdout)
		return 0
	}
	if err == nil && fs.NArg() < 2 {
		err = errors.New("at least two exploded repositories are needed")
	}
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n\n", err)
		usage(stderr)
		return 1
	}

	if !noDefault {
		ignore = append(append([]string{}, identity.DefaultIgnore...), ignore...)
	}
	var sources []identity.Source
	for _, dir := range fs.Args() {
		src, err := identity.Load(dir)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
			return 1
		}
		sources = append(sources, src)
	}
	overlaps := identity.Overlaps(sources, ignore)

	if asJSON {
		return writeOverlapJSON(stdout, stderr, fs.Args(), ignore, overlaps)
	}
	writeOverlapText(stdout, fs.Args(), ignore, overlaps)
	return 0
}

func writeOverlapText(w io.Writer, dirs, ignore []string, overlaps []identity.Overlap) {
	p := func(format string, args ...any) { _, _ = fmt.Fprintf(w, format, args...) }
	p("IDENTITY OVERLAP\n================\n\n")
	p("Repositories:\n")
	for _, d := range dirs {
		p("  %s\n", d)
	}
	if len(ignore) > 0 {
		p("Ignored:      %s\n", strings.Join(ignore, ", "))
	}
	p("\n")
	if len(overlaps) == 0 {
		p("(no identity appears in more than one repository)\n")
		return
	}
	for _, o := range overlaps {
		p("%s  in %d repositories\n", o.Key, len(o.Uses))
		for _, u := range o.Uses {
			p("  %s  (%d commits)\n", u.Repository, u.Commits)
			for _, id := range u.Identities {
				p("    %s\n", id)
			}
		}
		p("\n")
	}
}

func writeOverlapJSON(stdout, stderr io.Writer, dirs, ignore []string, overlaps []identity.Overlap) int {
	type useJSON struct {
		Repository string   `json:"repository"`
		Identities []string `json:"identities"`
		Commits    int      `json:"commits"`
	}
	type overlapJSON struct {
		Key  string    `json:"key"`
		Uses []useJSON `json:"uses"`
	}
	doc := struct {
		SchemaVersion int           `json:"schema_version"`
		Repositories  []string      `json:"repositories"`
		Ignored       []string      `json:"ignored"`
		Overlaps      []overlapJSON `json:"overlaps"`
	}{1, dirs, append([]string{}, ignore...), []overlapJSON{}}
	for _, o := range overlaps {
		oj := overlapJSON{Key: o.Key}
		for _, u := range o.Uses {
			oj.Uses = append(oj.Uses, useJSON{u.Repository, u.Identities, u.Commits})
		}
		doc.Overlaps = append(doc.Overlaps, oj)
	}
	enc := json.NewEncoder(stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(doc); err != nil {
		_, _ = fmt.Fprintf(stderr, "Error: %v\n", err)
		return 1
	}
	return 0
}
