// Package commands cmd/apps/skychat/commands/forward_test.go
//
// The test that matters most here is the one asserting what is NOT in the
// envelope. Everything else is round-tripping.
package commands

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/skychat/history"
)

func TestForwardRoundTrip(t *testing.T) {
	body, err := encodeForwardText(forwardMeta{From: "Skywire News", Text: "the release is out"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, ok := parseForwardText(body)
	if !ok {
		t.Fatalf("parse did not recognize its own output: %q", body)
	}
	if got.From != "Skywire News" || got.Text != "the release is out" {
		t.Fatalf("round trip = %+v", got)
	}
}

// The privacy rule made testable: whatever a forward carries, it is never an
// author. This asserts the wire shape directly rather than the behavior of
// one call site, so adding a field to forwardMeta fails here regardless of
// which caller would have populated it.
func TestForwardEnvelopeCarriesNoAuthor(t *testing.T) {
	body, err := encodeForwardText(forwardMeta{From: "Announcements", Text: "hello"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	var env map[string]map[string]any
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	inner, ok := env["skychat_forward"]
	if !ok {
		t.Fatalf("no skychat_forward key in %q", body)
	}
	allowed := map[string]bool{"from": true, "text": true}
	for k := range inner {
		if !allowed[k] {
			t.Fatalf("forward envelope carries unexpected field %q — a forward must never "+
				"disclose the original author; see forward.go", k)
		}
	}
	// Belt and braces against a field that merely looks harmless.
	for _, banned := range []string{"sender", "author", "pk", "public_key", "from_pk", "ts", "group"} {
		if _, bad := inner[banned]; bad {
			t.Fatalf("forward envelope carries %q", banned)
		}
	}
}

// A DM or group forward has no origin at all, and that is the normal case —
// it must round trip as cleanly as the channel case.
func TestForwardWithoutOriginOmitsFrom(t *testing.T) {
	body, err := encodeForwardText(forwardMeta{Text: "just the words"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if strings.Contains(body, `"from"`) {
		t.Fatalf("empty origin still emitted a from field: %q", body)
	}
	got, ok := parseForwardText(body)
	if !ok {
		t.Fatal("anonymous forward did not parse")
	}
	if got.From != "" || got.Text != "just the words" {
		t.Fatalf("round trip = %+v", got)
	}
}

// The label is display text from a peer, so the cap is enforced on receipt
// and not only on send.
func TestForwardFromIsTruncatedOnParse(t *testing.T) {
	long := strings.Repeat("n", maxForwardFromLen*3)
	raw := `{"skychat_forward":{"from":"` + long + `","text":"x"}}`
	got, ok := parseForwardText(raw)
	if !ok {
		t.Fatal("did not parse")
	}
	if len(got.From) != maxForwardFromLen {
		t.Fatalf("from is %d bytes, want %d", len(got.From), maxForwardFromLen)
	}
}

func TestParseForwardIgnoresOrdinaryText(t *testing.T) {
	for _, s := range []string{
		"",
		"hello there",
		"{ not json",
		`{"skychat_reply":{"text":"a reply"}}`,
		`{"skychat_file":{"name":"x"}}`,
		`{"skychat_forward":null}`,
	} {
		if _, ok := parseForwardText(s); ok {
			t.Fatalf("%q was read as a forward", s)
		}
	}
}

// A reply and a forward must not be confused for each other in either
// direction — both read boundaries try them in sequence on the same body.
func TestReplyAndForwardDoNotOverlap(t *testing.T) {
	fwd, err := encodeForwardText(forwardMeta{Text: "x"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if _, ok := parseReplyText(fwd); ok {
		t.Fatal("a forward parsed as a reply")
	}
	rep, err := encodeReplyText(replyMeta{Text: "y"})
	if err != nil {
		t.Fatalf("encode reply: %v", err)
	}
	if _, ok := parseForwardText(rep); ok {
		t.Fatal("a reply parsed as a forward")
	}
}

// The flag has to survive even with no label, because "this is a forward" is
// the only thing distinguishing an anonymous forward from text the sender
// wrote themselves.
func TestEnrichForwardRowAlwaysMarksForwarded(t *testing.T) {
	row := map[string]any{}
	enrichForwardRow(row, forwardMeta{Text: "x"})
	if row["forwarded"] != true {
		t.Fatalf("anonymous forward not marked: %+v", row)
	}
	if _, present := row["forward_from"]; present {
		t.Fatalf("anonymous forward emitted an origin: %+v", row)
	}

	row = map[string]any{}
	enrichForwardRow(row, forwardMeta{From: "Chan", Text: "x"})
	if row["forwarded"] != true || row["forward_from"] != "Chan" {
		t.Fatalf("channel forward row = %+v", row)
	}
}

func TestEnrichForwardMessageRewritesBody(t *testing.T) {
	body, err := encodeForwardText(forwardMeta{From: "Chan", Text: "the text"})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	m := history.Message{Text: body}
	enrichForwardMessage(&m)
	if m.Text != "the text" {
		t.Fatalf("text = %q, want the unwrapped body", m.Text)
	}
	if !m.Forwarded || m.ForwardFrom != "Chan" {
		t.Fatalf("message = %+v", m)
	}

	// An ordinary message is left exactly as it was.
	plain := history.Message{Text: "hello"}
	enrichForwardMessage(&plain)
	if plain.Text != "hello" || plain.Forwarded {
		t.Fatalf("plain message was rewritten: %+v", plain)
	}
}
