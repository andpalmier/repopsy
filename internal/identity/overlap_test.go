package identity_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/identity"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// exploded writes an exploded repository's identity records, through the same
// renderer a real run uses, so the parser is tested against the real format.
func exploded(t *testing.T, withJSON bool, people ...[2]string) string {
	t.Helper()
	var commits []git.Commit
	for _, p := range people {
		commits = append(commits, git.Commit{Author: p[0], AuthorEmail: p[1], Committer: p[0], CommitterEmail: p[1]})
	}
	dir := t.TempDir()
	ids := snapshot.NewIdentities(commits)
	if err := snapshot.Write(dir, ids); err != nil {
		t.Fatal(err)
	}
	if withJSON {
		if err := snapshot.WriteJSON(dir, ids); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

func TestOverlaps(t *testing.T) {
	a := exploded(t, false,
		[2]string{"Alice", "Alice@Company.io"},
		[2]string{"A. Dev", "13154679+alicedev@users.noreply.github.com"},
		[2]string{"dependabot[bot]", "49699333+dependabot[bot]@users.noreply.github.com"},
		[2]string{"GitHub", "noreply@github.com"},
		[2]string{"Only Here", "solo@company.io"},
	)
	b := exploded(t, true,
		[2]string{"alice", "alice@company.io"},
		[2]string{"Alice D", "alicedev@users.noreply.github.com"},
		[2]string{"dependabot[bot]", "49699333+dependabot[bot]@users.noreply.github.com"},
		[2]string{"GitHub", "noreply@github.com"},
	)

	var sources []identity.Source
	for _, dir := range []string{a, b} {
		src, err := identity.Load(dir)
		if err != nil {
			t.Fatal(err)
		}
		sources = append(sources, src)
	}
	if len(sources[0].Identities) != 5 || sources[0].Identities[0].Commits != 1 {
		t.Fatalf("parsed from text: %+v", sources[0].Identities)
	}

	got := identity.Overlaps(sources, identity.DefaultIgnore)
	var keys []string
	for _, o := range got {
		keys = append(keys, o.Key)
	}
	if strings.Join(keys, " ") != "email:alice@company.io github:alicedev" {
		t.Errorf("overlaps = %v", keys)
	}
	if got[1].Uses[0].Identities[0] != "A. Dev <13154679+alicedev@users.noreply.github.com>" {
		t.Errorf("uses = %+v", got[1].Uses)
	}

	// Without the ignore list, the bots overlap too.
	if n := len(identity.Overlaps(sources, nil)); n != 4 {
		t.Errorf("unfiltered overlaps = %d, want 4", n)
	}
}

func TestLoadRejectsAnUnexplodedDirectory(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := identity.Load(dir); err == nil || !strings.Contains(err.Error(), "not an exploded repository") {
		t.Errorf("err = %v", err)
	}
}
