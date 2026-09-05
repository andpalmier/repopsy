package snapshot

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"text/template"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

// Report is one of the files written at the root of an exploded repository,
// describing the extraction rather than any single commit.
type Report interface {
	// Filename is the report's name at the output root.
	Filename() string

	// Render writes the report's content.
	Render(w io.Writer) error
}

// Write renders a report into dir, creating dir if it does not exist yet.
func Write(dir string, r Report) (err error) {
	if mkErr := os.MkdirAll(dir, dirPerms); mkErr != nil {
		return fmt.Errorf("failed to create output directory: %w", mkErr)
	}

	f, err := os.Create(filepath.Join(dir, r.Filename()))
	if err != nil {
		return fmt.Errorf("failed to create %s: %w", r.Filename(), err)
	}
	defer func() {
		if closeErr := f.Close(); closeErr != nil && err == nil {
			err = fmt.Errorf("failed to close %s: %w", r.Filename(), closeErr)
		}
	}()

	return r.Render(f)
}

// dirPerms is the mode for directories this package creates.
const dirPerms = 0o755

// reportFuncs are shared by every report template.
var reportFuncs = template.FuncMap{
	"formatDate": func(t time.Time) string {
		if t.IsZero() {
			return "(unknown)"
		}
		return t.Format(dateFormat)
	},
	"or_": func(s, fallback string) string {
		if s == "" {
			return fallback
		}
		return s
	},
}

// render is the shared body of every report's Render method.
func render(w io.Writer, t *template.Template, name string, data any) error {
	if err := t.Execute(w, data); err != nil {
		return fmt.Errorf("failed to render %s: %w", name, err)
	}
	return nil
}

// ── Reflog ──────────────────────────────────────────────────────────────────

// Reflog records every movement of every ref. It is the only evidence of
// history that was rewritten away.
type Reflog struct {
	Entries []git.ReflogEntry
}

// Filename implements Report.
func (Reflog) Filename() string { return "REFLOG.txt" }

// Render writes the ref movement log.
func (r Reflog) Render(w io.Writer) error {
	return render(w, reflogTemplate, r.Filename(), r)
}

var reflogTemplate = template.Must(template.New("reflog").Funcs(reportFuncs).Parse(
	`REF MOVEMENT LOG
===========================

Every recorded movement of every ref, newest first per ref. A reset or a
force-push leaves the commits it replaced unreachable but still listed here, so
this is the record of any history that was rewritten away.

Reflogs are local to a repository and are NOT transferred by clone. An empty
log below means either that no ref ever moved, or that this repository is a
clone or a bare mirror and its history was never rewritten here.

{{if .Entries}}{{range .Entries}}{{.Ref}}
  {{.Commit}}  {{.At | formatDate}}
  {{.Action}}
  by {{or_ .Actor "(unknown)"}} <{{.Email}}>
{{end}}{{else}}(no reflog entries)
{{end}}`))

// ── Tags ────────────────────────────────────────────────────────────────────

// Tags records every tag and, for annotated tags, the independent attestation
// each one carries.
type Tags struct {
	Tags []git.Tag
}

// Filename implements Report.
func (Tags) Filename() string { return "TAGS.txt" }

// Render writes the tag record.
func (t Tags) Render(w io.Writer) error {
	return render(w, tagsTemplate, t.Filename(), t)
}

var tagsTemplate = template.Must(template.New("tags").Funcs(reportFuncs).Parse(
	`TAGS
===========================

Tags are not extracted as snapshots; their targets appear under the branches
that reach them. An annotated tag carries its own tagger identity, date and
signature, which is an attestation separate from the commit it points at.

{{if .Tags}}{{range .Tags}}{{.Name}}
  Target:       {{.Target}}
  Type:         {{if .Annotated}}annotated (tag object {{.Object}}){{else}}lightweight{{end}}
{{if .Annotated}}  Tagger:       {{or_ .Tagger "(not recorded)"}} <{{.TaggerEmail}}>
  Date:         {{.TaggerDate | formatDate}}
  Signature:    {{if .Signed}}present{{else}}none{{end}}
  Subject:      {{.Subject}}
{{end}}{{end}}{{else}}(no tags)
{{end}}`))

// ── Identities ──────────────────────────────────────────────────────────────

// Identity is one name and email pair seen in the history, with where it
// appeared.
type Identity struct {
	Name  string
	Email string

	AsAuthor    int
	AsCommitter int

	First time.Time
	Last  time.Time
}

// Identities records every distinct identity in the extracted history.
//
// Collisions matter: one email used under several names, or one name under
// several emails, is invisible commit by commit but obvious in aggregate, and
// is how impersonation and misconfigured clients both look.
type Identities struct {
	Identities []Identity

	// SharedEmails and SharedNames list the colliding values.
	SharedEmails []string
	SharedNames  []string
}

// Filename implements Report.
func (Identities) Filename() string { return "IDENTITIES.txt" }

// Render writes the identity record.
func (i Identities) Render(w io.Writer) error {
	return render(w, identitiesTemplate, i.Filename(), i)
}

// NewIdentities aggregates the identities in commits. It needs no git calls:
// every field is already on the commits that were parsed for extraction.
func NewIdentities(commits []git.Commit) Identities {
	type key struct{ name, email string }
	seen := map[key]*Identity{}

	record := func(name, email string, at time.Time, author bool) {
		k := key{name, email}
		id, ok := seen[k]
		if !ok {
			id = &Identity{Name: name, Email: email, First: at, Last: at}
			seen[k] = id
		}
		if at.Before(id.First) {
			id.First = at
		}
		if at.After(id.Last) {
			id.Last = at
		}
		if author {
			id.AsAuthor++
		} else {
			id.AsCommitter++
		}
	}

	for _, c := range commits {
		record(c.Author, c.AuthorEmail, c.AuthorDate, true)
		record(c.Committer, c.CommitterEmail, c.CommitDate, false)
	}

	result := Identities{Identities: make([]Identity, 0, len(seen))}
	for _, id := range seen {
		result.Identities = append(result.Identities, *id)
	}
	// Most active first, then by email so the order is stable.
	sort.Slice(result.Identities, func(a, b int) bool {
		x, y := result.Identities[a], result.Identities[b]
		if n, m := x.AsAuthor+x.AsCommitter, y.AsAuthor+y.AsCommitter; n != m {
			return n > m
		}
		return x.Email < y.Email
	})

	result.SharedEmails = collisions(result.Identities, func(i Identity) (string, string) { return i.Email, i.Name })
	result.SharedNames = collisions(result.Identities, func(i Identity) (string, string) { return i.Name, i.Email })
	return result
}

// collisions finds values of the first field that appear with more than one
// distinct value of the second.
func collisions(ids []Identity, split func(Identity) (string, string)) []string {
	others := map[string]map[string]bool{}
	for _, id := range ids {
		a, b := split(id)
		if others[a] == nil {
			others[a] = map[string]bool{}
		}
		others[a][b] = true
	}

	var found []string
	for value, set := range others {
		if len(set) > 1 {
			var pairs []string
			for other := range set {
				pairs = append(pairs, other)
			}
			sort.Strings(pairs)
			found = append(found, fmt.Sprintf("%s used by: %s", value, strings.Join(pairs, ", ")))
		}
	}
	sort.Strings(found)
	return found
}

var identitiesTemplate = template.Must(template.New("identities").Funcs(reportFuncs).Parse(
	`IDENTITIES
===========================

Every distinct name and email pair in the extracted history, most active first.
Counts are the number of commits in which the identity appeared in each role.

{{if .Identities}}{{range .Identities}}{{or_ .Name "(no name)"}} <{{.Email}}>
  Commits:      {{.AsAuthor}} as author, {{.AsCommitter}} as committer
  First seen:   {{.First | formatDate}}
  Last seen:    {{.Last | formatDate}}
{{end}}{{else}}(no identities)
{{end}}
COLLISIONS
----------
One email under several names, or one name under several emails, is invisible
commit by commit. It is how both impersonation and a misconfigured client look.

Emails used under more than one name:
{{if .SharedEmails}}{{range .SharedEmails}}  {{.}}
{{end}}{{else}}  (none)
{{end}}
Names used with more than one email:
{{if .SharedNames}}{{range .SharedNames}}  {{.}}
{{end}}{{else}}  (none)
{{end}}`))

// ── Repository state ────────────────────────────────────────────────────────

// RepositoryState records the parts of a repository that are not versioned, so
// no commit walk reaches them.
type RepositoryState struct {
	State git.State
}

// Filename implements Report.
func (RepositoryState) Filename() string { return "REPOSITORY.txt" }

// Render writes the repository state record.
func (r RepositoryState) Render(w io.Writer) error {
	return render(w, repositoryStateTemplate, r.Filename(), r)
}

var repositoryStateTemplate = template.Must(template.New("state").Funcs(reportFuncs).Parse(
	`REPOSITORY STATE
===========================

The repository's local configuration and installed hooks. Neither is versioned,
so no commit contains them and no amount of history walking reveals them. A hook
is an executable git runs on repository events, which makes a malicious one a
real attack and an unexpected one worth explaining.

Hooks that git ships as inert *.sample files are omitted.

CONFIGURATION
-------------
{{if .State.Config}}{{.State.Config}}{{else}}(no local configuration file)
{{end}}
HOOKS
-----
{{if .State.Hooks}}{{range .State.Hooks}}{{.Name}}
  Size:         {{.Size}} bytes
  SHA-256:      {{.SHA256}}
  Executable:   {{if .Executable}}yes{{else}}no - git will not run it{{end}}
  Content:{{if .Truncated}} (truncated){{end}}
{{.Content}}
{{end}}{{else}}(no hooks installed)
{{end}}`))
