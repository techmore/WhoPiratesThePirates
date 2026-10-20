// Package sqliteuri builds SQLite "file:" URIs from filesystem paths.
//
// SQLite parses everything after the first '?' in a file URI as query
// parameters and everything after '#' as a fragment, so a path that contains
// those characters (or a literal '%') must be percent-encoded before it can be
// opened. Every database open in this project goes through this helper so
// operator-chosen paths behave the same everywhere.
package sqliteuri

import "strings"

// Build returns a "file:" URI for path with the given query parameters
// appended. Each entry of params is written verbatim (for example
// "mode=ro" or "_pragma=busy_timeout(5000)").
func Build(path string, params ...string) string {
	var builder strings.Builder
	builder.WriteString("file:")
	builder.WriteString(escapePath(path))
	for index, param := range params {
		if index == 0 {
			builder.WriteByte('?')
		} else {
			builder.WriteByte('&')
		}
		builder.WriteString(param)
	}
	return builder.String()
}

// escapePath percent-encodes only the characters that change how SQLite
// interprets a file URI. Spaces and other bytes are passed through unchanged
// because the driver accepts them literally and encoding them would make the
// path harder to read in logs.
func escapePath(path string) string {
	var builder strings.Builder
	for index := 0; index < len(path); index++ {
		switch char := path[index]; char {
		case '%':
			builder.WriteString("%25")
		case '?':
			builder.WriteString("%3F")
		case '#':
			builder.WriteString("%23")
		default:
			builder.WriteByte(char)
		}
	}
	return builder.String()
}
