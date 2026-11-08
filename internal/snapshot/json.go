package snapshot

import (
	"encoding/json"
	"fmt"
	"io"
	"path/filepath"
	"strings"
	"time"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

// SchemaVersion is the version of every JSON record repopsy writes. It changes
// only when a field is removed or changes meaning; adding a field does not.
const SchemaVersion = 1

// MetadataJSONFilename is the machine-readable form of MetadataFilename.
const MetadataJSONFilename = "COMMIT_INFO.json"

// The JSON records are built from these types rather than by marshalling the
// internal ones, so that renaming a Go field can never change the schema.
// Field names are stable; the README documents them. Lists are always present,
// empty rather than null, so a consumer never has to tell the two apart.

type personJSON struct {
	Name      string `json:"name"`
	Email     string `json:"email"`
	Date      string `json:"date"`
	Timestamp int64  `json:"timestamp"`
}

type signatureJSON struct {
	Present           bool   `json:"present"`
	Type              string `json:"type"`
	Status            string `json:"status"`
	Verdict           string `json:"verdict"`
	Verified          bool   `json:"verified"`
	Signer            string `json:"signer"`
	Key               string `json:"key"`
	Fingerprint       string `json:"fingerprint"`
	Trust             string `json:"trust"`
	IssuerKeyID       string `json:"issuer_key_id"`
	IssuerFingerprint string `json:"issuer_fingerprint"`
	Algorithm         string `json:"algorithm"`
	ClaimedSigner     string `json:"claimed_signer"`
}

type fileJSON struct {
	Status     string `json:"status"`
	Path       string `json:"path"`
	OldPath    string `json:"old_path"`
	OldMode    string `json:"old_mode"`
	NewMode    string `json:"new_mode"`
	OldBlob    string `json:"old_blob"`
	NewBlob    string `json:"new_blob"`
	Insertions int    `json:"insertions"`
	Deletions  int    `json:"deletions"`
	Binary     bool   `json:"binary"`
}

type submoduleJSON struct {
	Path   string `json:"path"`
	Commit string `json:"commit"`
}

type specialJSON struct {
	Path    string `json:"path"`
	Mode    string `json:"mode"`
	Target  string `json:"target"`
	Written string `json:"written"`
}

type pathIssueJSON struct {
	Path      string `json:"path"`
	WrittenAs string `json:"written_as"`
	Reason    string `json:"reason"`
}

type treeJSON struct {
	Written  bool            `json:"written"`
	Filtered int             `json:"filtered"`
	Special  []specialJSON   `json:"special"`
	Issues   []pathIssueJSON `json:"issues"`
}

type anomalyJSON struct {
	Kind   string `json:"kind"`
	Detail string `json:"detail"`
}

type statsJSON struct {
	FilesChanged int `json:"files_changed"`
	Insertions   int `json:"insertions"`
	Deletions    int `json:"deletions"`
}

type commitJSON struct {
	SchemaVersion   int             `json:"schema_version"`
	Hash            string          `json:"hash"`
	ShortHash       string          `json:"short_hash"`
	Tree            string          `json:"tree"`
	Refs            string          `json:"refs"`
	Encoding        string          `json:"encoding"`
	Author          personJSON      `json:"author"`
	Committer       personJSON      `json:"committer"`
	Signature       signatureJSON   `json:"signature"`
	Parents         []string        `json:"parents"`
	Root            bool            `json:"root"`
	ShallowBoundary bool            `json:"shallow_boundary"`
	Unreachable     bool            `json:"unreachable"`
	Stats           statsJSON       `json:"stats"`
	Files           []fileJSON      `json:"files"`
	Submodules      []submoduleJSON `json:"submodules"`
	TreeReport      treeJSON        `json:"tree_report"`
	Anomalies       []anomalyJSON   `json:"anomalies"`
	Subject         string          `json:"subject"`
	Message         string          `json:"message"`
	Notes           string          `json:"notes"`
}

// WriteMetadataJSON renders a commit's record as JSON into w.
func WriteMetadataJSON(w io.Writer, c git.Commit) error {
	return encodeJSON(w, commitToJSON(c))
}

func commitToJSON(c git.Commit) commitJSON {
	out := commitJSON{
		SchemaVersion:   SchemaVersion,
		Hash:            c.Hash,
		ShortHash:       c.ShortHash,
		Tree:            c.TreeHash,
		Refs:            c.Refs,
		Encoding:        c.Encoding,
		Author:          person(c.Author, c.AuthorEmail, c.AuthorDate),
		Committer:       person(c.Committer, c.CommitterEmail, c.CommitDate),
		Signature:       signatureToJSON(c.Signature),
		Parents:         nonNil(c.ParentHashes),
		Root:            len(c.ParentHashes) == 0 && !c.ShallowBoundary,
		ShallowBoundary: c.ShallowBoundary,
		Unreachable:     c.Unreachable,
		Stats:           statsJSON{c.FilesChanged, c.Insertions, c.Deletions},
		Files:           []fileJSON{},
		Submodules:      []submoduleJSON{},
		TreeReport:      treeJSON{Written: !c.Tree.NotWritten, Filtered: c.Tree.Filtered, Special: []specialJSON{}, Issues: []pathIssueJSON{}},
		Anomalies:       []anomalyJSON{},
		Subject:         c.Subject,
		Message:         c.FullMessage,
		Notes:           c.Notes,
	}
	for _, f := range c.Files {
		out.Files = append(out.Files, fileJSON{f.Status, f.Path, f.OldPath, f.OldMode, f.NewMode, f.OldBlob, f.NewBlob, f.Insertions, f.Deletions, f.Binary})
	}
	for _, s := range c.Submodules {
		out.Submodules = append(out.Submodules, submoduleJSON{s.Path, s.Commit})
	}
	for _, s := range c.Tree.Special {
		out.TreeReport.Special = append(out.TreeReport.Special, specialJSON{s.Path, s.Mode, s.Target, s.Written})
	}
	for _, i := range c.Tree.Issues {
		out.TreeReport.Issues = append(out.TreeReport.Issues, pathIssueJSON{i.Path, i.WrittenAs, i.Reason})
	}
	for _, a := range c.Anomalies {
		out.Anomalies = append(out.Anomalies, anomalyJSON{a.Kind, a.Detail})
	}
	return out
}

func person(name, email string, at time.Time) personJSON {
	return personJSON{Name: name, Email: email, Date: jsonDate(at), Timestamp: at.Unix()}
}

func signatureToJSON(s git.Signature) signatureJSON {
	return signatureJSON{
		Present: s.Signed(), Type: s.Type, Status: s.Status, Verdict: FormatSignature(s), Verified: s.Verified(),
		Signer: s.Signer, Key: s.Key, Fingerprint: s.Fingerprint, Trust: s.Trust,
		IssuerKeyID: s.IssuerKeyID, IssuerFingerprint: s.IssuerFingerprint,
		Algorithm: s.Algorithm, ClaimedSigner: s.ClaimedSigner,
	}
}

// jsonDate renders a date with the offset git recorded, or "" when unknown.
func jsonDate(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.Format(dateFormat)
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

// encodeJSON writes v as indented JSON. HTML escaping is off, so an email's
// angle brackets stay readable.
func encodeJSON(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(v); err != nil {
		return fmt.Errorf("failed to encode JSON: %w", err)
	}
	return nil
}

// JSONReport is a root record with a machine-readable form.
type JSONReport interface {
	Report

	// JSON returns the record's JSON document.
	JSON() any
}

// JSONFilename is the name of a record's JSON form: FOO.txt becomes FOO.json.
func JSONFilename(r Report) string {
	return strings.TrimSuffix(r.Filename(), filepath.Ext(r.Filename())) + ".json"
}

// WriteJSON writes a record's JSON form into dir.
func WriteJSON(dir string, r JSONReport) error {
	return WriteFile(dir, JSONFilename(r), func(w io.Writer) error {
		return encodeJSON(w, r.JSON())
	})
}

// JSON implements JSONReport.
func (t Tags) JSON() any {
	type tagJSON struct {
		Name          string `json:"name"`
		Target        string `json:"target"`
		Annotated     bool   `json:"annotated"`
		Object        string `json:"object"`
		Tagger        string `json:"tagger"`
		TaggerEmail   string `json:"tagger_email"`
		TaggerDate    string `json:"tagger_date"`
		Subject       string `json:"subject"`
		Signed        bool   `json:"signed"`
		SignatureType string `json:"signature_type"`
	}
	tags := []tagJSON{}
	for _, tag := range t.Tags {
		tags = append(tags, tagJSON{tag.Name, tag.Target, tag.Annotated, tag.Object, tag.Tagger,
			tag.TaggerEmail, jsonDate(tag.TaggerDate), tag.Subject, tag.Signed, tag.SignatureType})
	}
	return struct {
		SchemaVersion int       `json:"schema_version"`
		Tags          []tagJSON `json:"tags"`
	}{SchemaVersion, tags}
}

// JSON implements JSONReport.
func (i Identities) JSON() any {
	type identityJSON struct {
		Name        string `json:"name"`
		Email       string `json:"email"`
		Commits     int    `json:"commits"`
		AsAuthor    int    `json:"as_author"`
		AsCommitter int    `json:"as_committer"`
		FirstSeen   string `json:"first_seen"`
		LastSeen    string `json:"last_seen"`
	}
	ids := []identityJSON{}
	for _, id := range i.Identities {
		ids = append(ids, identityJSON{id.Name, id.Email, id.Commits, id.AsAuthor, id.AsCommitter, jsonDate(id.First), jsonDate(id.Last)})
	}
	return struct {
		SchemaVersion    int            `json:"schema_version"`
		Identities       []identityJSON `json:"identities"`
		SharedEmails     []string       `json:"shared_emails"`
		SharedNames      []string       `json:"shared_names"`
		Placeholders     []string       `json:"placeholders"`
		NoreplyConflicts []string       `json:"noreply_conflicts"`
	}{SchemaVersion, ids, nonNil(i.SharedEmails), nonNil(i.SharedNames), nonNil(i.Placeholders), nonNil(i.NoreplyConflicts)}
}

// JSON implements JSONReport.
func (m Manifest) JSON() any {
	type countJSON struct {
		Name  string `json:"name"`
		Count int    `json:"count"`
	}
	counts := func(cs []Count) []countJSON {
		out := []countJSON{}
		for _, c := range cs {
			out = append(out, countJSON{c.Name, c.N})
		}
		return out
	}
	type refJSON struct {
		Name      string `json:"name"`
		Commits   int    `json:"commits"`
		Snapshots int    `json:"snapshots"`
		Rewritten int    `json:"rewritten"`
		Unparsed  int    `json:"unparsed"`
		Skipped   string `json:"skipped"`
	}
	type namespaceJSON struct {
		Name      string `json:"name"`
		Refs      int    `json:"refs"`
		Extracted int    `json:"extracted"`
		Hint      string `json:"hint"`
	}
	type failureJSON struct {
		Ref       string `json:"ref"`
		ShortHash string `json:"short_hash"`
		Reason    string `json:"reason"`
	}
	type pathIssueSummaryJSON struct {
		pathIssueJSON
		Commits     int    `json:"commits"`
		FirstCommit string `json:"first_commit"`
	}
	refs := []refJSON{}
	for _, b := range m.Branches {
		refs = append(refs, refJSON{b.Name, b.Commits, b.Extracted, b.Rewritten, b.Unparsed, b.Skipped})
	}
	namespaces := []namespaceJSON{}
	for _, n := range m.Namespaces {
		// A conversion, so adding a field to NamespaceSummary fails to compile
		// here rather than silently leaving it out of the schema.
		namespaces = append(namespaces, namespaceJSON(n))
	}
	failures := []failureJSON{}
	for _, f := range m.Failures {
		failures = append(failures, failureJSON{f.Branch, f.ShortHash, f.Reason})
	}
	anomalies := []countJSON{}
	for _, k := range m.Anomalies.Counts {
		anomalies = append(anomalies, countJSON{k.Kind, k.Commits})
	}
	issues := []pathIssueSummaryJSON{}
	for _, p := range m.PathIssues {
		issues = append(issues, pathIssueSummaryJSON{pathIssueJSON{p.Issue.Path, p.Issue.WrittenAs, p.Issue.Reason}, p.Commits, p.First})
	}

	type toolJSON struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Built   string `json:"built"`
	}
	type sourceJSON struct {
		Repository     string   `json:"repository"`
		Scope          string   `json:"scope"`
		Branch         string   `json:"branch"`
		RefPatterns    []string `json:"ref_patterns"`
		AllRefs        bool     `json:"all_refs"`
		Workers        int      `json:"workers"`
		Limit          int      `json:"limit"`
		Shallow        bool     `json:"shallow"`
		ShallowCommits []string `json:"shallow_boundary_commits"`
		TreeWritten    bool     `json:"tree_written"`
		PreserveModes  bool     `json:"preserve_modes"`
		Paths          []string `json:"paths"`
		Dedupe         bool     `json:"dedupe"`
		AllowedSigners string   `json:"allowed_signers"`
	}
	type signaturesJSON struct {
		Commits     int         `json:"commits"`
		Signed      int         `json:"signed"`
		Verified    int         `json:"verified"`
		Summary     string      `json:"summary"`
		ByType      []countJSON `json:"by_type"`
		ByVerdict   []countJSON `json:"by_verdict"`
		ByCommitter []countJSON `json:"by_committer"`
		ByIssuer    []countJSON `json:"by_issuer"`
	}
	type anomaliesJSON struct {
		Repository []string    `json:"repository"`
		ByKind     []countJSON `json:"by_kind"`
	}
	s := m.Signatures
	return struct {
		SchemaVersion int                    `json:"schema_version"`
		Tool          toolJSON               `json:"tool"`
		Started       string                 `json:"started"`
		Finished      string                 `json:"finished"`
		Source        sourceJSON             `json:"source"`
		Refs          []refJSON              `json:"refs"`
		Namespaces    []namespaceJSON        `json:"namespaces"`
		Snapshots     int                    `json:"snapshots"`
		UniqueCommits int                    `json:"unique_commits"`
		Failures      []failureJSON          `json:"failures"`
		Signatures    signaturesJSON         `json:"signatures"`
		Anomalies     anomaliesJSON          `json:"anomalies"`
		PathIssues    []pathIssueSummaryJSON `json:"path_issues"`
	}{
		SchemaVersion: SchemaVersion,
		Tool:          toolJSON{m.ToolVersion, m.ToolCommit, m.ToolBuilt},
		Started:       jsonDate(m.StartedAt),
		Finished:      jsonDate(m.FinishedAt),
		Source: sourceJSON{
			Repository: m.RepoPath, Scope: m.Scope(), Branch: m.Branch, RefPatterns: nonNil(m.RefPatterns),
			AllRefs: m.AllRefs, Workers: m.Workers, Limit: m.Limit, Shallow: len(m.Shallow) > 0,
			ShallowCommits: nonNil(m.Shallow), TreeWritten: !m.NoTree, PreserveModes: m.PreserveModes,
			Paths: nonNil(m.Paths), Dedupe: m.Dedupe, AllowedSigners: m.AllowedSigners,
		},
		Refs:          refs,
		Namespaces:    namespaces,
		Snapshots:     m.Snapshots(),
		UniqueCommits: m.UniqueCommits,
		Failures:      failures,
		Signatures: signaturesJSON{s.Commits, s.Signed, s.Verified, s.Headline(),
			counts(s.ByType), counts(s.ByVerdict), counts(s.ByCommitter), counts(s.ByIssuer)},
		Anomalies:  anomaliesJSON{nonNil(m.Anomalies.Repository), anomalies},
		PathIssues: issues,
	}
}
