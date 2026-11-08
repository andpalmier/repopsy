// Package secrets finds strings shaped like well-known credentials on the
// lines a history adds, and records them without ever recording the value.
//
// A finding keeps only the credential type, a masked form (the first six and
// last four characters), and a short SHA-256 prefix that lets the same value
// be recognised across commits. The value itself is never written anywhere,
// nor passed to another process.
package secrets

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

// hashPrefix is how much of the SHA-256 is kept: enough to tell values apart,
// far too little to confirm a guess at a value by brute force.
const hashPrefix = 12

// pattern is one credential shape. The expressions are Go's RE2 and are never
// handed to git, whose -G would read them as POSIX and silently match nothing.
type pattern struct {
	kind string
	re   *regexp.Regexp
}

// patterns are checked in order, and a span claimed by an earlier one is not
// reported again: an Anthropic key also matches the OpenAI shape.
var patterns = []pattern{
	{"anthropic-api-key", regexp.MustCompile(`\bsk-ant-[A-Za-z0-9_\-]{32,}`)},
	{"openai-api-key", regexp.MustCompile(`\bsk-(?:proj-|svcacct-|admin-)?[A-Za-z0-9_\-]{32,}`)},
	{"aws-access-key-id", regexp.MustCompile(`\b(?:AKIA|ASIA)[0-9A-Z]{16}\b`)},
	{"github-token", regexp.MustCompile(`\b(?:ghp|gho|ghu|ghs|ghr)_[A-Za-z0-9]{36,}\b`)},
	{"github-fine-grained-pat", regexp.MustCompile(`\bgithub_pat_[A-Za-z0-9_]{22,}`)},
	{"google-api-key", regexp.MustCompile(`\bAIza[0-9A-Za-z_\-]{35}`)},
	{"slack-token", regexp.MustCompile(`\bxox[abposr]-[A-Za-z0-9\-]{10,}`)},
	{"telegram-bot-token", regexp.MustCompile(`\b[0-9]{8,10}:AA[A-Za-z0-9_\-]{33}`)},
	{"alibaba-access-key-id", regexp.MustCompile(`\bLTAI[A-Za-z0-9]{12,20}\b`)},
	{"tencent-secret-id", regexp.MustCompile(`\bAKID[A-Za-z0-9]{13,40}\b`)},
	{"zhipu-api-key", regexp.MustCompile(`\b[0-9a-f]{32}\.[A-Za-z0-9]{16}\b`)},
	{"private-key", regexp.MustCompile(`-----BEGIN (?:RSA |DSA |EC |OPENSSH |ENCRYPTED |PGP )?PRIVATE KEY(?: BLOCK)?-----`)},
}

// Kinds lists every credential type the scan recognises, in check order.
func Kinds() []string {
	kinds := make([]string, len(patterns))
	for i, p := range patterns {
		kinds[i] = p.kind
	}
	return kinds
}

// match is one credential-shaped string found in a line, already reduced to
// what may be kept. The placeholder verdict needs the value, so it is taken
// here too.
type match struct {
	kind        string
	masked      string
	hash        string
	placeholder bool
}

// scanLine returns every credential-shaped string in line.
func scanLine(line string) []match {
	var found []match
	var claimed [][2]int
	for _, p := range patterns {
		for _, loc := range p.re.FindAllStringIndex(line, -1) {
			if overlaps(claimed, loc) {
				continue
			}
			claimed = append(claimed, [2]int{loc[0], loc[1]})
			value := line[loc[0]:loc[1]]
			found = append(found, match{kind: p.kind, masked: Mask(value), hash: Fingerprint(value), placeholder: isPlaceholder(value)})
		}
	}
	return found
}

func overlaps(claimed [][2]int, loc []int) bool {
	for _, c := range claimed {
		if loc[0] < c[1] && c[0] < loc[1] {
			return true
		}
	}
	return false
}

// Mask shows the first six and last four characters of a value. A value too
// short for that to hide most of it shows less.
func Mask(value string) string {
	if len(value) < 20 {
		return value[:min(3, len(value))] + "..."
	}
	return value[:6] + "..." + value[len(value)-4:]
}

// Fingerprint is the short SHA-256 prefix a finding is identified by.
func Fingerprint(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])[:hashPrefix]
}

// placeholderHints mark values that look like documentation rather than a
// live credential.
var placeholderHints = []string{"xxxx", "0000", "1234", "example", "test", "dummy", "fake", "sample",
	"placeholder", "your", "changeme", "redacted", "abcdef"}

// isPlaceholder reports whether a value looks like a placeholder. Evaluated
// while the value is in hand, so it is never kept for later.
func isPlaceholder(value string) bool {
	lower := strings.ToLower(value)
	for _, h := range placeholderHints {
		if strings.Contains(lower, h) {
			return true
		}
	}
	// RE2 has no backreferences, so a run of one repeated character is
	// checked by hand.
	run := 1
	for i := 1; i < len(value); i++ {
		if value[i] == value[i-1] {
			if run++; run >= 8 {
				return true
			}
		} else {
			run = 1
		}
	}
	return false
}

// Occurrence is one commit adding a value at a path.
type Occurrence struct {
	Commit    string
	ShortHash string
	Date      time.Time
	Path      string

	// LiveAt lists the extracted refs whose tip still has the value at Path.
	LiveAt []string

	// ReachableFrom lists every ref in the repository that reaches Commit,
	// and OnlyPullRequests whether all of them are under refs/pull/.
	ReachableFrom    []string
	OnlyPullRequests bool
}

// Finding is one distinct value, identified by its fingerprint, and every
// commit that adds it.
type Finding struct {
	Kind        string
	Masked      string
	Fingerprint string
	Placeholder bool
	Occurrences []Occurrence
}

// Tip is a ref tip checked for whether a value is still present.
type Tip struct {
	Label string
	Rev   string
}

// Scan finds credential-shaped strings added by commits and works out where
// each still lives. commits must be the distinct commits to scan.
func Scan(ctx context.Context, repo *git.Repository, commits []git.Commit, tips []Tip) ([]Finding, error) {
	dates := map[string]time.Time{}
	hashes := make([]string, len(commits))
	for i, c := range commits {
		hashes[i] = c.Hash
		dates[c.Hash] = c.AuthorDate
	}

	byHash := map[string]*Finding{}
	seen := map[[3]string]bool{}
	err := repo.AddedLines(ctx, hashes, func(l git.AddedLine) {
		for _, m := range scanLine(l.Text) {
			key := [3]string{m.hash, l.Commit, l.Path}
			if seen[key] {
				continue
			}
			seen[key] = true
			f := byHash[m.hash]
			if f == nil {
				f = &Finding{Kind: m.kind, Masked: m.masked, Fingerprint: m.hash, Placeholder: m.placeholder}
				byHash[m.hash] = f
			}
			f.Occurrences = append(f.Occurrences, Occurrence{Commit: l.Commit, ShortHash: l.Commit[:min(7, len(l.Commit))], Date: dates[l.Commit], Path: l.Path})
		}
	})
	if err != nil {
		return nil, err
	}

	findings := make([]Finding, 0, len(byHash))
	for _, f := range byHash {
		sort.SliceStable(f.Occurrences, func(i, j int) bool { return f.Occurrences[i].Date.Before(f.Occurrences[j].Date) })
		findings = append(findings, *f)
	}
	sort.Slice(findings, func(i, j int) bool {
		a, b := findings[i].Occurrences[0], findings[j].Occurrences[0]
		if !a.Date.Equal(b.Date) {
			return a.Date.Before(b.Date)
		}
		return findings[i].Fingerprint < findings[j].Fingerprint
	})

	if err := liveAtTips(ctx, repo, findings, tips); err != nil {
		return nil, err
	}
	if err := reachability(ctx, repo, findings); err != nil {
		return nil, err
	}
	return findings, nil
}

// liveAtTips checks, for each occurrence, whether each ref's tip still has the
// value at the same path. The check compares fingerprints of what the tip's
// file contains, so no value has to be kept from the scan.
func liveAtTips(ctx context.Context, repo *git.Repository, findings []Finding, tips []Tip) error {
	type key struct{ tip, path string }
	var names []string
	index := map[key]int{}
	for _, f := range findings {
		for _, o := range f.Occurrences {
			for _, t := range tips {
				k := key{t.Rev, o.Path}
				if _, ok := index[k]; ok {
					continue
				}
				index[k] = len(names)
				names = append(names, t.Rev+":"+o.Path)
			}
		}
	}

	present := make([]map[string]bool, len(names))
	err := repo.ReadObjects(ctx, names, func(i int, content []byte, found bool) {
		if !found {
			return
		}
		present[i] = map[string]bool{}
		for line := range strings.SplitSeq(string(content), "\n") {
			for _, m := range scanLine(line) {
				present[i][m.hash] = true
			}
		}
	})
	if err != nil {
		return err
	}

	for fi := range findings {
		f := &findings[fi]
		for oi := range f.Occurrences {
			o := &f.Occurrences[oi]
			for _, t := range tips {
				if present[index[key{t.Rev, o.Path}]][f.Fingerprint] {
					o.LiveAt = append(o.LiveAt, t.Label)
				}
			}
		}
	}
	return nil
}

// reachability lists which refs reach each commit, and whether they are all
// pull request refs: a credential only a pull request reaches came from a
// contributor's branch, not from the project's own history.
func reachability(ctx context.Context, repo *git.Repository, findings []Finding) error {
	cache := map[string][]string{}
	for fi := range findings {
		for oi := range findings[fi].Occurrences {
			o := &findings[fi].Occurrences[oi]
			refs, ok := cache[o.Commit]
			if !ok {
				var err error
				if refs, err = repo.RefsContaining(ctx, o.Commit); err != nil {
					return err
				}
				cache[o.Commit] = refs
			}
			o.ReachableFrom = refs
			o.OnlyPullRequests = len(refs) > 0
			for _, r := range refs {
				if !strings.HasPrefix(r, "refs/pull/") {
					o.OnlyPullRequests = false
				}
			}
		}
	}
	return nil
}
