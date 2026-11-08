package git

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// maxHookBytes caps how much of a hook is recorded. Hooks are small scripts;
// the cap exists so a pathological file cannot dominate the record.
const maxHookBytes = 4096

// Hook is an executable git runs on repository events. Hooks are not versioned
// and never appear in any commit, so a tool that only walks history cannot see
// them — and a malicious hook is a real attack.
type Hook struct {
	Name       string
	Size       int64
	SHA256     string
	Executable bool
	Content    string
	Truncated  bool

	// Symlink is the target of a hook that is a symbolic link. Its content is
	// not read: following it could copy an unrelated local file into the record.
	Symlink string
}

// State is the part of a repository that lives outside its object database.
type State struct {
	// Config is the repository's local configuration, verbatim, including
	// comments — which "git config --list" would discard.
	Config string

	// ConfigSymlink is the target when config is a symbolic link. Its content
	// is not read, for the same reason hook symlinks are not followed.
	ConfigSymlink string

	// HooksPath is the directory git actually uses for hooks. It may come from
	// core.hooksPath rather than .git/hooks.
	HooksPath string

	// HooksExternal says HooksPath is outside the repository, so hook content is
	// not read. A repository must not be able to point repopsy at an arbitrary
	// local directory and have its files copied into the output.
	HooksExternal bool

	// Hooks are the installed hooks, excluding git's inert *.sample files.
	Hooks []Hook
}

// ReadState reads the repository's unversioned state. A missing file is not an
// error: the record simply reports what was there.
func (r *Repository) ReadState() (State, error) {
	if r.GitDir == "" {
		return State{}, fmt.Errorf("the repository's git directory could not be located")
	}

	root, err := os.OpenRoot(r.GitDir)
	if err != nil {
		return State{}, fmt.Errorf("failed to open the repository's git directory: %w", err)
	}
	defer func() { _ = root.Close() }()

	var state State
	info, err := root.Lstat("config")
	switch {
	case err == nil && info.Mode()&os.ModeSymlink != 0:
		target, err := root.Readlink("config")
		if err != nil {
			return State{}, fmt.Errorf("failed to read config symlink: %w", err)
		}
		state.ConfigSymlink = target
	case err == nil && info.Mode().IsRegular():
		config, err := root.ReadFile("config")
		if err != nil {
			return State{}, fmt.Errorf("failed to read config: %w", err)
		}
		state.Config = string(config)
	case err != nil && !errors.Is(err, fs.ErrNotExist):
		return State{}, fmt.Errorf("failed to inspect config: %w", err)
	}

	hooksPath, err := r.gitPath("hooks")
	if err != nil {
		return State{}, fmt.Errorf("failed to locate hooks directory: %w", err)
	}
	state.HooksPath = hooksPath
	if !within(r.Path, hooksPath) {
		state.HooksExternal = true
		return state, nil
	}

	hooks, err := os.OpenRoot(hooksPath)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return state, nil // no hooks directory at all is normal
		}
		return State{}, fmt.Errorf("failed to open hooks directory: %w", err)
	}
	defer func() { _ = hooks.Close() }()

	entries, err := fs.ReadDir(hooks.FS(), ".")
	if err != nil {
		return State{}, fmt.Errorf("failed to list hooks: %w", err)
	}

	for _, entry := range entries {
		// git ships *.sample hooks that it never runs.
		if strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}
		hook, err := readHook(hooks, entry.Name())
		if err != nil {
			if errors.Is(err, errSkipHook) {
				continue
			}
			return State{}, fmt.Errorf("failed to read hook %s: %w", entry.Name(), err)
		}
		state.Hooks = append(state.Hooks, hook)
	}
	sort.Slice(state.Hooks, func(i, j int) bool { return state.Hooks[i].Name < state.Hooks[j].Name })
	return state, nil
}

func within(root, p string) bool {
	if resolved, err := filepath.EvalSymlinks(root); err == nil {
		root = resolved
	}
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	rel, err := filepath.Rel(root, p)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

// gitPath returns the absolute path git uses for name in this repository.
func (r *Repository) gitPath(name string) (string, error) {
	out, err := r.output(context.Background(), "rev-parse", "--git-path", name)
	if err != nil {
		return "", err
	}
	if filepath.IsAbs(out) {
		return out, nil
	}
	return filepath.Join(r.Path, out), nil
}

func readHook(root *os.Root, name string) (Hook, error) {
	info, err := root.Lstat(name)
	if err != nil {
		return Hook{}, err
	}
	if info.Mode().IsDir() {
		return Hook{}, errSkipHook
	}
	if info.Mode()&os.ModeSymlink != 0 {
		target, err := root.Readlink(name)
		if err != nil {
			return Hook{}, err
		}
		return Hook{Name: name, Symlink: target}, nil
	}
	if !info.Mode().IsRegular() {
		return Hook{}, fmt.Errorf("not a regular file")
	}
	content, err := root.ReadFile(name)
	if err != nil {
		return Hook{}, err
	}

	// Hashed in full even when the recorded content is truncated.
	sum := sha256.Sum256(content)
	hook := Hook{
		Name:       name,
		Size:       info.Size(),
		SHA256:     hex.EncodeToString(sum[:]),
		Executable: info.Mode().Perm()&0o111 != 0,
		Content:    string(content),
	}
	if len(content) > maxHookBytes {
		hook.Content = string(content[:maxHookBytes])
		hook.Truncated = true
	}
	return hook, nil
}

var errSkipHook = errors.New("skip hook")
