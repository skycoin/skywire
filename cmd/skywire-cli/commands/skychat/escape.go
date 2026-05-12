// Package cliskychat cmd/skywire-cli/commands/skychat/escape.go:
// helpers for keeping `skychat listen` and `skychat group listen`
// output strictly one-line-per-message.
//
// Why this exists: agent monitor harnesses and most log aggregators
// treat each stdout line as a separate event. When a chat body
// contains embedded newlines, the default fmt.Fprintf(out, "... %s\n",
// body) emits multiple lines for one logical message — the harness
// either fragments them into N events (losing per-message correlation)
// or clips at line N due to per-event size limits. The wire is fine
// (/status.inbound_msg_count and chat-app logs still see the full
// body); the issue is purely the CLI's stdout shape.
//
// escapeForOneLine renders a body so each chat message renders as
// exactly ONE line of stdout. We pick literal `\n` / `\r` / `\\`
// escapes (visible to humans, machine-detectable) rather than dropping
// the characters: an operator reading the log can still tell the
// message contained newlines, and any consumer that wants the raw
// content can pass --raw (or --json, which handles multi-line bodies
// natively via JSON string encoding).
package cliskychat

import "strings"

// escapeForOneLine turns embedded newlines and carriage returns into
// the literal two-character sequences `\n` and `\r`, so the returned
// string contains no actual line terminators. Backslashes are also
// escaped so the transformation is reversible — `\\n` always decodes
// to "\n" (escape) rather than "\<newline>" (raw control char).
//
// Order matters: backslashes must be escaped FIRST so that a "\n" in
// the input doesn't survive the subsequent newline-escape only to be
// undone by an external decoder. Without that, an input of `a\nb`
// (the two chars: backslash + 'n', no real newline) and an input of
// `a<LF>b` would both render to the same output `a\nb`.
func escapeForOneLine(s string) string {
	if s == "" {
		return s
	}
	if !strings.ContainsAny(s, "\\\n\r") {
		return s
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch r {
		case '\\':
			b.WriteString(`\\`)
		case '\n':
			b.WriteString(`\n`)
		case '\r':
			b.WriteString(`\r`)
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
