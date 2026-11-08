// Package git reads a git repository through the git command line: its refs,
// its commits, its trees, and the unversioned state around them.
//
// Nothing here knows what repopsy's output looks like. Rendering and naming
// belong to the snapshot package.
package git

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
)

// Repository represents an opened git repository.
type Repository struct {
	// Path is the absolute path to the repository root
	Path string

	// GitDir is the absolute path to the repository's git directory, which is
	// Path/.git for a work tree and Path itself for a bare repository. Its
	// contents are not versioned, so no commit walk reaches them.
	GitDir string

	// Shallow lists the boundary commits of a shallow clone, sorted, or is
	// empty for a complete repository. git records no parents for these, so
	// without this they are indistinguishable from root commits.
	Shallow []string

	// AllowedSigners is an allowed-signers file used to verify SSH signatures.
	// Empty means the examiner's own git configuration decides. Set it with
	// SetAllowedSigners.
	AllowedSigners string

	// pinned overrides configuration the examined repository could use to make
	// git run a program or vouch for its own signatures. See pinnedConfig.
	pinned []string

	// sshAllowedSigners records whether the examiner has an allowed-signers
	// file of their own, so an SSH verdict can be read correctly.
	sshAllowedSigners bool

	shallowSet map[string]bool
}

// Open opens and validates the git repository containing path.
//
// The repository is identified by its top level, so naming any directory inside
// a work tree names the repository itself rather than the subtree. Bare
// repositories have no work tree and are their own root. See docs/adr/0002.
func Open(path string) (*Repository, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("failed to resolve path: %w", err)
	}

	// Stat before asking git, so a missing or non-directory path reports what is
	// actually wrong instead of "not a git repository".
	info, err := os.Stat(absPath)
	if err != nil {
		return nil, fmt.Errorf("failed to access path: %w", err)
	}
	if !info.IsDir() {
		return nil, fmt.Errorf("path is not a directory: %s", absPath)
	}

	root, err := resolveRoot(absPath)
	if err != nil {
		return nil, err
	}

	// Best effort: everything except the repository-state record works without it.
	gitDir, _ := gitOutput(context.Background(), root, "rev-parse", "--absolute-git-dir")

	pinned, values := pinnedConfig(root)
	repo := &Repository{Path: root, GitDir: gitDir, pinned: pinned}
	repo.sshAllowedSigners = values["gpg.ssh.allowedsignersfile"] != ""
	repo.Shallow = readShallow(root, gitDir)
	repo.shallowSet = make(map[string]bool, len(repo.Shallow))
	for _, h := range repo.Shallow {
		repo.shallowSet[h] = true
	}
	return repo, nil
}

// pinnedKeys are configuration keys that name a program git runs, or a file
// that decides whether a signature verifies, each with git's own default.
//
// A repository's local configuration belongs to the subject of the
// examination. Working from a copy of the original repository, as the README
// recommends for reflogs, hands that configuration to every git command repopsy
// runs: gpg.program would run on the first signed commit, and an
// allowedSignersFile chosen by the repository would make its own forged SSH
// signatures verify.
var pinnedKeys = []struct{ key, fallback string }{
	{"gpg.program", "gpg"},
	{"gpg.openpgp.program", "gpg"},
	{"gpg.x509.program", "gpgsm"},
	{"gpg.ssh.program", "ssh-keygen"},
	{"gpg.ssh.allowedSignersFile", ""},
	{"gpg.ssh.revocationFile", ""},
}

// pinnedConfig returns "-c key=value" arguments that fix each pinned key to the
// examiner's own global or system value, or git's default, so the examined
// repository's local configuration cannot override it. core.fsmonitor names a
// program too and is never needed to read history, so it is switched off.
func pinnedConfig(dir string) (args []string, values map[string]string) {
	// --global and --system read only those files, never the repository's.
	// Read lowest precedence first, so a global value replaces a system one.
	values = map[string]string{}
	for _, scope := range []string{"--system", "--global"} {
		out, err := gitOutput(context.Background(), dir, "config", scope, "-z", "--get-regexp", `^gpg\.`)
		if err != nil {
			continue // no such file, or no gpg settings in it
		}
		for record := range strings.SplitSeq(out, "\x00") {
			key, value, _ := strings.Cut(record, "\n")
			if value != "" {
				values[strings.ToLower(key)] = value
			}
		}
	}

	args = []string{"-c", "core.fsmonitor=false"}
	for _, k := range pinnedKeys {
		value, ok := values[strings.ToLower(k.key)]
		if !ok {
			value = k.fallback
		}
		args = append(args, "-c", k.key+"="+value)
	}
	return args, values
}

// readShallow returns the boundary commits of a shallow clone, or nil.
func readShallow(root, gitDir string) []string {
	if out, err := gitOutput(context.Background(), root, "rev-parse", "--is-shallow-repository"); err != nil || out != "true" {
		return nil
	}
	data, err := os.ReadFile(filepath.Join(gitDir, "shallow"))
	if err != nil {
		return nil
	}
	hashes := strings.Fields(string(data))
	sort.Strings(hashes)
	return hashes
}

// SetAllowedSigners makes SSH signatures verify against file.
func (r *Repository) SetAllowedSigners(file string) {
	r.AllowedSigners = file
	r.sshAllowedSigners = r.sshAllowedSigners || file != ""
}

// IsShallowBoundary reports whether hash is a commit whose parents a shallow
// clone cut off.
func (r *Repository) IsShallowBoundary(hash string) bool {
	return r.shallowSet[hash]
}

// command builds a git invocation in the repository with the pinned
// configuration applied. Every git command repopsy runs against the examined
// repository goes through here.
func (r *Repository) command(ctx context.Context, args ...string) *exec.Cmd {
	full := append([]string{}, r.pinned...)
	if r.AllowedSigners != "" {
		full = append(full, "-c", "gpg.ssh.allowedSignersFile="+r.AllowedSigners)
	}
	cmd := exec.CommandContext(ctx, "git", append(full, args...)...)
	cmd.Dir = r.Path
	return cmd
}

// output runs a git command in the repository and returns its trimmed output.
func (r *Repository) output(ctx context.Context, args ...string) (string, error) {
	return runOutput(r.command(ctx, args...), args[0])
}

// resolveRoot finds the root repopsy should treat dir as naming. git resolves
// symlinks along the way, so the result is canonical.
func resolveRoot(dir string) (string, error) {
	top, topErr := gitOutput(context.Background(), dir, "rev-parse", "--show-toplevel")
	if topErr == nil && top != "" {
		return top, nil
	}

	// --show-toplevel fails inside a bare repository ("must be run in a work
	// tree"), so fall back to asking whether that is why.
	if bare, err := gitOutput(context.Background(), dir, "rev-parse", "--is-bare-repository"); err == nil && bare == "true" {
		return dir, nil
	}

	// Report git's own diagnosis rather than assuming the path is not a
	// repository. git distinguishes cases that matter: a directory it refuses
	// to read is not the same as a directory holding no repository, and saying
	// the wrong one sends the reader to check the wrong thing. Its refusal on
	// ownership grounds inside a container is the common example.
	if topErr != nil {
		return "", fmt.Errorf("cannot read a git repository at %s: %w", dir, topErr)
	}
	return "", fmt.Errorf("not a git repository: %s", dir)
}

// gitOutput runs git in dir and returns its trimmed standard output. git's own
// stderr is surfaced in the error, which is the most useful thing it produces.
func gitOutput(ctx context.Context, dir string, args ...string) (string, error) {
	cmd := exec.CommandContext(ctx, "git", args...)
	cmd.Dir = dir
	return runOutput(cmd, args[0])
}

// runOutput runs cmd and returns its trimmed standard output. Standard error is
// captured, never passed through, so git's warnings cannot reach the terminal;
// it is surfaced only in the error of a command that failed.
func runOutput(cmd *exec.Cmd, name string) (string, error) {
	output, err := cmd.Output()
	if err != nil {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
			return "", fmt.Errorf("git %s failed: %s", name, strings.TrimSpace(string(exitErr.Stderr)))
		}
		return "", fmt.Errorf("git %s failed: %w", name, err)
	}
	return strings.TrimSpace(string(output)), nil
}

// Ref is one ref, as selected for extraction.
type Ref struct {
	// Name is the full ref name, e.g. refs/heads/main or refs/pull/12/head.
	Name string

	// Type is the type of object the ref ultimately points at, after peeling
	// annotated tags. Only refs reaching a commit have history to extract.
	Type string
}

// Branch reports the branch name of a ref under refs/heads/, if it is one.
func (r Ref) Branch() (string, bool) {
	return strings.CutPrefix(r.Name, "refs/heads/")
}

// Namespace is the first level below refs/, e.g. refs/pull for
// refs/pull/12/head, which is how refs are grouped when reporting scope.
func (r Ref) Namespace() string {
	parts := strings.SplitN(r.Name, "/", 3)
	if len(parts) < 3 {
		return r.Name
	}
	return parts[0] + "/" + parts[1]
}

// ListRefs returns the refs matching any of patterns, sorted by name, or every
// ref when no pattern is given. Patterns follow git for-each-ref: a pattern
// matches a ref literally, as a prefix up to a slash, or as a glob in which *
// does not cross a slash.
func (r *Repository) ListRefs(ctx context.Context, patterns []string) ([]Ref, error) {
	args := []string{"for-each-ref", "--format=%(refname)%00%(objecttype)%00%(*objecttype)"}
	if len(patterns) > 0 {
		args = append(args, "--")
		args = append(args, patterns...)
	}
	output, err := r.output(ctx, args...)
	if err != nil {
		return nil, fmt.Errorf("failed to list refs: %w", err)
	}
	if output == "" {
		return nil, nil
	}

	var refs []Ref
	for line := range strings.SplitSeq(output, "\n") {
		f := strings.Split(line, fieldSep)
		if len(f) < 3 {
			continue
		}
		ref := Ref{Name: f[0], Type: f[1]}
		if f[2] != "" {
			ref.Type = f[2]
		}
		refs = append(refs, ref)
	}
	return refs, nil
}

// RevList returns the hashes of the commits reachable from rev, oldest first,
// limited to the newest limit when limit is positive. It is far cheaper than
// ListCommits, so refs are walked with it and full metadata is fetched once per
// distinct commit.
func (r *Repository) RevList(ctx context.Context, rev string, limit int) ([]string, error) {
	args := []string{"rev-list", "--reverse"}
	if limit > 0 {
		args = append(args, fmt.Sprintf("-n%d", limit))
	}
	args = append(args, "--end-of-options", rev)
	output, err := r.output(ctx, args...)
	if err != nil {
		return nil, err
	}
	if output == "" {
		return nil, nil
	}
	return strings.Split(output, "\n"), nil
}
