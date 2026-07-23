// Package commands cmd/apps/skychat/commands/reply_test.go
//
// Unit coverage for quoted replies: the envelope round-trip, the cheap
// detection gate, history/map enrichment, and the DM /sse read boundary
// (renderLegacySSE) unwrapping a reply body into text + reply_to_* fields.
package commands

import (
	"encoding/json"
	"testing"

	"github.com/skycoin/skywire/cmd/apps/skychat/history"
)

func TestReplyEnvelopeRoundTrip(t *testing.T) {
	in := replyMeta{ToSender: "abc", ToTS: "2026-07-24T00:00:00Z", ToPreview: "hello there", Text: "hi back"}
	s, err := encodeReplyText(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, ok := parseReplyText(s)
	if !ok {
		t.Fatal("round-trip: envelope not detected")
	}
	if got != in {
		t.Errorf("round-trip mismatch: got %+v want %+v", got, in)
	}
}

func TestParseReplyText_Gate(t *testing.T) {
	// Not a reply envelope — must fall through (ok=false).
	for _, raw := range []string{
		"",
		"plain text",
		"  hello skychat_reply not json  ", // substring present but not an envelope
		`{"skychat_file":{"id":"x"}}`,       // a different envelope
		`{"skychat_reply"`,                  // malformed JSON
		`{"foo":1}`,
	} {
		if _, ok := parseReplyText(raw); ok {
			t.Errorf("payload %q should NOT parse as a reply", raw)
		}
	}

	// A well-formed envelope parses.
	valid := `{"skychat_reply":{"to_ts":"t","to_preview":"p","text":"body"}}`
	meta, ok := parseReplyText(valid)
	if !ok || meta.Text != "body" || meta.ToPreview != "p" {
		t.Errorf("valid envelope: ok=%v meta=%+v", ok, meta)
	}
}

func TestEnrichReplyMessage(t *testing.T) {
	// A reply body is rewritten to clean text + reply_to_* fields.
	env, _ := encodeReplyText(replyMeta{ToSender: "s", ToTS: "ts", ToPreview: "prev", Text: "the reply"}) //nolint:errcheck
	m := history.Message{Text: env}
	enrichReplyMessage(&m)
	if m.Text != "the reply" || m.ReplyToSender != "s" || m.ReplyToTS != "ts" || m.ReplyToPreview != "prev" {
		t.Errorf("enriched message wrong: %+v", m)
	}

	// A plain message is left untouched.
	plain := history.Message{Text: "just text"}
	enrichReplyMessage(&plain)
	if plain.Text != "just text" || plain.ReplyToTS != "" {
		t.Errorf("plain message mutated: %+v", plain)
	}
}

func TestEnrichReplyRow(t *testing.T) {
	row := map[string]any{}
	enrichReplyRow(row, replyMeta{ToSender: "s", ToTS: "ts", ToPreview: "prev", Text: "x"})
	if row["reply_to_sender"] != "s" || row["reply_to_ts"] != "ts" || row["reply_to_preview"] != "prev" {
		t.Errorf("row not enriched: %v", row)
	}
	// Empty fields are omitted, not written as "".
	empty := map[string]any{}
	enrichReplyRow(empty, replyMeta{Text: "x"})
	if _, ok := empty["reply_to_sender"]; ok {
		t.Errorf("empty sender should be omitted: %v", empty)
	}
}

func TestRenderLegacySSE_Reply(t *testing.T) {
	env, _ := encodeReplyText(replyMeta{ToSender: "peerpk", ToTS: "2026-07-24T00:00:00Z", ToPreview: "orig", Text: "clean reply"}) //nolint:errcheck
	out := renderLegacySSE(chatEvent{ID: "1", Channel: channelDM, Transport: "dmsg", Dir: "in", From: "peerpk", Text: env})

	var m map[string]any
	if err := json.Unmarshal([]byte(out), &m); err != nil {
		t.Fatalf("unmarshal SSE: %v", err)
	}
	if m["message"] != "clean reply" {
		t.Errorf("message not unwrapped: %v", m["message"])
	}
	if m["reply_to_preview"] != "orig" || m["reply_to_ts"] != "2026-07-24T00:00:00Z" || m["reply_to_sender"] != "peerpk" {
		t.Errorf("reply_to_* not surfaced: %v", m)
	}
}
