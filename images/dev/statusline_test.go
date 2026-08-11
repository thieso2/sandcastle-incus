// Package dev holds the images/dev build assets and the tests for the
// pure-shell scripts baked into the Dev Image (nothing here is imported by
// the rest of the Go module).
package dev

import (
	"bytes"
	"os/exec"
	"testing"
)

// statusline-command.sh's shebang is #!/bin/sh, which on the Ubuntu image
// this ships in resolves to dash. dash's printf builtin, unlike bash's,
// does not expand \xHH escapes that appear in the *format string* itself
// (as opposed to a %b-tagged argument) — see implementation-notes.md. The
// script's glyph escapes (folder/branch/hourglass/calendar/circle-arrows)
// are written exactly that way, verbatim per the wish, so running it under
// dash prints the literal `\xHH...` text rather than the intended glyph.
// These tests assert the actual byte output of the shipped artifact, not
// an idealized one, so they run the script under dash specifically.
func dashOrSkip(t *testing.T) string {
	t.Helper()
	path, err := exec.LookPath("dash")
	if err != nil {
		t.Skip("dash not found on PATH; statusline-command.sh's exact byte output is dash-specific (see implementation-notes.md)")
	}
	return path
}

func runStatusline(t *testing.T, dash, payload, home string) string {
	t.Helper()
	cmd := exec.Command(dash, "statusline-command.sh")
	cmd.Env = []string{"HOME=" + home, "TZ=UTC", "PATH=/usr/bin:/bin"}
	cmd.Stdin = bytes.NewBufferString(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("statusline-command.sh failed: %v\nstderr: %s", err, stderr.String())
	}
	return stdout.String()
}

func TestStatuslineCommandExactBytes(t *testing.T) {
	dash := dashOrSkip(t)

	const (
		esc     = "\x1b"
		reset   = esc + "[0m"
		green   = esc + "[32m"
		yellow  = esc + "[33m"
		red     = esc + "[31m"
		cyan    = esc + "[36m"
		magenta = esc + "[35m"

		// Written verbatim from the script: \xHH escapes inside a dash
		// printf format string are not expanded, so they surface as this
		// literal text rather than the intended glyph.
		folderGlyph  = `\xef\x81\xbc`
		branchGlyph  = `\xee\x82\xa0`
		hourglass    = `\xe2\x8f\xb3`
		calendar     = `\xf0\x9f\x93\x85`
		circleArrows = `\xe2\x86\xbb`

		filledBlock = "\xe2\x96\x88" // █
		emptyBlock  = "\xe2\x96\x91" // ░
	)

	bar := func(filled, empty int) string {
		s := ""
		for i := 0; i < filled; i++ {
			s += filledBlock
		}
		for i := 0; i < empty; i++ {
			s += emptyBlock
		}
		return s
	}

	tests := []struct {
		name    string
		payload string
		home    string
		want    string
	}{
		{
			name: "all-fields-present",
			payload: `{
  "model": {"display_name": "Opus 5"},
  "context_window": {"used_percentage": 42.3},
  "workspace": {"current_dir": "/home/testuser"},
  "rate_limits": {
    "five_hour": {"used_percentage": 18, "resets_at": 1786000000},
    "seven_day": {"used_percentage": 73}
  }
}`,
			home: "/home/testuser",
			want: "Opus 5 " + green + bar(4, 6) + " 42%" + reset +
				" " + cyan + folderGlyph + " ~" + reset +
				" " + green + hourglass + " 5h 18%" + reset + " " + circleArrows + " 07:06" +
				" " + yellow + calendar + " 7d 73%" + reset,
		},
		{
			name: "rate-limits-absent",
			payload: `{
  "model": {"display_name": "Opus 5"},
  "context_window": {"used_percentage": 42.3},
  "workspace": {"current_dir": "/home/testuser"}
}`,
			home: "/home/testuser",
			want: "Opus 5 " + green + bar(4, 6) + " 42%" + reset +
				" " + cyan + folderGlyph + " ~" + reset,
		},
		{
			name: "directory-outside-home",
			payload: `{
  "model": {"display_name": "Opus 5"},
  "context_window": {"used_percentage": 5},
  "workspace": {"current_dir": "/var/data/project"}
}`,
			home: "/home/testuser",
			want: "Opus 5 " + green + bar(0, 10) + " 5%" + reset +
				" " + cyan + folderGlyph + " /var/data/project" + reset,
		},
		{
			name: "directory-with-glob-metacharacter",
			payload: `{
  "model": {"display_name": "Opus 5"},
  "context_window": {"used_percentage": 95},
  "workspace": {"current_dir": "/home/testuser/weird?dir"}
}`,
			home: "/home/testuser",
			want: "Opus 5 " + red + bar(9, 1) + " 95%" + reset +
				" " + cyan + folderGlyph + " ~/weird?dir" + reset,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := runStatusline(t, dash, tc.payload, tc.home)
			if got != tc.want {
				t.Errorf("output mismatch\n got: %q\nwant: %q", got, tc.want)
			}
		})
	}
}
