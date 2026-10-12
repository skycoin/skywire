package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/privacy"
	"github.com/skycoin/skywire/pkg/skychat/xfer"
)

func withPrivacyStore(t *testing.T) {
	t.Helper()
	prev := privacyStore
	store, err := privacy.OpenStore(filepath.Join(t.TempDir(), "privacy.json"))
	if err != nil {
		t.Fatalf("OpenStore: %v", err)
	}
	privacyStore = store
	t.Cleanup(func() { privacyStore = prev })
}

func privacyCall(t *testing.T, h http.HandlerFunc, method, body string) privacyState {
	t.Helper()
	rr := httptest.NewRecorder()
	h(rr, httptest.NewRequest(method, "/privacy", strings.NewReader(body)))
	if rr.Code != http.StatusOK {
		t.Fatalf("%s: code=%d body=%s", method, rr.Code, rr.Body.String())
	}
	var st privacyState
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return st
}

func TestPrivacy_ApprovalRequestsAndBlocking(t *testing.T) {
	withPrivacyStore(t)
	withContactStore(t)
	restoreHistoryStore(t)
	historyStore = newTempStore(t, history.Limits{})
	old, _ := cipher.GenerateKeyPair()
	named, _ := cipher.GenerateKeyPair()
	stranger, _ := cipher.GenerateKeyPair()
	inbound := func(pk cipher.PubKey) {
		if err := historyStore.Append(history.Message{
			Peer: pk.Hex(), From: pk.Hex(), Text: "hi", Timestamp: time.Now().UTC(),
		}); err != nil {
			t.Fatal(err)
		}
	}
	inbound(old)

	st := privacyCall(t, privacyHandler, http.MethodPost, `{"approval":true}`)
	if !st.Approval || len(st.Requests) != 0 {
		t.Fatalf("turning approval on made an existing chat a request: %+v", st)
	}
	if _, err := contactStore.Set(named.Hex(), "Named"); err != nil {
		t.Fatal(err)
	}
	inbound(named)
	inbound(stranger)

	st = privacyCall(t, privacyHandler, http.MethodGet, "")
	if len(st.Requests) != 1 || st.Requests[0] != stranger.Hex() {
		t.Fatalf("requests = %v, want only the stranger", st.Requests)
	}
	st = privacyCall(t, privacyAcceptHandler, http.MethodPost, `{"pk":"`+stranger.Hex()+`"}`)
	if len(st.Requests) != 0 || inboundVerdict(stranger.Hex()) != privacy.Allow {
		t.Fatalf("accepting left a request: %+v", st)
	}
	st = privacyCall(t, privacyBlockHandler, http.MethodPost, `{"pk":"`+named.Hex()+`","blocked":true}`)
	if len(st.Blocked) != 1 || inboundVerdict(named.Hex()) != privacy.Reject {
		t.Fatalf("blocking a named contact did not take: %+v", st)
	}
}

func TestPrivacy_FilesFromBlockedOrRequestingPeersAreDeclined(t *testing.T) {
	withXferEnv(t)
	withCleanDownloads(t)
	withPrivacyStore(t)
	withContactStore(t)
	from, _ := cipher.GenerateKeyPair()
	establishPeer(t, from)
	offer := xfer.Offer{ID: "priv-1", Name: "x.txt", Size: 3}

	if err := privacyStore.SetApproval(true, nil); err != nil {
		t.Fatal(err)
	}
	if w, ok := acceptInbound(from, offer); ok || w != nil {
		t.Error("accepted a file from a peer still waiting as a request")
	}
	if err := privacyStore.Accept(from.Hex()); err != nil {
		t.Fatal(err)
	}
	if err := privacyStore.SetBlocked(from.Hex(), true); err != nil {
		t.Fatal(err)
	}
	if w, ok := acceptInbound(from, offer); ok || w != nil {
		t.Error("accepted a file from a blocked peer")
	}
}

func TestPrivacy_RequestFlagReachesTheUI(t *testing.T) {
	var m map[string]any
	if err := json.Unmarshal([]byte(renderLegacySSE(chatEvent{From: "x", Text: "hi", Dir: "in", Request: true})), &m); err != nil {
		t.Fatal(err)
	}
	if m["request"] != true {
		t.Errorf("request flag missing from the /sse line: %v", m)
	}
}

type declineAPI struct {
	visorAPIShim
	declined []string
}

func (a *declineAPI) VoiceDecline(id string) error {
	a.declined = append(a.declined, id)
	return nil
}

func TestPrivacy_CallsFromBlockedPeersAreDeclined(t *testing.T) {
	withPrivacyStore(t)
	withContactStore(t)
	blocked, _ := cipher.GenerateKeyPair()
	friend, _ := cipher.GenerateKeyPair()
	if err := privacyStore.SetBlocked(blocked.Hex(), true); err != nil {
		t.Fatal(err)
	}
	fake := &declineAPI{}
	withPairRPC(t, fake)

	kept := declineBlockedCalls([]string{
		"c1 from " + blocked.Hex(),
		"c2 from " + friend.Hex(),
	})
	if len(kept) != 1 || !strings.HasPrefix(kept[0], "c2 ") {
		t.Errorf("kept = %v, want only the friend's call", kept)
	}
	if len(fake.declined) != 1 || fake.declined[0] != "c1" {
		t.Errorf("declined = %v, want [c1]", fake.declined)
	}
}
