package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/group"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

func searchFor(t *testing.T, q string) []searchHit {
	t.Helper()
	rr := httptest.NewRecorder()
	searchHandler(rr, httptest.NewRequest(http.MethodGet, "/search?q="+url.QueryEscape(q), nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("code=%d body=%s", rr.Code, rr.Body.String())
	}
	var hits []searchHit
	if err := json.Unmarshal(rr.Body.Bytes(), &hits); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return hits
}

func TestSearch_DirectAndGroupMessages(t *testing.T) {
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	peerA, _ := cipher.GenerateKeyPair()
	peerB, _ := cipher.GenerateKeyPair()
	member, _ := cipher.GenerateKeyPair()
	base := time.Now().UTC().Add(-time.Hour)

	reply, err := encodeReplyText(replyMeta{ToPreview: "hi", Text: "World tour"})
	if err != nil {
		t.Fatal(err)
	}
	for _, m := range []history.Message{
		{Peer: peerA.Hex(), From: peerA.Hex(), Text: "hello world", Timestamp: base},
		{Peer: peerA.Hex(), Outgoing: true, Text: reply, Timestamp: base.Add(time.Minute)},
		{Peer: peerB.Hex(), From: peerB.Hex(), Text: "nothing here", Timestamp: base.Add(2 * time.Minute)},
	} {
		if err := historyStore.Append(m); err != nil {
			t.Fatalf("append: %v", err)
		}
	}
	withPairRPC(t, &groupAPI{
		groups: []visorapi.GroupInfo{
			{ID: "g-live"},
			{ID: "g-left", Status: group.StatusLeft},
		},
		history: []visorapi.GroupMessage{
			{GroupID: "g-live", SenderPK: member, Text: "WORLD news", TS: base.Add(3 * time.Minute)},
		},
	})

	hits := searchFor(t, "world")
	if len(hits) != 3 {
		t.Fatalf("hits=%+v, want 3", hits)
	}
	if hits[0].GroupID != "g-live" || hits[0].From != member.Hex() {
		t.Errorf("newest hit = %+v, want the group message", hits[0])
	}
	if hits[1].Peer != peerA.Hex() || !hits[1].Outgoing || hits[1].Text != "World tour" {
		t.Errorf("second hit = %+v, want the reply's own text", hits[1])
	}
	if hits[2].Peer != peerA.Hex() || hits[2].Text != "hello world" {
		t.Errorf("oldest hit = %+v", hits[2])
	}

	if got := searchFor(t, "skychat_reply"); len(got) != 0 {
		t.Errorf("an envelope key matched: %+v", got)
	}
	if got := searchFor(t, "  "); len(got) != 0 {
		t.Errorf("a blank query matched: %+v", got)
	}
}

// Persistence off and the visor RPC down: an empty answer, not an error.
func TestSearch_NoSources(t *testing.T) {
	restoreHistoryStore(t)
	historyStore = nil
	withPairRPCDown(t)
	if got := searchFor(t, "x"); len(got) != 0 {
		t.Errorf("hits=%+v, want none", got)
	}
}
