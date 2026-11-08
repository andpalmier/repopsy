package snapshot

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/git"
)

// TestWriteMetadataJSONGolden pins the schema: a renamed field breaks every
// consumer, so it has to show up as a diff here.
func TestWriteMetadataJSONGolden(t *testing.T) {
	c := sampleCommit()
	c.Anomalies = []git.Anomaly{{Kind: "date-skew", Detail: "committed 40 days after it was authored"}}
	c.Tree = git.TreeReport{Special: []git.SpecialEntry{{Path: "scripts/deploy.sh", Mode: "100755", Written: "file without the executable bit"}}}
	var buf strings.Builder
	if err := WriteMetadataJSON(&buf, c); err != nil {
		t.Fatal(err)
	}
	assertGolden(t, "metadata.json.golden", buf.String())
}

func TestJSONListsAreNeverNull(t *testing.T) {
	var buf strings.Builder
	if err := WriteMetadataJSON(&buf, git.Commit{}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(buf.String(), "null") {
		t.Errorf("JSON contains null:\n%s", buf.String())
	}
	for _, r := range []JSONReport{Tags{}, NewIdentities(nil), Manifest{}} {
		out, err := json.Marshal(r.JSON())
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(out), "null") {
			t.Errorf("%s contains null: %s", JSONFilename(r), out)
		}
	}
}

func TestRefDir(t *testing.T) {
	out := filepath.FromSlash("/out")
	for ref, want := range map[string]string{
		"":                  "/out",
		"refs/heads/main":   "/out/refs/main",
		"refs/heads/feat/x": "/out/refs/feat/x",
		"HEAD":              "/out/refs/HEAD",
		"refs/pull/12/head": "/out/other-refs/pull/12/head",
		// A branch named like a pull request ref cannot meet the real one.
		"refs/heads/pull/12/head": "/out/refs/pull/12/head",
		"refs/remotes/origin/x":   "/out/other-refs/remotes/origin/x",
	} {
		if got := RefDir(out, ref); got != filepath.FromSlash(want) {
			t.Errorf("RefDir(%q) = %q, want %q", ref, got, want)
		}
	}
	// The established branch layout is unchanged.
	c := sampleCommit()
	got := SnapshotIn(RefDir(out, "refs/heads/feat/x"), c)
	want := filepath.FromSlash("/out/refs/feat/x/20231205_143022_8f6a2b1")
	if got != want {
		t.Errorf("snapshot path = %q, want %q", got, want)
	}
}
