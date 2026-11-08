package snapshot

import (
	"fmt"
	"io"
	"strconv"
	"text/template"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

// Strings records the history of each string given with --track.
type Strings struct {
	Histories []git.StringHistory
}

// Filename implements Report.
func (Strings) Filename() string { return "STRINGS.txt" }

// Render writes the string history record.
func (s Strings) Render(w io.Writer) error {
	return render(w, stringsTemplate, s.Filename(), s)
}

var stringsTemplate = template.Must(template.New("strings").Funcs(reportFuncs).Funcs(template.FuncMap{
	"quote":    strconv.Quote,
	"first":    first,
	"signed":   func(n int) string { return fmt.Sprintf("%+d", n) },
	"pathText": pathText,
}).Parse(`TRACKED STRINGS
===========================

For each string: the first commit that adds it, every commit that changes how
often it occurs (git log -S, merges compared against their first parent) in
date order, and how often it occurs at each extracted ref's tip. Strings are
matched literally and case-sensitively, across every file including binaries.
Commits outside the extracted refs are not searched.
{{range .Histories}}
{{quote .String}}
{{with first .}}  First appears:  {{.ShortHash}}  {{.Date | formatDate}}  {{.Author}} <{{.Email}}>
                  {{.Subject}}
{{else}}  First appears:  never, in the extracted history
{{end}}  Changes:        {{len .Changes}} commit(s)
{{range .Changes}}    {{.ShortHash}}  {{.Date | formatDate}}  {{signed .Delta}}  {{.Author}} <{{.Email}}>  {{.Subject}}
{{range .Paths}}      {{pathText .Path}}  {{.Before}} -> {{.After}}
{{end}}{{end}}  At ref tips:
{{range .Tips}}    {{.Ref}}: {{.Total}}
{{range .Paths}}      {{pathText .Path}}  {{.After}}
{{end}}{{end}}{{end}}`))

// First adapts StringHistory.First for the template, which cannot take two
// return values in a with.
func first(h git.StringHistory) *git.StringChange {
	if c, ok := h.First(); ok {
		return &c
	}
	return nil
}

// JSON implements JSONReport.
func (s Strings) JSON() any {
	type countJSON struct {
		Path   string `json:"path"`
		Before int    `json:"before"`
		After  int    `json:"after"`
	}
	type changeJSON struct {
		Hash    string      `json:"hash"`
		Date    string      `json:"date"`
		Author  string      `json:"author"`
		Email   string      `json:"email"`
		Subject string      `json:"subject"`
		Delta   int         `json:"delta"`
		Paths   []countJSON `json:"paths"`
	}
	type tipPathJSON struct {
		Path  string `json:"path"`
		Count int    `json:"count"`
	}
	type tipJSON struct {
		Ref   string        `json:"ref"`
		Count int           `json:"count"`
		Paths []tipPathJSON `json:"paths"`
	}
	type historyJSON struct {
		String      string       `json:"string"`
		FirstCommit string       `json:"first_commit"`
		Changes     []changeJSON `json:"changes"`
		Tips        []tipJSON    `json:"tips"`
	}
	histories := []historyJSON{}
	for _, h := range s.Histories {
		hj := historyJSON{String: h.String, Changes: []changeJSON{}, Tips: []tipJSON{}}
		if c := first(h); c != nil {
			hj.FirstCommit = c.Hash
		}
		for _, c := range h.Changes {
			cj := changeJSON{c.Hash, jsonDate(c.Date), c.Author, c.Email, c.Subject, c.Delta(), []countJSON{}}
			for _, p := range c.Paths {
				cj.Paths = append(cj.Paths, countJSON{p.Path, p.Before, p.After})
			}
			hj.Changes = append(hj.Changes, cj)
		}
		for _, t := range h.Tips {
			tj := tipJSON{t.Ref, t.Total, []tipPathJSON{}}
			for _, p := range t.Paths {
				tj.Paths = append(tj.Paths, tipPathJSON{p.Path, p.After})
			}
			hj.Tips = append(hj.Tips, tj)
		}
		histories = append(histories, hj)
	}
	return struct {
		SchemaVersion int           `json:"schema_version"`
		Strings       []historyJSON `json:"strings"`
	}{SchemaVersion, histories}
}
