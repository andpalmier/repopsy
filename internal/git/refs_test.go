package git

import (
	"context"
	"strings"
	"testing"
)

func TestReflogRecordsARewrite(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "v1\n", 0o644)
	b.Commit("one")
	b.Write("f.txt", "v2\n", 0o644)
	lost := b.Commit("SENSITIVE commit that gets rewritten")
	b.Write("f.txt", "v3\n", 0o644)
	b.Commit("three")
	// A reset, as a force-push would leave behind.
	b.Git("reset", "--hard", "HEAD~2")
	b.Write("f.txt", "v3again\n", 0o644)
	b.Commit("replacement")

	entries, err := b.open().Reflog(context.Background())
	if err != nil {
		t.Fatalf("Reflog: %v", err)
	}
	if len(entries) == 0 {
		t.Fatal("no reflog entries")
	}

	var sawLost, sawReset bool
	for _, e := range entries {
		if e.Commit == lost {
			sawLost = true
		}
		if strings.HasPrefix(e.Action, "reset:") {
			sawReset = true
		}
		// Every entry must be attributed and dated.
		if e.Ref == "" {
			t.Errorf("entry has no ref: %+v", e)
		}
		if e.Actor == "" {
			t.Errorf("entry has no actor: %+v", e)
		}
		if e.At.IsZero() {
			t.Errorf("entry has no timestamp: %+v", e)
		}
	}
	if !sawLost {
		t.Error("the rewritten-away commit is not in the reflog")
	}
	if !sawReset {
		t.Error("the reset itself is not recorded")
	}
}

func TestReflogEmptyForAFreshBareClone(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	// Reflogs are local and are not transferred, so a bare clone has none.
	bare := t.TempDir() + "/bare.git"
	b.Git("clone", "--bare", b.Dir, bare)

	repo, err := Open(bare)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	entries, err := repo.Reflog(context.Background())
	if err != nil {
		t.Fatalf("Reflog: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("expected no reflog in a bare clone, got %d entries", len(entries))
	}
}

func TestReflogTipsAreDistinct(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "v1\n", 0o644)
	b.Commit("one")
	b.Write("f.txt", "v2\n", 0o644)
	lost := b.Commit("two")
	b.Git("reset", "--hard", "HEAD~1")

	branch := b.Git("rev-parse", "--abbrev-ref", "HEAD")
	tips, err := b.open().ReflogTips(context.Background(), branch)
	if err != nil {
		t.Fatalf("ReflogTips: %v", err)
	}

	seen := map[string]bool{}
	for _, tip := range tips {
		if seen[tip] {
			t.Errorf("tip %s repeated", tip)
		}
		seen[tip] = true
	}
	if !seen[lost] {
		t.Errorf("the reset-away tip %s is missing from %v", lost, tips)
	}
}

func TestReflogTipsUnknownBranchIsNotAnError(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	tips, err := b.open().ReflogTips(context.Background(), "no-such-branch")
	if err != nil {
		t.Errorf("expected no error for a branch without a reflog, got %v", err)
	}
	if len(tips) != 0 {
		t.Errorf("expected no tips, got %v", tips)
	}
}

func TestTags(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	target := b.Commit("one")
	b.Git("tag", "light")
	b.Git("tag", "-a", "v1.0", "-m", "release one")

	tags, err := b.open().Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 2 {
		t.Fatalf("got %d tags, want 2: %+v", len(tags), tags)
	}

	byName := map[string]Tag{}
	for _, tag := range tags {
		byName[tag.Name] = tag
	}

	light, ok := byName["light"]
	if !ok {
		t.Fatal("lightweight tag missing")
	}
	if light.Annotated {
		t.Error("light should not be annotated")
	}
	// A lightweight tag points straight at the commit.
	if light.Target != target {
		t.Errorf("light target = %q, want %q", light.Target, target)
	}

	ann, ok := byName["v1.0"]
	if !ok {
		t.Fatal("annotated tag missing")
	}
	if !ann.Annotated {
		t.Error("v1.0 should be annotated")
	}
	// An annotated tag dereferences through its own object to the commit.
	if ann.Target != target {
		t.Errorf("v1.0 target = %q, want %q", ann.Target, target)
	}
	if ann.Object == "" || ann.Object == target {
		t.Errorf("v1.0 should have its own object hash, got %q", ann.Object)
	}
	if ann.Tagger != "Test User" || ann.TaggerEmail != "test@example.com" {
		t.Errorf("tagger = %q <%q>", ann.Tagger, ann.TaggerEmail)
	}
	if ann.TaggerDate.IsZero() {
		t.Error("annotated tag has no tagger date")
	}
	if ann.Subject != "release one" {
		t.Errorf("subject = %q", ann.Subject)
	}
	if ann.Signed {
		t.Error("this tag is not signed")
	}
}

func TestTagsNone(t *testing.T) {
	b := newRepo(t)
	b.Write("f.txt", "x\n", 0o644)
	b.Commit("one")

	tags, err := b.open().Tags(context.Background())
	if err != nil {
		t.Fatalf("Tags: %v", err)
	}
	if len(tags) != 0 {
		t.Errorf("expected no tags, got %+v", tags)
	}
}

func TestParseReflogLine(t *testing.T) {
	tests := []struct {
		name    string
		line    string
		wantRef string
		wantAt  string
		ok      bool
	}{
		{
			name:    "selector carries the ref and the date",
			line:    join("refs/heads/main@{2023-12-05T15:15:15+01:00}", "abc123", "Alice", "a@b", "commit: x"),
			wantRef: "refs/heads/main",
			wantAt:  "2023-12-05T15:15:15+01:00",
			ok:      true,
		},
		{
			name:    "a ref containing @ is still split at the last @{",
			line:    join("refs/heads/weird@name@{2023-12-05T15:15:15+01:00}", "abc", "A", "a@b", "x"),
			wantRef: "refs/heads/weird@name",
			wantAt:  "2023-12-05T15:15:15+01:00",
			ok:      true,
		},
		{
			name:    "selector without a date still yields the ref",
			line:    join("refs/heads/main", "abc", "A", "a@b", "x"),
			wantRef: "refs/heads/main",
			ok:      true,
		},
		{
			name: "too few fields is rejected",
			line: join("refs/heads/main", "abc"),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := parseReflogLine(tc.line)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if !tc.ok {
				return
			}
			if got.Ref != tc.wantRef {
				t.Errorf("Ref = %q, want %q", got.Ref, tc.wantRef)
			}
			if tc.wantAt == "" {
				if !got.At.IsZero() {
					t.Errorf("At = %v, want zero", got.At)
				}
			} else if got.At.Format("2006-01-02T15:04:05Z07:00") != tc.wantAt {
				t.Errorf("At = %v, want %s", got.At, tc.wantAt)
			}
		})
	}
}

func TestParseTagLineRejectsShortRecords(t *testing.T) {
	if _, ok := parseTagLine(join("v1", "tag", "abc")); ok {
		t.Error("expected a short tag record to be rejected")
	}
}

// join builds a NUL-separated record the way git emits one.
func join(fields ...string) string {
	return strings.Join(fields, fieldSep)
}
