package dashboard

import (
	"io/fs"
	"regexp"
	"strings"
	"testing"
)

var htmlSinkRe = regexp.MustCompile(`\.(innerHTML|outerHTML)\s*\+?=(?:[^=])|insertAdjacentHTML\s*\(|document\.write`)

// TestStaticJSHTMLSinksTakeOnlyLiterals guards the dashboard JS against
// feeding server-, error- or user-derived strings into HTML parsing sinks:
// every innerHTML/outerHTML assignment must be a single string literal with no
// interpolation, and insertAdjacentHTML/document.write must not be used.
func TestStaticJSHTMLSinksTakeOnlyLiterals(t *testing.T) {
	files, err := fs.Glob(staticFS, "static/js/*.js")
	if err != nil {
		t.Fatal(err)
	}
	if len(files) == 0 {
		t.Fatal("no static JS files found")
	}
	for _, f := range files {
		b, err := fs.ReadFile(staticFS, f)
		if err != nil {
			t.Fatal(err)
		}
		src := string(b)
		for _, loc := range htmlSinkRe.FindAllStringSubmatchIndex(src, -1) {
			line := strings.Count(src[:loc[0]], "\n") + 1
			if loc[2] < 0 {
				t.Errorf("%s:%d: %s is not allowed; build nodes with textContent/createElement", f, line, strings.TrimSpace(src[loc[0]:loc[1]]))
				continue
			}
			rhs := strings.TrimLeft(src[loc[1]-1:], " \t\r\n")
			if !isLoneStringLiteral(rhs) {
				t.Errorf("%s:%d: %s is fed a non-literal or interpolated value; use textContent/createElement", f, line, src[loc[2]:loc[3]])
			}
		}
	}
}

// isLoneStringLiteral reports whether s starts with a quoted string literal
// (no template interpolation) followed directly by the end of the statement.
func isLoneStringLiteral(s string) bool {
	if s == "" {
		return false
	}
	q := s[0]
	if q != '\'' && q != '"' && q != '`' {
		return false
	}
	for i := 1; i < len(s); i++ {
		switch s[i] {
		case '\\':
			i++
		case '$':
			if q == '`' && i+1 < len(s) && s[i+1] == '{' {
				return false
			}
		case q:
			rest := strings.TrimLeft(s[i+1:], " \t")
			return strings.HasPrefix(rest, ";")
		}
	}
	return false
}
