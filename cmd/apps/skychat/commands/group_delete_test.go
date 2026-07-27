// Package commands cmd/apps/skychat/commands/group_delete_test.go
//
// Coverage for group "delete for everyone": the tombstone envelope, the
// DELETE /group/<id>/message endpoint (publishes a tombstone + prunes the
// leaf), and the group-history filtering that hides a deleted message (and the
// tombstone itself) for reloading clients / new joiners.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

func TestGroupDeleteEnvelopeRoundTrip(t *testing.T) {
	s, err := encodeGroupDeleteText(groupDeleteMeta{ToTSNano: 1700000000000000000})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	got, ok := parseGroupDeleteText(s)
	if !ok || got.ToTSNano != 1700000000000000000 {
		t.Fatalf("round-trip: ok=%v got=%+v", ok, got)
	}

	// Not a tombstone → falls through.
	for _, raw := range []string{"", "plain", `{"skychat_file":{"id":"x"}}`, `{"skychat_delete":{}}`, `{"skychat_delete":{"to_ts_nano":0}}`} {
		if _, ok := parseGroupDeleteText(raw); ok {
			t.Errorf("payload %q should not parse as a tombstone", raw)
		}
	}
}

func TestGroupDeleteEndpoint(t *testing.T) {
	fake := &groupAPI{}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodDelete, "/group/gid-1/message?ts=1700000000000000123", nil)
	groupItemHandler()(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Fatalf("DELETE message: code=%d body=%q", rr.Code, rr.Body.String())
	}
	// A tombstone was published…
	if len(fake.sent) != 1 {
		t.Fatalf("expected 1 tombstone send, got %d", len(fake.sent))
	}
	meta, ok := parseGroupDeleteText(fake.sent[0].Text)
	if !ok || meta.ToTSNano != 1700000000000000123 {
		t.Errorf("tombstone body wrong: %q", fake.sent[0].Text)
	}
	// …and the leaf was pruned by the same ts.
	if len(fake.unsent) != 1 || fake.unsent[0].TS != 1700000000000000123 {
		t.Errorf("expected prune ts=...123, got %+v", fake.unsent)
	}

	// Missing ts → 400.
	rr = httptest.NewRecorder()
	groupItemHandler()(rr, httptest.NewRequest(http.MethodDelete, "/group/gid-1/message", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("no ts: code=%d, want 400", rr.Code)
	}
}

func TestGroupHistory_FiltersDeleted(t *testing.T) {
	author, _ := cipher.GenerateKeyPair()
	target := time.Unix(0, 1700000000000000000).UTC()
	tomb, _ := encodeGroupDeleteText(groupDeleteMeta{ToTSNano: target.UnixNano()}) //nolint:errcheck

	fake := &groupAPI{history: []visor.GroupMessage{
		{GroupID: "g", SenderPK: author, Text: "keep me", TS: time.Unix(0, 1699000000000000000).UTC()},
		{GroupID: "g", SenderPK: author, Text: "delete me", TS: target},
		{GroupID: "g", SenderPK: author, Text: tomb, TS: time.Unix(0, 1700000000000000001).UTC()},
	}}
	withFakePairRPC(t, fake)

	rr := httptest.NewRecorder()
	groupItemHandler()(rr, httptest.NewRequest(http.MethodGet, "/group/g/history", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("history: code=%d", rr.Code)
	}
	var rows []map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &rows); err != nil {
		t.Fatalf("decode: %v", err)
	}
	// Only "keep me" survives: the deleted message and the tombstone are gone.
	if len(rows) != 1 {
		t.Fatalf("want 1 surviving row, got %d: %v", len(rows), rows)
	}
	if rows[0]["text"] != "keep me" {
		t.Errorf("wrong survivor: %v", rows[0]["text"])
	}
	if _, ok := rows[0]["ts_nano"]; !ok {
		t.Errorf("survivor should carry ts_nano: %v", rows[0])
	}
}
