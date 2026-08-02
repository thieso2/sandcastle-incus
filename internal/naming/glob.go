package naming

import (
	"fmt"
	"path"
	"strings"
)

// Glob support for project and machine names. Sandcastle names are single
// labels with no separators, so shell-style globbing is exactly `path.Match`
// on one path element: `*` and `?` cannot escape the segment because there is
// nothing to escape into. References are split on ":" by the caller and each
// segment is matched on its own.

// PatternMetaCharacters are the characters that turn a name into a pattern.
const PatternMetaCharacters = "*?["

// IsPattern reports whether value uses glob syntax rather than naming exactly
// one thing. Callers use it to decide whether a reference selects a set (which
// may legitimately be empty) or names a single object (whose absence is an
// error).
func IsPattern(value string) bool {
	return strings.ContainsAny(value, PatternMetaCharacters)
}

// MatchName reports whether name matches pattern. A pattern with no
// metacharacter matches only itself, so a caller can run every reference
// through MatchName and get exact-equality behaviour for literals for free.
func MatchName(pattern string, name string) bool {
	matched, err := path.Match(pattern, name)
	return err == nil && matched
}

// ValidateNamePattern validates a project/machine glob. It is deliberately
// laxer than ValidateProjectName/ValidateMachineName: it drops their two-
// character floor (`*` is a legal pattern but not a legal name) and admits the
// glob metacharacters, while still refusing anything outside the name charset
// so a typo does not sail through as a pattern that matches nothing.
//
// kind names the thing being matched ("project", "machine") for the error.
func ValidateNamePattern(kind string, value string) error {
	if value == "" {
		return fmt.Errorf("%s pattern is required", kind)
	}
	if len(value) > 63 {
		return fmt.Errorf("invalid %s pattern %q: too long", kind, value)
	}
	for _, r := range value {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
		case r == '-', r == '*', r == '?', r == '[', r == ']', r == '!', r == '^':
		default:
			return fmt.Errorf("invalid %s pattern %q", kind, value)
		}
	}
	// An unterminated "[" is not a syntax error to path.Match's caller — it is
	// a silent never-match. Reject it here so `sc ls "[abc"` says so.
	if _, err := path.Match(value, "x"); err != nil {
		return fmt.Errorf("invalid %s pattern %q: %w", kind, value, err)
	}
	return nil
}
