package cmd

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/gittest"
	"github.com/andpalmier/repopsy/v2/internal/snapshot"
)

// explode runs the CLI and fails the test on a non-zero exit.
func explode(t *testing.T, args ...string) string {
	t.Helper()
	code, _, stderr := invoke(t, args...)
	if code != 0 {
		t.Fatalf("repopsy %v exited %d\n%s", args, code, stderr)
	}
	return stderr
}

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

// mirror builds a repository shaped like a GitHub mirror: two branches
// sharing history, and a pull request ref.
func mirror(t *testing.T) (*gittest.Repo, string) {
	t.Helper()
	r := gittest.New(t)
	r.Write("start.sh", "#!/bin/sh\necho hi\n", 0o755)
	r.Write("ua.py", "UA = 'Mozilla/5.0 test'\n", 0o644)
	r.Commit("init")
	main := r.Branch()
	r.Git("checkout", "-q", "-b", "feature")
	r.Write("f.txt", "feature\n", 0o644)
	r.Commit("feature work")
	r.Git("checkout", "-q", "-b", "contrib")
	r.Write("c.txt", "contribution\n", 0o644)
	pr := r.Commit("contribution")
	r.Git("checkout", "-q", main)
	r.Git("branch", "-D", "contrib")
	r.Git("update-ref", "refs/pull/7/head", pr)
	return r, main
}

func snapshots(t *testing.T, dir string) []string {
	t.Helper()
	found, err := filepath.Glob(filepath.Join(dir, "*", snapshot.MetadataFilename))
	if err != nil {
		t.Fatal(err)
	}
	return found
}

func TestRunTreeIsNeverExecutableByDefault(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no executable bit")
	}
	r := gittest.New(t)
	r.Write("start.sh", "#!/bin/sh\n", 0o755)
	r.Commit("script")

	for _, tc := range []struct {
		args []string
		exec bool
	}{{nil, false}, {[]string{"--preserve-modes"}, true}} {
		out := filepath.Join(t.TempDir(), "out")
		explode(t, append(append([]string{"-o", out}, tc.args...), r.Dir)...)
		snaps := snapshots(t, filepath.Join(out, snapshot.RefsDir, r.Branch()))
		info, err := os.Stat(filepath.Join(filepath.Dir(snaps[0]), "tree", "start.sh"))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm()&0o111 != 0; got != tc.exec {
			t.Errorf("%v: executable = %v, want %v", tc.args, got, tc.exec)
		}
		// The git mode is on the record either way.
		if !strings.Contains(read(t, snaps[0]), "100755  start.sh") {
			t.Errorf("%v: COMMIT_INFO.txt does not record the mode", tc.args)
		}
	}
}

func TestRunDefaultScopeHintsAtPullRequests(t *testing.T) {
	r, _ := mirror(t)
	out := filepath.Join(t.TempDir(), "out")
	stderr := explode(t, "-o", out, r.Dir)

	if !strings.Contains(stderr, "1 pull request ref(s) under refs/pull/ are not extracted") {
		t.Errorf("no hint about pull request refs:\n%s", stderr)
	}
	if _, err := os.Stat(filepath.Join(out, snapshot.OtherRefsDir)); err == nil {
		t.Error("pull request refs extracted without being asked for")
	}
	manifest := read(t, filepath.Join(out, "EXTRACTION.txt"))
	for _, want := range []string{"Unique commits: 2", "refs/pull                    1 refs,     0 extracted  (--refs 'refs/pull/*/head' adds 1)"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("EXTRACTION.txt missing %q:\n%s", want, manifest)
		}
	}
}

func TestRunRefsIncludesPullRequests(t *testing.T) {
	r, main := mirror(t)
	out := filepath.Join(t.TempDir(), "out")
	stderr := explode(t, "-o", out, "--refs", "refs/heads/", "--refs", "refs/pull/*/head", r.Dir)
	if strings.Contains(stderr, "not extracted") {
		t.Errorf("hint shown although pull requests were extracted:\n%s", stderr)
	}

	if n := len(snapshots(t, filepath.Join(out, snapshot.OtherRefsDir, "pull", "7", "head"))); n != 3 {
		t.Errorf("refs/pull/7/head has %d snapshots, want 3", n)
	}
	if n := len(snapshots(t, filepath.Join(out, snapshot.RefsDir, main))); n != 1 {
		t.Errorf("%s has %d snapshots, want 1", main, n)
	}
	manifest := read(t, filepath.Join(out, "EXTRACTION.txt"))
	for _, want := range []string{"Scope:          refs matching refs/heads/, refs/pull/*/head", "REFS\n----", "refs/pull/7/head", "Snapshots:      6", "Unique commits: 3"} {
		if !strings.Contains(manifest, want) {
			t.Errorf("EXTRACTION.txt missing %q", want)
		}
	}
}

func TestRunAllRefsSkipsWhatHasNoHistory(t *testing.T) {
	r, _ := mirror(t)
	r.Git("tag", "-a", "v1", "-m", "release")
	r.Git("update-ref", "refs/tags/a-tree", r.Git("rev-parse", "HEAD^{tree}"))
	r.Write("wip.txt", "stashed\n", 0o644)
	r.Git("add", "wip.txt")
	r.Git("stash")

	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, "--all-refs", "-n", "1", r.Dir)
	manifest := read(t, filepath.Join(out, "EXTRACTION.txt"))
	if !strings.Contains(manifest, "refs/tags/a-tree") || !strings.Contains(manifest, "points at a tree, not a commit") {
		t.Errorf("a ref without history was not reported:\n%s", manifest)
	}
	if strings.Contains(manifest, "refs/stash ") && !strings.Contains(manifest, "recorded in REFLOG.txt") {
		t.Errorf("stash handling:\n%s", manifest)
	}
	if _, err := os.Stat(filepath.Join(out, snapshot.OtherRefsDir, "stash")); err == nil {
		t.Error("the stash was extracted")
	}
	if n := len(snapshots(t, filepath.Join(out, snapshot.OtherRefsDir, "tags", "v1"))); n != 1 {
		t.Errorf("the annotated tag's history has %d snapshots", n)
	}
	// -n keeps each ref's newest commit.
	pull := snapshots(t, filepath.Join(out, snapshot.OtherRefsDir, "pull", "7", "head"))
	if len(pull) != 1 || !strings.Contains(read(t, pull[0]), "Subject:\ncontribution") {
		t.Errorf("-n 1 on refs/pull/7/head kept %d snapshots", len(pull))
	}
}

func TestRunDedupeStoresEachCommitOnce(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("needs symlinks")
	}
	r, main := mirror(t)
	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, "--refs", "refs/heads/", "--refs", "refs/pull/*/head", "--dedupe", r.Dir)

	stored, _ := filepath.Glob(filepath.Join(out, snapshot.CommitsDir, "*"))
	if len(stored) != 3 {
		t.Fatalf("commits/ holds %d snapshots, want 3", len(stored))
	}
	// Ref directories still resolve, through links.
	links := snapshots(t, filepath.Join(out, snapshot.OtherRefsDir, "pull", "7", "head"))
	if len(links) != 3 {
		t.Fatalf("pull ref resolves %d snapshots, want 3", len(links))
	}
	info, err := os.Lstat(filepath.Dir(links[0]))
	if err != nil || info.Mode()&os.ModeSymlink == 0 {
		t.Errorf("ref entry is not a symlink: %v %v", info, err)
	}
	if n := len(snapshots(t, filepath.Join(out, snapshot.RefsDir, main))); n != 1 {
		t.Errorf("%s resolves %d snapshots", main, n)
	}

	// The integrity record verifies through the link.
	if path, err := exec.LookPath("sha256sum"); err == nil {
		cmd := exec.Command(path, "-c", snapshot.ChecksumFilename)
		cmd.Dir = filepath.Dir(links[0])
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Errorf("sha256sum -c failed: %v\n%s", err, out)
		}
	}
	if !strings.Contains(read(t, filepath.Join(out, "EXTRACTION.txt")), "Layout:         each commit once under commits/") {
		t.Error("EXTRACTION.txt does not record the layout")
	}
}

func TestRunPathsLimitsTheTree(t *testing.T) {
	r := gittest.New(t)
	r.Write("src/a.go", "package a\n", 0o644)
	r.Write("docs/x.md", "x\n", 0o644)
	r.Commit("two dirs")
	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, "--paths", "src", r.Dir)
	dir := filepath.Dir(snapshots(t, filepath.Join(out, snapshot.RefsDir, r.Branch()))[0])
	if _, err := os.Stat(filepath.Join(dir, "tree", "src", "a.go")); err != nil {
		t.Error(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "tree", "docs")); err == nil {
		t.Error("docs/ extracted despite --paths src")
	}
	// The record of the commit is still complete.
	if !strings.Contains(read(t, filepath.Join(dir, snapshot.MetadataFilename)), "docs/x.md") {
		t.Error("COMMIT_INFO.txt lost a changed file outside --paths")
	}
}

func TestRunDryRunWritesNothing(t *testing.T) {
	r, _ := mirror(t)
	out := filepath.Join(t.TempDir(), "out")
	stderr := explode(t, "-o", out, "--dry-run", "--refs", "refs/heads/", "--refs", "refs/pull/*/head", r.Dir)
	if _, err := os.Stat(out); err == nil {
		t.Error("--dry-run created the output directory")
	}
	for _, want := range []string{"Dry run - nothing was written.", "refs/pull/7/head", "Snapshots:          6", "Distinct commits:   3", "with --dedupe"} {
		if !strings.Contains(stderr, want) {
			t.Errorf("dry run output missing %q:\n%s", want, stderr)
		}
	}
}

func TestRunJSONWritesEveryRecord(t *testing.T) {
	r := gittest.New(t).WithCommits(1)
	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, "--json", "--no-tree", "--date-skew", "7d",
		"--track", "content", "--track", "absent", "--scan-secrets", r.Dir)
	docs := map[string]map[string]any{}
	for _, name := range []string{"EXTRACTION.json", "TAGS.json", "IDENTITIES.json", "STRINGS.json", "SECRETS.json"} {
		var doc map[string]any
		if err := json.Unmarshal([]byte(read(t, filepath.Join(out, name))), &doc); err != nil {
			t.Errorf("%s: %v", name, err)
		}
		if doc["schema_version"] != float64(snapshot.SchemaVersion) {
			t.Errorf("%s schema_version = %v", name, doc["schema_version"])
		}
		docs[name] = doc
	}
	if n := len(docs["STRINGS.json"]["strings"].([]any)); n != 2 {
		t.Errorf("STRINGS.json tracks %d strings, want 2", n)
	}
	if docs["EXTRACTION.json"]["unique_commits"] != float64(1) {
		t.Errorf("unique_commits = %v", docs["EXTRACTION.json"]["unique_commits"])
	}

	// --no-tree: the records and the integrity record, but no tree.
	snap := filepath.Dir(snapshots(t, filepath.Join(out, snapshot.RefsDir, r.Branch()))[0])
	if _, err := os.Stat(filepath.Join(snap, snapshot.TreeDir)); err == nil {
		t.Error("tree/ written with --no-tree")
	}
	if sums := read(t, filepath.Join(snap, snapshot.ChecksumFilename)); !strings.Contains(sums, "tree/filea.txt") {
		t.Errorf("SHA256SUMS = %q", sums)
	}
	if !strings.Contains(read(t, filepath.Join(snap, snapshot.MetadataFilename)), "Not written (--no-tree)") {
		t.Error("COMMIT_INFO.txt does not say the tree was not written")
	}
	var commit map[string]any
	if err := json.Unmarshal([]byte(read(t, filepath.Join(snap, snapshot.MetadataJSONFilename))), &commit); err != nil {
		t.Fatal(err)
	}
	if commit["subject"] != "commit a" {
		t.Errorf("COMMIT_INFO.json subject = %v", commit["subject"])
	}
	// Without --json, none are written.
	plain := filepath.Join(t.TempDir(), "plain")
	explode(t, "-o", plain, r.Dir)
	if found, _ := filepath.Glob(filepath.Join(plain, "*.json")); len(found) != 0 {
		t.Errorf("JSON written without --json: %v", found)
	}
}

func TestRunReportsAShallowClone(t *testing.T) {
	src := gittest.New(t).WithCommits(3)
	dir := filepath.Join(t.TempDir(), "shallow")
	if out, err := exec.Command("git", "clone", "-q", "--depth", "1", "file://"+filepath.ToSlash(src.Dir), dir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	out := filepath.Join(t.TempDir(), "out")
	stderr := explode(t, "-o", out, dir)
	if !strings.Contains(stderr, "Shallow clone: history is cut off") {
		t.Errorf("no shallow warning:\n%s", stderr)
	}
	manifest := read(t, filepath.Join(out, "EXTRACTION.txt"))
	if !strings.Contains(manifest, "Shallow:        YES") || !strings.Contains(manifest, src.Git("rev-parse", "HEAD")) {
		t.Errorf("EXTRACTION.txt does not record the boundary:\n%s", manifest)
	}
	info := read(t, snapshots(t, filepath.Join(out, snapshot.RefsDir, src.Branch()))[0])
	if !strings.Contains(info, "(parents not present - shallow boundary)") || strings.Contains(info, "(root commit - no parents)") {
		t.Errorf("COMMIT_INFO.txt misreports the boundary:\n%s", info)
	}
}

// TestRunRewrittenCommitAnotherBranchReachesIsNotUnreachable: every branch is
// walked before any reflog is recovered from, so a commit one branch was reset
// away from but another still reaches is not reported as rewritten history.
func TestRunRewrittenCommitAnotherBranchReachesIsNotUnreachable(t *testing.T) {
	r := gittest.New(t).WithCommits(1)
	r.Git("checkout", "-q", "-b", "a-reset")
	r.Write("x.txt", "x\n", 0o644)
	kept := r.Commit("on both branches")
	r.Git("branch", "b-keeps")
	r.Git("reset", "-q", "--hard", "HEAD~1")

	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, "--include-rewritten", r.Dir)
	for _, info := range snapshots(t, filepath.Join(out, snapshot.RefsDir, "a-reset")) {
		if strings.Contains(read(t, info), kept) {
			t.Errorf("%s was recovered for a-reset although b-keeps reaches it", kept[:7])
		}
	}
	if !strings.Contains(read(t, filepath.Join(out, "EXTRACTION.txt")), "b-keeps                                  2 commits, 2 snapshots\n") {
		t.Error("b-keeps lost its commit")
	}
}

func TestRunIdentitiesSubcommand(t *testing.T) {
	var dirs []string
	for _, email := range []string{"shared@company.io", "SHARED@company.io"} {
		r := gittest.New(t)
		r.Git("config", "user.email", email)
		r.Write("a.txt", email, 0o644)
		r.Git("add", "-A")
		r.Git("-c", "user.email="+email, "commit", "-q", "-m", "x")
		out := filepath.Join(t.TempDir(), "out")
		explode(t, "-o", out, r.Dir)
		dirs = append(dirs, out)
	}

	code, stdout, stderr := invoke(t, append([]string{"identities"}, dirs...)...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, stderr)
	}
	// gittest pins the identity through the environment, so both
	// repositories share test@example.com.
	if !strings.Contains(stdout, "email:test@example.com  in 2 repositories") {
		t.Errorf("stdout:\n%s", stdout)
	}

	code, stdout, _ = invoke(t, append([]string{"identities", "--json"}, dirs...)...)
	var doc struct {
		Overlaps []struct{ Key string } `json:"overlaps"`
	}
	if code != 0 || json.Unmarshal([]byte(stdout), &doc) != nil || len(doc.Overlaps) != 1 {
		t.Errorf("JSON: exit %d\n%s", code, stdout)
	}

	if code, _, stderr := invoke(t, "identities", dirs[0]); code != 1 || !strings.Contains(stderr, "at least two") {
		t.Errorf("one directory: exit %d, %s", code, stderr)
	}
}

// TestRunBranchSharingATagsNameKeepsItsDirectory: git disambiguates such a
// branch's short name ("refs/heads/main"), which used to become its directory,
// so where a branch landed depended on unrelated refs.
func TestRunBranchSharingATagsNameKeepsItsDirectory(t *testing.T) {
	r := gittest.New(t).WithCommits(1)
	main := r.Branch()
	r.Git("checkout", "-q", "-b", "heads/"+main)
	r.Write("extra.txt", "x", 0o644)
	r.Commit("only on heads/" + main)
	r.Git("tag", main, "refs/heads/"+main)

	out := filepath.Join(t.TempDir(), "out")
	explode(t, "-o", out, r.Dir)
	if n := len(snapshots(t, filepath.Join(out, snapshot.RefsDir, main))); n != 1 {
		t.Errorf("%s has %d snapshots, want 1", main, n)
	}
	if n := len(snapshots(t, filepath.Join(out, snapshot.RefsDir, "heads", main))); n != 2 {
		t.Errorf("heads/%s has %d snapshots, want 2", main, n)
	}
}
