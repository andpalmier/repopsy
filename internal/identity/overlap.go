package identity

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// DefaultIgnore matches automation rather than people: bots that commit to
// many repositories and would otherwise dominate any overlap. Patterns are
// matched case-insensitively against the name, the email and any GitHub
// handle, and * matches any run of characters.
var DefaultIgnore = []string{
	"*[bot]*",
	"noreply@github.com",
	"*dependabot*",
	"github-actions*",
	"action@github.com",
}

// Seen is one identity as an exploded repository records it.
type Seen struct {
	Name    string
	Email   string
	Commits int
}

// Source is one exploded repository's identities.
type Source struct {
	Label      string
	Identities []Seen
}

// Load reads the identities of an exploded repository, from IDENTITIES.json
// when it was written and IDENTITIES.txt otherwise.
func Load(dir string) (Source, error) {
	src := Source{Label: dir}
	if data, err := os.ReadFile(filepath.Join(dir, "IDENTITIES.json")); err == nil {
		var doc struct {
			Identities []struct {
				Name        string `json:"name"`
				Email       string `json:"email"`
				Commits     int    `json:"commits"`
				AsAuthor    int    `json:"as_author"`
				AsCommitter int    `json:"as_committer"`
			} `json:"identities"`
		}
		if err := json.Unmarshal(data, &doc); err != nil {
			return src, fmt.Errorf("%s: %w", filepath.Join(dir, "IDENTITIES.json"), err)
		}
		for _, id := range doc.Identities {
			commits := id.Commits
			if commits == 0 {
				// Old JSON records predate the distinct-commit field.
				commits = max(id.AsAuthor, id.AsCommitter)
			}
			src.Identities = append(src.Identities, Seen{id.Name, id.Email, commits})
		}
		return src, nil
	}

	f, err := os.Open(filepath.Join(dir, "IDENTITIES.txt"))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return src, fmt.Errorf("%s is not an exploded repository: it has no IDENTITIES.txt", dir)
		}
		return src, err
	}
	defer func() { _ = f.Close() }()
	src.Identities, err = parseIdentitiesText(f)
	return src, err
}

var (
	identityLine      = regexp.MustCompile(`^(.*) <(.*)>$`)
	commitsLine       = regexp.MustCompile(`^  Commits:\s+(\d+) total \(\d+ as author, \d+ as committer\)$`)
	legacyCommitsLine = regexp.MustCompile(`^  Commits:\s+(\d+) as author, (\d+) as committer$`)
)

// parseIdentitiesText reads the identity list at the top of IDENTITIES.txt:
// an unindented "Name <email>" line, then an indented "Commits:" line.
func parseIdentitiesText(r io.Reader) ([]Seen, error) {
	var ids []Seen
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	var pending *Seen
	for scanner.Scan() {
		line := scanner.Text()
		if line == "COLLISIONS" {
			break
		}
		if m := commitsLine.FindStringSubmatch(line); m != nil && pending != nil {
			pending.Commits, _ = strconv.Atoi(m[1])
			ids = append(ids, *pending)
			pending = nil
			continue
		}
		if m := legacyCommitsLine.FindStringSubmatch(line); m != nil && pending != nil {
			a, _ := strconv.Atoi(m[1])
			c, _ := strconv.Atoi(m[2])
			pending.Commits = max(a, c)
			ids = append(ids, *pending)
			pending = nil
			continue
		}
		if m := identityLine.FindStringSubmatch(line); m != nil && !strings.HasPrefix(line, " ") {
			name := m[1]
			if name == "(no name)" {
				name = ""
			}
			pending = &Seen{Name: name, Email: m[2]}
		}
	}
	return ids, scanner.Err()
}

// Use is how one repository uses an overlapping identity.
type Use struct {
	Repository string
	Identities []string // "Name <email>" as recorded
	Commits    int
}

// Overlap is one identity that appears in more than one repository.
type Overlap struct {
	// Key is "email:<address>" or "github:<handle>", the latter from a
	// noreply address, which names the account whatever the email shows.
	Key  string
	Uses []Use
}

// Overlaps finds identities that appear in more than one source, skipping any
// that match an ignore pattern. Sorted by how many repositories share them,
// then by key.
func Overlaps(sources []Source, ignore []string) []Overlap {
	type keyed struct {
		uses  map[int]*Use
		order []int
	}
	byKey := map[string]*keyed{}
	for si, src := range sources {
		for _, id := range src.Identities {
			if NormalizeEmail(id.Email) == "" || ignored(id, ignore) {
				continue
			}
			key := "email:" + NormalizeEmail(id.Email)
			if n, ok := ParseNoreply(id.Email); ok {
				key = "github:" + n.Handle
			}
			k := byKey[key]
			if k == nil {
				k = &keyed{uses: map[int]*Use{}}
				byKey[key] = k
			}
			u := k.uses[si]
			if u == nil {
				u = &Use{Repository: src.Label}
				k.uses[si] = u
				k.order = append(k.order, si)
			}
			u.Identities = append(u.Identities, fmt.Sprintf("%s <%s>", id.Name, id.Email))
			u.Commits += id.Commits
		}
	}

	var out []Overlap
	for key, k := range byKey {
		if len(k.uses) < 2 {
			continue
		}
		sort.Ints(k.order)
		o := Overlap{Key: key}
		for _, si := range k.order {
			sort.Strings(k.uses[si].Identities)
			o.Uses = append(o.Uses, *k.uses[si])
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool {
		if len(out[i].Uses) != len(out[j].Uses) {
			return len(out[i].Uses) > len(out[j].Uses)
		}
		return out[i].Key < out[j].Key
	})
	return out
}

// ignored reports whether any pattern matches the identity's name, email or
// GitHub handle.
func ignored(id Seen, patterns []string) bool {
	candidates := []string{id.Name, id.Email}
	if n, ok := ParseNoreply(id.Email); ok {
		candidates = append(candidates, n.Handle)
	}
	for _, p := range patterns {
		for _, c := range candidates {
			if wildcard(strings.ToLower(p), strings.ToLower(c)) {
				return true
			}
		}
	}
	return false
}

// wildcard matches s against a pattern in which only * is special, so that
// "[bot]" means itself rather than a character class.
func wildcard(pattern, s string) bool {
	parts := strings.Split(pattern, "*")
	if len(parts) == 1 {
		return pattern == s
	}
	if !strings.HasPrefix(s, parts[0]) {
		return false
	}
	s = s[len(parts[0]):]
	for _, part := range parts[1 : len(parts)-1] {
		i := strings.Index(s, part)
		if i < 0 {
			return false
		}
		s = s[i+len(part):]
	}
	return strings.HasSuffix(s, parts[len(parts)-1])
}
