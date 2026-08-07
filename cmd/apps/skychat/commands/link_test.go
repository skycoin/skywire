// Package commands cmd/apps/skychat/commands/link_test.go
//
// Coverage for /link, the endpoint the UI asks "is there a way to this person
// right now" before it lets a message leave the composer. The contract it
// depends on is small and worth pinning: a well-formed answer for a peer with
// no link, a refusal for a malformed key or an unknown network, and — the one
// that matters most — a GET that never dials, so polling it costs nothing.
package commands

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/dm"
)

// linkTestPK is a syntactically valid key nothing will ever answer for.
const linkTestPK = "024ec47420176680816e0406250e7156465e4531f5b26057c9f6297bb0303558c7"

// withLinkCtrl points the package at a controller with no transport (dials
// fail immediately) and gives the link store a clean slate.
func withLinkCtrl(t *testing.T) {
	t.Helper()
	orig := chatCtrl
	chatCtrl = dm.New(dm.Config{})
	links.mu.Lock()
	links.dial = make(map[cipher.PubKey]bool)
	links.err = make(map[cipher.PubKey]string)
	links.mu.Unlock()
	t.Cleanup(func() { chatCtrl = orig })
}

func TestLinkHandler_GetReportsNoLink(t *testing.T) {
	withLinkCtrl(t)

	rr := httptest.NewRecorder()
	linkHandler()(rr, httptest.NewRequest(http.MethodGet, "/link?pk="+linkTestPK, nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%q", rr.Code, rr.Body.String())
	}
	var st linkState
	if err := json.Unmarshal(rr.Body.Bytes(), &st); err != nil {
		t.Fatalf("body is not a link state: %v\nbody=%q", err, rr.Body.String())
	}
	if st.Ready {
		t.Error("ready = true for a peer that was never dialled")
	}
	// The whole point of the GET: it reads, it does not dial. A poll that
	// started a dial would stack one every 1.5 seconds.
	if st.Connecting {
		t.Error("a GET started a dial; it must only report")
	}
}

func TestLinkHandler_RejectsBadInput(t *testing.T) {
	withLinkCtrl(t)

	cases := []struct {
		name   string
		method string
		target string
		body   string
		want   int
	}{
		{"malformed pk", http.MethodGet, "/link?pk=nope", "", http.StatusBadRequest},
		{"empty pk", http.MethodGet, "/link", "", http.StatusBadRequest},
		{"bad json", http.MethodPost, "/link", "{", http.StatusBadRequest},
		{
			"unknown network", http.MethodPost, "/link",
			`{"pk":"` + linkTestPK + `","network":"carrier-pigeon"}`, http.StatusBadRequest,
		},
		{"wrong method", http.MethodDelete, "/link?pk=" + linkTestPK, "", http.StatusMethodNotAllowed},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			req := httptest.NewRequest(c.method, c.target, strings.NewReader(c.body))
			linkHandler()(rr, req)
			if rr.Code != c.want {
				t.Fatalf("status = %d, want %d; body=%q", rr.Code, c.want, rr.Body.String())
			}
		})
	}
}

func TestLinkNetwork_AutoPrefersDmsg(t *testing.T) {
	origDmsg, origSkynet := useDmsg, useSkynet
	t.Cleanup(func() { useDmsg, useSkynet = origDmsg, origSkynet })

	// Auto means "get me talking to this person soonest", and dmsg connects in
	// about the time a route takes to plan — the same reading the send path's
	// auto mode uses, so a link and the send that reuses it agree.
	useDmsg, useSkynet = true, true
	if got, ok := linkNetwork("auto"); !ok || got != appnet.TypeDmsg {
		t.Errorf("auto with both enabled = %v (ok=%v), want dmsg", got, ok)
	}
	// With dmsg off there is one answer left, and it is not "no".
	useDmsg, useSkynet = false, true
	if got, ok := linkNetwork(""); !ok || got != appnet.TypeSkynet {
		t.Errorf("auto with dmsg off = %v (ok=%v), want skynet", got, ok)
	}
	if _, ok := linkNetwork("pigeon"); ok {
		t.Error("an unknown network was accepted")
	}
}
