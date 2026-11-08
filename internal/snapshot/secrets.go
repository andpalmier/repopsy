package snapshot

import (
	"fmt"
	"io"
	"strings"
	"text/template"

	"github.com/andpalmier/repopsy/v2/internal/secrets"
)

// Secrets records credential-shaped strings added in the history, for
// --scan-secrets. It never holds a value: see package secrets.
type Secrets struct {
	Findings []secrets.Finding
}

// Filename implements Report.
func (Secrets) Filename() string { return "SECRETS.txt" }

// Render writes the credential record.
func (s Secrets) Render(w io.Writer) error {
	return render(w, secretsTemplate, s.Filename(), s)
}

// Kinds lists the credential types searched for.
func (Secrets) Kinds() string { return strings.Join(secrets.Kinds(), ", ") }

var secretsTemplate = template.Must(template.New("secrets").Funcs(reportFuncs).Funcs(template.FuncMap{
	"reach": reachText,
	"live": func(o secrets.Occurrence) string {
		if len(o.LiveAt) == 0 {
			return "no extracted ref tip"
		}
		return strings.Join(o.LiveAt, ", ")
	},
	"pathText": pathText,
}).Parse(`CREDENTIAL-SHAPED STRINGS
===========================

Strings matching well-known credential formats on lines the extracted commits
add. A match is a shape, not proof of a live credential.

No value is recorded. Each is shown masked (its first six and last four
characters) with the first 12 hex digits of its SHA-256, which is enough to
recognise the same value across commits and nowhere near enough to recover it.
Rotate any real credential found here: its value is in the repository.

Searched for: {{.Kinds}}
{{range .Findings}}
{{.Kind}}  {{.Masked}}  sha256:{{.Fingerprint}}{{if .Placeholder}}  (looks like a placeholder){{end}}
{{range .Occurrences}}  added in {{.ShortHash}}  {{.Date | formatDate}}  {{pathText .Path}}
    still at tip of: {{live .}}
    reachable from:  {{reach .}}
{{end}}{{else}}
(none found)
{{end}}`))

// reachText summarises which refs reach a commit.
func reachText(o secrets.Occurrence) string {
	switch {
	case len(o.ReachableFrom) == 0:
		return "no ref (only a reflog or a rewritten history holds it)"
	case o.OnlyPullRequests:
		return fmt.Sprintf("pull request refs only (%s)", listSome(o.ReachableFrom))
	default:
		return listSome(o.ReachableFrom)
	}
}

// listSome lists up to five names, then how many more there are.
func listSome(names []string) string {
	const show = 5
	if len(names) <= show {
		return strings.Join(names, ", ")
	}
	return fmt.Sprintf("%s and %d more", strings.Join(names[:show], ", "), len(names)-show)
}

// JSON implements JSONReport.
func (s Secrets) JSON() any {
	type occurrenceJSON struct {
		Commit           string   `json:"commit"`
		Date             string   `json:"date"`
		Path             string   `json:"path"`
		LiveAt           []string `json:"live_at"`
		ReachableFrom    []string `json:"reachable_from"`
		OnlyPullRequests bool     `json:"only_pull_requests"`
	}
	type findingJSON struct {
		Type        string           `json:"type"`
		Masked      string           `json:"masked"`
		SHA256      string           `json:"sha256_prefix"`
		Placeholder bool             `json:"placeholder"`
		Occurrences []occurrenceJSON `json:"occurrences"`
	}
	findings := []findingJSON{}
	for _, f := range s.Findings {
		fj := findingJSON{f.Kind, f.Masked, f.Fingerprint, f.Placeholder, []occurrenceJSON{}}
		for _, o := range f.Occurrences {
			fj.Occurrences = append(fj.Occurrences, occurrenceJSON{o.Commit, jsonDate(o.Date), o.Path,
				nonNil(o.LiveAt), nonNil(o.ReachableFrom), o.OnlyPullRequests})
		}
		findings = append(findings, fj)
	}
	return struct {
		SchemaVersion int           `json:"schema_version"`
		Types         []string      `json:"types"`
		Findings      []findingJSON `json:"findings"`
	}{SchemaVersion, secrets.Kinds(), findings}
}
