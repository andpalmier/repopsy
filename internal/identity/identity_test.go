package identity

import (
	"strings"
	"testing"
)

func TestPlaceholder(t *testing.T) {
	tests := []struct {
		name, email string
		want        string // substring; "" means a real-looking identity
	}{
		{"Alice", "alice@company.io", ""},
		{"Alice", "13154679+alice@users.noreply.github.com", ""},
		{"Dev", "developer@example.com", "placeholder domain"},
		{"Dev", "dev@EXAMPLE.org", "placeholder domain"},
		{"root", "root@buildbox", "machine-local"},
		{"x", "x@mac.local", "reserved domain"},
		{"x", "x@localhost", "placeholder domain"},
		{"x", "", "empty email"},
		{"x", "not-an-email", "malformed"},
		{"", "a@b.com", "empty name"},
	}
	for _, tc := range tests {
		got := Placeholder(tc.name, tc.email)
		if tc.want == "" && got != "" || !strings.Contains(got, tc.want) {
			t.Errorf("Placeholder(%q, %q) = %q, want %q", tc.name, tc.email, got, tc.want)
		}
	}
}

func TestParseNoreply(t *testing.T) {
	for email, want := range map[string]Noreply{
		"13154679+AndPalmier@users.noreply.github.com":      {ID: "13154679", Handle: "andpalmier"},
		"andpalmier@users.noreply.github.com":               {Handle: "andpalmier"},
		"49699333+dependabot[bot]@users.noreply.github.com": {ID: "49699333", Handle: "dependabot[bot]"},
	} {
		got, ok := ParseNoreply(email)
		if !ok || got != want {
			t.Errorf("ParseNoreply(%q) = %+v, %v", email, got, ok)
		}
	}
	for _, email := range []string{"noreply@github.com", "a@users.noreply.github.com.evil.com", "+x@users.noreply.github.com"} {
		if _, ok := ParseNoreply(email); ok {
			t.Errorf("%q parsed as a noreply address", email)
		}
	}
}

func TestNoreplyConflicts(t *testing.T) {
	got := NoreplyConflicts([]string{
		"1+alice@users.noreply.github.com",
		"1+alice-renamed@users.noreply.github.com",
		"2+bob@users.noreply.github.com",
		"3+bob@users.noreply.github.com",
		"4+carol@users.noreply.github.com",
		"carol@users.noreply.github.com",
	})
	want := []string{
		"account ID 1 appears with handles: alice, alice-renamed",
		"handle bob appears with account IDs: 2, 3",
	}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Errorf("got %q", got)
	}
}
