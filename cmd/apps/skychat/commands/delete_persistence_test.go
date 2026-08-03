// Package commands cmd/apps/skychat/commands/delete_persistence_test.go
//
// Delete-for-everyone must be durable, not just a UI hide. Before these,
// /delete and the controller's OnDelete only pushed an SSE event: the message
// stayed in the history store on both ends and came back the next time a
// client hydrated from /history instead of its localStorage cache. These pin
// the store prune on each side, and the CXO/pair equivalent.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/cmd/apps/skychat/pairing"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/visor"
)

// storedIDs lists the ids currently persisted for peer.
func storedIDs(t *testing.T, peer string) []string {
	t.Helper()
	msgs, err := historyStore.ListByPeer(peer, 0)
	if err != nil {
		t.Fatalf("ListByPeer: %v", err)
	}
	out := make([]string, 0, len(msgs))
	for _, m := range msgs {
		out = append(out, m.ID)
	}
	return out
}

// The deleter's own stored copy goes too — otherwise the operator deletes a
// message for the peer and still has it in their own history.
func TestDeleteHandler_PrunesOurStoredCopy(t *testing.T) {
	withLifecycleEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())

	cc := newCapturingClient()
	chatCtrl = dm.New(dm.Config{Client: cc, Networks: []appnet.Type{appnet.TypeSkynet}})
	t.Cleanup(func() {
		_ = chatCtrl.Close() //nolint:errcheck
		cc.closeAll()
	})

	peer, _ := cipher.GenerateKeyPair()
	now := time.Now().UTC()
	for i, id := range []string{"keep-1", "drop-me", "keep-2"} {
		if err := historyStore.Append(history.Message{
			Peer: peer.Hex(), Outgoing: true, Text: "msg " + id, ID: id,
			Timestamp: now.Add(time.Duration(i) * time.Millisecond),
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	rr := httptest.NewRecorder()
	deleteHandler(context.Background())(rr, httptest.NewRequest(http.MethodPost, "/delete",
		strings.NewReader(`{"pk":"`+peer.Hex()+`","id":"drop-me"}`)))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}

	got := storedIDs(t, peer.Hex())
	if len(got) != 2 {
		t.Fatalf("stored ids = %v, want the two keepers", got)
	}
	for _, id := range got {
		if id == "drop-me" {
			t.Errorf("stored ids = %v, want drop-me pruned", got)
		}
	}
}

// The recipient side: a chat-delete from the peer must prune the copy we
// persisted when their message arrived. This drives the same forgetPersisted
// the controller's OnDelete calls.
func TestForgetPersisted_PrunesInboundCopy(t *testing.T) {
	withLifecycleEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())

	peer, _ := cipher.GenerateKeyPair()
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), From: peer.Hex(), Outgoing: false,
		Text: "they retract this", ID: "theirs", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	forgetPersisted(peer.Hex(), "theirs")

	if got := storedIDs(t, peer.Hex()); len(got) != 0 {
		t.Errorf("stored ids = %v, want empty after the peer retracted their message", got)
	}
}

// Persistence off, or an id that was never stored, must not fail the delete —
// the live tombstone is still the point.
func TestForgetPersisted_NoStoreAndUnknownIDAreNoOps(t *testing.T) {
	withLifecycleEnv(t)
	restoreHistoryStore(t)

	peer, _ := cipher.GenerateKeyPair()
	historyStore = nil
	forgetPersisted(peer.Hex(), "anything") // must not panic

	historyStore = newTempStore(t, history.DefaultLimits())
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), Text: "kept", ID: "keep", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	forgetPersisted(peer.Hex(), "never-stored")
	forgetPersisted(peer.Hex(), "")
	if got := storedIDs(t, peer.Hex()); len(got) != 1 || got[0] != "keep" {
		t.Errorf("stored ids = %v, want [keep] untouched", got)
	}
}

// --- CXO / pair path --------------------------------------------------------

// DELETE /pair/<pk>/message?id= publishes a retraction onto the pair feed,
// prunes our stored copy and tombstones our own bubble. Before this the CXO
// path had no delete at all: its messages carried no id either side could name.
func TestPairItemHandler_DeleteMessageRetractsOnTheFeed(t *testing.T) {
	fake := &pairHandlersAPI{}
	withPairHandlerEnv(t, fake)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())

	peer := mustPK(t)
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), Outgoing: true, Text: "over the feed", ID: "1700000000000000000",
		Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	raw, unsub := hub.subscribe()
	defer unsub()

	rr := httptest.NewRecorder()
	pairItemHandler(context.Background())(rr, httptest.NewRequest(http.MethodDelete,
		"/pair/"+peer.Hex()+"/message?id=1700000000000000000", nil))
	if rr.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%q", rr.Code, rr.Body.String())
	}

	fake.mu.Lock()
	deleted := append([]pairDeletedMsg(nil), fake.deleted...)
	fake.mu.Unlock()
	if len(deleted) != 1 || deleted[0].peer != peer || deleted[0].id != "1700000000000000000" {
		t.Errorf("PairDelete calls = %+v, want one for the seeded id", deleted)
	}

	if got := storedIDs(t, peer.Hex()); len(got) != 0 {
		t.Errorf("stored ids = %v, want the retracted message pruned", got)
	}

	got := waitForString(t, raw, 2*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("dm-status is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "dm-status" || m["status"] != dmStatusDeleted ||
		m["id"] != "1700000000000000000" || m["peer"] != peer.Hex() {
		t.Errorf("dm-status = %v, want a deleted status for the retracted id", m)
	}
}

// Inbound: a retraction on the pair feed must remove the peer's message
// instead of rendering as an empty chat bubble, and must take the stored
// copy with it. Same dm-status event the framed-conn OnDelete emits, so the
// browser has one code path for both transports.
func TestPairPoller_RetractionTombstonesInsteadOfRendering(t *testing.T) {
	withLifecycleEnv(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.DefaultLimits())
	pairEnable = true
	pairPollInterval = 10 * time.Millisecond

	peer, _ := cipher.GenerateKeyPair()
	if err := historyStore.Append(history.Message{
		Peer: peer.Hex(), From: peer.Hex(), Outgoing: false,
		Text: "they retract this", ID: "1700000000000000001", Timestamp: time.Now().UTC(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	fake := newPollAPI()
	fake.pair = []visor.PairMessage{{
		PeerPK: peer,
		TS:     time.Now().UTC(),
		Type:   pairing.MessageTypeDelete,
		ID:     "1700000000000000001",
	}}
	withFakePairRPC(t, fake)

	raw, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	startPairPoller(ctx)
	t.Cleanup(func() { fake.awaitQuiet(t, 2); cancel(); stopPairPoller() })

	got := waitForString(t, raw, 3*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("poller broadcast is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "dm-status" || m["status"] != dmStatusDeleted ||
		m["id"] != "1700000000000000001" || m["peer"] != peer.Hex() {
		t.Fatalf("broadcast = %v, want a dm-status deleted for the retracted id", m)
	}
	if got := storedIDs(t, peer.Hex()); len(got) != 0 {
		t.Errorf("stored ids = %v, want the retracted message pruned", got)
	}
}

// A normal pair message carries its feed id, so the browser can name the
// bubble for a later delete.
func TestPairPoller_BridgesMessageID(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = true
	pairPollInterval = 10 * time.Millisecond

	peer, _ := cipher.GenerateKeyPair()
	fake := newPollAPI()
	fake.pair = []visor.PairMessage{{
		PeerPK: peer, Text: "hello", TS: time.Now().UTC(), ID: "1700000000000000002",
	}}
	withFakePairRPC(t, fake)

	raw, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	startPairPoller(ctx)
	t.Cleanup(func() { fake.awaitQuiet(t, 2); cancel(); stopPairPoller() })

	got := waitForString(t, raw, 3*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("poller broadcast is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "pair" || m["id"] != "1700000000000000002" {
		t.Errorf("bridged envelope = %v, want the pair message to carry its id", m)
	}
}

// A control record this build doesn't recognize (a future type from a newer
// peer) must be skipped, not bridged — it has no text, so rendering it would
// show an empty bubble.
func TestPairPoller_SkipsUnknownRecordType(t *testing.T) {
	withLifecycleEnv(t)
	pairEnable = true
	pairPollInterval = 10 * time.Millisecond

	peer, _ := cipher.GenerateKeyPair()
	fake := newPollAPI()
	fake.pair = []visor.PairMessage{
		{PeerPK: peer, TS: time.Now().UTC(), Type: "some-future-thing", ID: "x"},
		{PeerPK: peer, Text: "a real message", TS: time.Now().UTC(), ID: "1700000000000000003-1"},
	}
	withFakePairRPC(t, fake)

	raw, unsub := hub.subscribe()
	defer unsub()

	ctx, cancel := context.WithCancel(context.Background())
	startPairPoller(ctx)
	t.Cleanup(func() { fake.awaitQuiet(t, 2); cancel(); stopPairPoller() })

	// The first thing on the wire must be the real message — the unknown
	// record ahead of it is dropped rather than broadcast.
	got := waitForString(t, raw, 3*time.Second)
	var m map[string]string
	if err := json.Unmarshal([]byte(got), &m); err != nil {
		t.Fatalf("poller broadcast is not JSON: %v (%q)", err, got)
	}
	if m["channel"] != "pair" || m["message"] != "a real message" {
		t.Errorf("first broadcast = %v, want the real message (the unknown type must be skipped)", m)
	}
}

func TestPairItemHandler_DeleteMessageRequiresID(t *testing.T) {
	withPairHandlerEnv(t, &pairHandlersAPI{})
	peer := mustPK(t)

	rr := httptest.NewRecorder()
	pairItemHandler(context.Background())(rr, httptest.NewRequest(http.MethodDelete,
		"/pair/"+peer.Hex()+"/message", nil))
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 with no id", rr.Code)
	}
}
