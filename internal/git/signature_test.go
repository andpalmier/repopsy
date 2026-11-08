package git

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// pgpFixture is a real detached OpenPGP signature from a throwaway ed25519 key
// that no keyring has, which is exactly the position of anyone examining a
// GitHub web-flow commit.
const (
	pgpFixture = `-----BEGIN PGP SIGNATURE-----

iJEEABYKADkWIQQv66/tdCvmdkxOE/zTVD5WnFpvbgUCau9XoRsUgAAAAAAEAA5t
YW51MiwyLjUrMS4xMiwwLDMACgkQ01Q+Vpxab2423QD9HdD8POV6MjoGf/F8bhr3
9j2MEZ/SBI8mLpm/9bzuOnUBAOB3c6GG32EPVGnp65P7o0vJ/ghPiw2rnvPwlRFn
++YD
=+eQ6
-----END PGP SIGNATURE-----`
	pgpFixtureKeyID       = "D3543E569C5A6F6E"
	pgpFixtureFingerprint = "2FEBAFED742BE6764C4E13FCD3543E569C5A6F6E"
)

// isolateGitConfig keeps the examiner's own git configuration out of a test,
// since whether a signature verifies depends on it.
func isolateGitConfig(t *testing.T) {
	t.Helper()
	empty := filepath.Join(t.TempDir(), "gitconfig")
	if err := os.WriteFile(empty, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GIT_CONFIG_GLOBAL", empty)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
}

// signedObject writes a commit object carrying armor as its signature and
// points the current branch at it.
func (r *repo) signedObject(armor, committer string) string {
	r.Helper()
	emptyTree := r.gitStdin("", "mktree")
	var b strings.Builder
	b.WriteString("tree " + emptyTree + "\n")
	b.WriteString("author T <t@example.com> 1700000000 +0000\n")
	b.WriteString("committer " + committer + " 1700000000 +0000\n")
	for i, line := range strings.Split(armor, "\n") {
		if i == 0 {
			b.WriteString("gpgsig " + line + "\n")
		} else {
			b.WriteString(" " + line + "\n")
		}
	}
	b.WriteString("\nsigned\n")
	hash := r.gitStdin(b.String(), "hash-object", "-t", "commit", "-w", "--stdin")
	r.Git("update-ref", "HEAD", hash)
	return hash
}

func TestParseSignatureKinds(t *testing.T) {
	tests := map[string]string{
		"":                                       "",
		"-----BEGIN SIGNED MESSAGE-----\nabc":    SigX509,
		"-----BEGIN SOMETHING ELSE-----\nabc":    SigUnknown,
		"-----BEGIN PGP SIGNATURE-----\n\n!!!\n": SigGPG,
		"-----BEGIN SSH SIGNATURE-----\nU1NI\n":  SigSSH,
	}
	for armor, want := range tests {
		// Malformed bodies still report the type, and never panic.
		if got := ParseSignature(armor); got.Type != want {
			t.Errorf("%q: Type = %q, want %q", armor, got.Type, want)
		}
	}
}

// sshSigner creates an SSH key and returns its paths and fingerprint.
func sshSigner(t *testing.T) (key, pub, fingerprint string) {
	t.Helper()
	if _, err := exec.LookPath("ssh-keygen"); err != nil {
		t.Skip("ssh-keygen not available")
	}
	key = filepath.Join(t.TempDir(), "key")
	if out, err := exec.Command("ssh-keygen", "-q", "-t", "ed25519", "-N", "", "-C", "signer@example.com", "-f", key).CombinedOutput(); err != nil {
		t.Fatalf("ssh-keygen: %v\n%s", err, out)
	}
	out, err := exec.Command("ssh-keygen", "-l", "-f", key+".pub").Output()
	if err != nil {
		t.Fatal(err)
	}
	return key, key + ".pub", strings.Fields(string(out))[1]
}

func (r *repo) sshSignedCommit(key string) string {
	r.Helper()
	r.Write("a.txt", "signed\n", 0o644)
	r.Git("add", "-A")
	r.Git("-c", "gpg.format=ssh", "-c", "user.signingkey="+key, "commit", "-S", "-m", "ssh signed")
	return r.Git("rev-parse", "HEAD")
}

func TestSSHSignatureIsReportedWithoutAnAllowedSignersFile(t *testing.T) {
	isolateGitConfig(t)
	key, pub, fingerprint := sshSigner(t)
	b := newRepo(t)
	b.sshSignedCommit(key)

	listed, err := b.open().ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	s := listed.Commits[0].Signature
	// git itself says "N" here, the same as for an unsigned commit.
	if !s.Signed() || s.Type != SigSSH || s.IssuerFingerprint != fingerprint || s.Algorithm != "ssh-ed25519" {
		t.Errorf("signature = %+v, want SSH %s", s, fingerprint)
	}
	if s.Verified() {
		t.Error("reported verified with no allowed signers file")
	}

	allowed := filepath.Join(t.TempDir(), "allowed")
	pubKey, _ := os.ReadFile(pub)
	if err := os.WriteFile(allowed, []byte("signer@example.com "+string(pubKey)), 0o644); err != nil {
		t.Fatal(err)
	}

	// The repository's own configuration is the subject's, not the examiner's,
	// so a file it names cannot vouch for its signatures.
	b.Git("config", "gpg.ssh.allowedSignersFile", allowed)
	listed, err = b.open().ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if s := listed.Commits[0].Signature; s.Verified() {
		t.Errorf("the repository's own allowed-signers file verified its signature: %+v", s)
	}

	// The examiner's file verifies it, and git names the principal.
	repo := b.open()
	repo.SetAllowedSigners(allowed)
	listed, err = repo.ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	s = listed.Commits[0].Signature
	if s.Status != "G" || s.Signer != "signer@example.com" || s.Type != SigSSH {
		t.Errorf("with allowed signers: %+v", s)
	}
}

func TestRepositoryConfigCannotRunAProgram(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a shell script")
	}
	isolateGitConfig(t)
	b := newRepo(t)
	b.WithCommits(1)
	marker := filepath.Join(t.TempDir(), "ran")
	script := filepath.Join(t.TempDir(), "evil.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\ntouch "+marker+"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"gpg.program", "gpg.openpgp.program", "core.fsmonitor"} {
		b.Git("config", k, script)
	}
	b.signedObject(pgpFixture, "GitHub <noreply@github.com>")

	listed, err := b.open().ListCommits(context.Background(), ListOptions{})
	if err != nil {
		t.Fatalf("ListCommits: %v", err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Fatal("a program named by the repository's own configuration was run")
	}

	// What can be known without the key still is.
	s := listed.Commits[0].Signature
	if s.Type != SigGPG || s.IssuerKeyID != pgpFixtureKeyID || s.IssuerFingerprint != pgpFixtureFingerprint ||
		s.Algorithm != "EdDSA" || s.Verified() {
		t.Errorf("signature = %+v", s)
	}
}
