package secrets

import (
	"context"
	"strings"
	"testing"

	"github.com/andpalmier/repopsy/v2/internal/git"
	"github.com/andpalmier/repopsy/v2/internal/gittest"
)

// Credential-shaped test values are assembled at run time, so no literal in
// this file looks like a credential to a secret scanner.
var (
	awsKey       = "AK" + "IA" + "Q3EGRBJ7XK2MN5PV"
	ghToken      = "gh" + "p_" + strings.Repeat("aB3d", 9)
	anthropicKey = "sk-" + "ant-" + "api03-" + strings.Repeat("Zq9_", 12)
	openaiKey    = "sk-" + "proj-" + strings.Repeat("Hk2m", 10)
	googleKey    = "AI" + "za" + "SyD3" + strings.Repeat("x7Pq", 7) + "abc"
	telegram     = "123456789:" + "AA" + strings.Repeat("Fq7_", 8) + "Z"
	zhipu        = strings.Repeat("0a1b2c3d", 4) + "." + "Qw3rTy7uIo9pAs2D"
	privateKey   = "-----BEGIN " + "OPENSSH PRIVATE KEY-----"
)

func TestScanLineRecognisesShapes(t *testing.T) {
	tests := map[string]string{
		awsKey:                                 "aws-access-key-id",
		ghToken:                                "github-token",
		anthropicKey:                           "anthropic-api-key",
		openaiKey:                              "openai-api-key",
		googleKey:                              "google-api-key",
		telegram:                               "telegram-bot-token",
		zhipu:                                  "zhipu-api-key",
		privateKey:                             "private-key",
		"LT" + "AI" + "5tQ8wRxZ2bN7mK4p":       "alibaba-access-key-id",
		"AK" + "ID" + "Hq7wE2rT9yU4iO1p":       "tencent-secret-id",
		"xo" + "xb-" + "1234567890-abcdefghij": "slack-token",
	}
	for value, kind := range tests {
		got := scanLine(`token = "` + value + `"`)
		if len(got) != 1 || got[0].kind != kind {
			t.Errorf("%s: got %+v, want one %s", kind, got, kind)
		}
	}
}

func TestScanLineAvoidsLookalikes(t *testing.T) {
	for _, line := range []string{
		"the risk-assessment-framework-for-everyone-involved",
		"AKIAshort",
		"sk-short",
		"commit 0123456789abcdef0123456789abcdef", // a hash, not a key
	} {
		if got := scanLine(line); len(got) != 0 {
			t.Errorf("%q matched %+v", line, got)
		}
	}
}

func TestMaskNeverRevealsTheValue(t *testing.T) {
	m := Mask(ghToken)
	if m != ghToken[:6]+"..."+ghToken[len(ghToken)-4:] || strings.Contains(m, ghToken[6:len(ghToken)-4]) {
		t.Errorf("Mask = %q", m)
	}
	if Mask("short-value") != "sho..." {
		t.Errorf("short value mask = %q", Mask("short-value"))
	}
	if len(Fingerprint(ghToken)) != 12 {
		t.Errorf("Fingerprint = %q", Fingerprint(ghToken))
	}
}

func TestPlaceholder(t *testing.T) {
	for _, v := range []string{"AKIAXXXXXXXXXXXXXXXX", "sk-your-key-goes-here", "AKIAIOSFODNN7EXAMPLE", "ghp_000000000000"} {
		if !isPlaceholder(v) {
			t.Errorf("%q not flagged as a placeholder", v)
		}
	}
	if isPlaceholder(awsKey) {
		t.Errorf("%q flagged as a placeholder", awsKey)
	}
}

func TestScan(t *testing.T) {
	b := gittest.New(t)
	b.Write("config.py", "KEY = '"+awsKey+"'\n", 0o644)
	b.Write("README", "use AKIAXXXXXXXXXXXXXXXX here\n", 0o644)
	added := b.Commit("add config")
	b.Write("config.py", "KEY = os.environ['KEY']\n", 0o644)
	b.Commit("move to env")
	main := b.Branch()

	// A contributor's branch, reachable only from a pull request ref.
	b.Git("checkout", "-q", "-b", "contrib")
	b.Write("bot.py", "TOKEN = '"+ghToken+"'\n", 0o644)
	prCommit := b.Commit("contrib")
	b.Git("update-ref", "refs/pull/1/head", prCommit)
	b.Git("checkout", "-q", main)
	b.Git("branch", "-D", "contrib")

	repo, err := git.Open(b.Dir)
	if err != nil {
		t.Fatal(err)
	}
	listed, err := repo.ListCommits(context.Background(), git.ListOptions{Tips: []string{main, "refs/pull/1/head"}})
	if err != nil {
		t.Fatal(err)
	}
	findings, err := Scan(context.Background(), repo, listed.Commits, []Tip{
		{Label: main, Rev: "refs/heads/" + main},
		{Label: "refs/pull/1/head", Rev: "refs/pull/1/head"},
	})
	if err != nil {
		t.Fatalf("Scan: %v", err)
	}

	byKind := map[string]Finding{}
	for _, f := range findings {
		byKind[f.Kind+map[bool]string{true: "/placeholder"}[f.Placeholder]] = f
	}
	aws := byKind["aws-access-key-id"]
	if len(aws.Occurrences) != 1 || aws.Occurrences[0].Commit != added || aws.Occurrences[0].Path != "config.py" {
		t.Fatalf("aws finding = %+v", aws)
	}
	if aws.Fingerprint != Fingerprint(awsKey) || aws.Occurrences[0].OnlyPullRequests {
		t.Errorf("aws finding = %+v", aws)
	}
	// Removed from main, but the pull request ref branched off after the
	// removal, so neither tip still has it.
	if len(aws.Occurrences[0].LiveAt) != 0 {
		t.Errorf("aws LiveAt = %v", aws.Occurrences[0].LiveAt)
	}
	if _, ok := byKind["aws-access-key-id/placeholder"]; !ok {
		t.Errorf("the placeholder key was not flagged: %+v", findings)
	}

	gh := byKind["github-token"]
	if len(gh.Occurrences) != 1 || !gh.Occurrences[0].OnlyPullRequests {
		t.Errorf("github finding = %+v", gh)
	}
	if strings.Join(gh.Occurrences[0].LiveAt, ",") != "refs/pull/1/head" {
		t.Errorf("github LiveAt = %v", gh.Occurrences[0].LiveAt)
	}
}
