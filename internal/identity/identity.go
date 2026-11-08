// Package identity interprets the name and email pairs git records: what a
// placeholder looks like, and what a GitHub noreply address says about the
// account behind it.
//
// It deliberately stops at provenance. Nothing here infers where a person is
// or profiles them; it only says whether an identity is well-formed and which
// account an address names.
package identity

import (
	"fmt"
	"regexp"
	"sort"
	"strings"
)

// WebFlowEmail is the committer email GitHub uses for commits it creates
// itself, such as merges and edits made in the web interface.
const WebFlowEmail = "noreply@github.com"

// placeholderDomains are reserved for documentation or name no real mailbox.
var placeholderDomains = map[string]bool{
	"example.com": true, "example.org": true, "example.net": true,
	"localhost": true, "localhost.localdomain": true, "localdomain": true,
	"local": true, "invalid": true, "test": true, "example": true, "(none)": true,
}

// placeholderSuffixes are reserved top-level domains (RFC 2606, RFC 6762).
var placeholderSuffixes = []string{".local", ".localdomain", ".invalid", ".example", ".test", ".localhost"}

// Placeholder returns why an identity looks like a placeholder or a
// misconfigured client rather than a real person's identity, or "".
func Placeholder(name, email string) string {
	var reasons []string
	if strings.TrimSpace(name) == "" {
		reasons = append(reasons, "empty name")
	}
	email = strings.TrimSpace(email)
	local, domain, hasAt := strings.Cut(strings.ToLower(email), "@")
	switch {
	case email == "":
		reasons = append(reasons, "empty email")
	case !hasAt || local == "" || domain == "":
		reasons = append(reasons, "malformed email")
	case placeholderDomains[domain]:
		reasons = append(reasons, "placeholder domain "+domain)
	case hasSuffix(domain, placeholderSuffixes):
		reasons = append(reasons, "reserved domain "+domain)
	case !strings.Contains(domain, "."):
		// git's fallback when no email is configured is user@hostname.
		reasons = append(reasons, "machine-local address (no domain)")
	}
	return strings.Join(reasons, ", ")
}

func hasSuffix(s string, suffixes []string) bool {
	for _, suffix := range suffixes {
		if strings.HasSuffix(s, suffix) {
			return true
		}
	}
	return false
}

// noreplyPattern matches GitHub's private commit email, with or without the
// numeric account ID GitHub has prefixed since 2017.
var noreplyPattern = regexp.MustCompile(`^(?:([0-9]+)\+)?([A-Za-z0-9](?:[A-Za-z0-9-]*[A-Za-z0-9])?(?:\[bot\])?)@users\.noreply\.github\.com$`)

// Noreply is what a GitHub noreply address says about its account.
type Noreply struct {
	// ID is the numeric account ID, which survives renames; empty for the
	// older form without one.
	ID string

	// Handle is the account name, lowercased, as it was when the address was
	// issued.
	Handle string
}

// ParseNoreply reads a GitHub noreply address.
func ParseNoreply(email string) (Noreply, bool) {
	m := noreplyPattern.FindStringSubmatch(strings.ToLower(strings.TrimSpace(email)))
	if m == nil {
		return Noreply{}, false
	}
	return Noreply{ID: m[1], Handle: m[2]}, true
}

// NormalizeEmail lowercases and trims an email, so trivial variations of one
// address compare equal.
func NormalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

// IsWebFlow reports whether an identity is GitHub's own committer identity.
func IsWebFlow(name, email string) bool {
	return NormalizeEmail(email) == WebFlowEmail && name == "GitHub"
}

// NoreplyConflicts finds GitHub noreply addresses that disagree about an
// account: one account ID under several handles, which a rename produces but
// so does a forged address, or one handle under several account IDs, which no
// rename produces.
func NoreplyConflicts(emails []string) []string {
	handlesByID := map[string]map[string]bool{}
	idsByHandle := map[string]map[string]bool{}
	for _, e := range emails {
		n, ok := ParseNoreply(e)
		if !ok || n.ID == "" {
			continue
		}
		add(handlesByID, n.ID, n.Handle)
		add(idsByHandle, n.Handle, n.ID)
	}

	var found []string
	for id, handles := range handlesByID {
		if len(handles) > 1 {
			found = append(found, fmt.Sprintf("account ID %s appears with handles: %s", id, joinSorted(handles)))
		}
	}
	for handle, ids := range idsByHandle {
		if len(ids) > 1 {
			found = append(found, fmt.Sprintf("handle %s appears with account IDs: %s", handle, joinSorted(ids)))
		}
	}
	sort.Strings(found)
	return found
}

func add(m map[string]map[string]bool, key, value string) {
	if m[key] == nil {
		m[key] = map[string]bool{}
	}
	m[key][value] = true
}

func joinSorted(set map[string]bool) string {
	values := make([]string, 0, len(set))
	for v := range set {
		values = append(values, v)
	}
	sort.Strings(values)
	return strings.Join(values, ", ")
}
