// Package cliskychat cmd/skywire-cli/commands/skychat/escape_test.go
package cliskychat

import "testing"

func TestEscapeForOneLine(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		{name: "empty", in: "", want: ""},
		{name: "plain ascii", in: "hello world", want: "hello world"},
		{name: "lf only", in: "line1\nline2", want: `line1\nline2`},
		{name: "cr only", in: "line1\rline2", want: `line1\rline2`},
		{name: "crlf", in: "line1\r\nline2", want: `line1\r\nline2`},
		{name: "multi lf", in: "a\nb\nc", want: `a\nb\nc`},
		{
			// Backslashes must round-trip. Without the backslash escape,
			// an input of `a\nb` (literal: a, backslash, n, b) and an
			// input of "a<LF>b" would both collapse to the same output.
			name: "backslash preserved",
			in:   `a\nb`,
			want: `a\\nb`,
		},
		{
			name: "newline plus backslash",
			in:   "a\\\nb",
			want: `a\\\nb`,
		},
		{
			name: "unicode passthrough",
			in:   "skywire — 🚀\nnext",
			want: `skywire — 🚀\nnext`,
		},
		{
			name: "trailing newline",
			in:   "done\n",
			want: `done\n`,
		},
		{
			// Verify the early-return fast path keeps non-special
			// input identical (no Builder allocation, byte-for-byte
			// same string).
			name: "tab and control chars left alone",
			in:   "col1\tcol2",
			want: "col1\tcol2",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := escapeForOneLine(tc.in)
			if got != tc.want {
				t.Fatalf("escapeForOneLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
			// Hard invariant: the output never contains a raw CR or LF.
			for _, r := range got {
				if r == '\n' || r == '\r' {
					t.Fatalf("escapeForOneLine(%q) emitted a raw line terminator %q", tc.in, got)
				}
			}
		})
	}
}
