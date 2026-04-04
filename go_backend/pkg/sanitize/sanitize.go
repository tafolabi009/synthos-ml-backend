package sanitize

import (
	"regexp"
	"strings"
)

var htmlTagRegex = regexp.MustCompile(`<[^>]*>`)
var multiSpaceRegex = regexp.MustCompile(`\s+`)

// String strips HTML tags and normalizes whitespace
func String(input string) string {
	s := htmlTagRegex.ReplaceAllString(input, "")
	s = multiSpaceRegex.ReplaceAllString(s, " ")
	return strings.TrimSpace(s)
}
