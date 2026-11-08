package git

import (
	"fmt"
	"regexp"
	"strings"
)

// PathFilter selects tree paths by pattern, for --paths.
type PathFilter struct {
	patterns []*regexp.Regexp
}

// NewPathFilter compiles patterns. A pattern without wildcards selects that
// file or everything under that directory. With wildcards, * and ? match any
// characters including "/", as git's own pathspecs do, and [...] a class; a
// pattern also selects everything under a directory it matches.
func NewPathFilter(patterns []string) (*PathFilter, error) {
	f := &PathFilter{}
	for _, p := range patterns {
		p = strings.TrimPrefix(strings.TrimPrefix(p, "./"), "/")
		p = strings.TrimSuffix(p, "/")
		if p == "" {
			return nil, fmt.Errorf("empty path pattern")
		}
		var re strings.Builder
		re.WriteString("^")
		for i := 0; i < len(p); i++ {
			switch c := p[i]; c {
			case '*':
				re.WriteString(".*")
			case '?':
				re.WriteString(".")
			case '[':
				end := strings.IndexByte(p[i+1:], ']')
				if end < 0 {
					return nil, fmt.Errorf("unterminated [ in path pattern %q", p)
				}
				class := p[i+1 : i+1+end]
				if strings.HasPrefix(class, "!") {
					class = "^" + class[1:]
				}
				re.WriteString("[" + class + "]")
				i += end + 1
			default:
				re.WriteString(regexp.QuoteMeta(string(c)))
			}
		}
		re.WriteString("(/.*)?$")
		compiled, err := regexp.Compile(re.String())
		if err != nil {
			return nil, fmt.Errorf("invalid path pattern %q: %w", p, err)
		}
		f.patterns = append(f.patterns, compiled)
	}
	return f, nil
}

// Match reports whether a tree path is selected.
func (f *PathFilter) Match(p string) bool {
	for _, re := range f.patterns {
		if re.MatchString(p) {
			return true
		}
	}
	return false
}
