package git

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
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
}

// State is the part of a repository that lives outside its object database.
type State struct {
	// Config is the repository's local configuration, verbatim, including
	// comments — which "git config --list" would discard.
	Config string

	// Hooks are the installed hooks, excluding git's inert *.sample files.
	Hooks []Hook
}

// ReadState reads the repository's unversioned state. A missing file is not an
// error: the record simply reports what was there.
func (r *Repository) ReadState() (State, error) {
	if r.GitDir == "" {
		return State{}, fmt.Errorf("the repository's git directory could not be located")
	}

	var state State
	if config, err := os.ReadFile(filepath.Join(r.GitDir, "config")); err == nil {
		state.Config = string(config)
	}

	entries, err := os.ReadDir(filepath.Join(r.GitDir, "hooks"))
	if err != nil {
		// No hooks directory at all is normal.
		return state, nil
	}

	for _, entry := range entries {
		// git ships *.sample hooks that it never runs.
		if entry.IsDir() || strings.HasSuffix(entry.Name(), ".sample") {
			continue
		}
		if hook, err := readHook(filepath.Join(r.GitDir, "hooks"), entry.Name()); err == nil {
			state.Hooks = append(state.Hooks, hook)
		}
	}
	sort.Slice(state.Hooks, func(i, j int) bool { return state.Hooks[i].Name < state.Hooks[j].Name })
	return state, nil
}

func readHook(dir, name string) (Hook, error) {
	content, err := os.ReadFile(filepath.Join(dir, name))
	if err != nil {
		return Hook{}, err
	}
	info, err := os.Stat(filepath.Join(dir, name))
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
