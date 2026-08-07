// Package commands cmd/apps/skychat/commands/link.go c4-app-chat
//
// The peer link — "is there a way to this person right now, and if not, make
// one" — as an endpoint of its own.
//
// A DM send has always dialed on demand: the first message to a peer carries
// the handshake inside its own request. That is fine for a CLI, where a
// command that takes half a minute is a command that takes half a minute. It
// is not fine for a person holding a phone. Setting up a skynet route means
// finding a path and building it hop by hop, and while that runs the sender
// sees a message sitting there with nothing to say for itself — so they send
// another, and another, and conclude the app is broken. Meanwhile every one of
// those sends starts a dial of its own.
//
// So the UI asks for the link first, up front, and is told which of three
// things is true: it is up, it is being built, or it could not be built and
// here is why. What it does with that — hold the composer's messages in a
// queue and say what it is waiting for — is the browser's business; this file
// is only the honest answer.
//
// Nothing else changes. The link is the same cached conn `Send` would have
// made, made a little earlier and under its own name, so a message that goes
// out after one lands exactly as it did before.
package commands

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
)

// linkDialTimeout bounds one attempt. Skynet route setup is the slow case and
// the visor's own retrier sits underneath this, so the ceiling is generous:
// giving up early on a link that was about to come up is worse than a spinner
// that runs a while, because the UI is showing what it is waiting for.
const linkDialTimeout = 45 * time.Second

// linkState is one peer's link as the UI needs to see it: up, being built, or
// failed with a reason. Exactly one of Ready/Connecting is true while a dial
// runs; Err is the last failure and is cleared when a new attempt starts.
type linkState struct {
	Ready      bool   `json:"ready"`
	Connecting bool   `json:"connecting"`
	Err        string `json:"error,omitempty"`
	// Network the dial went out over, for the UI's own reporting.
	Network string `json:"network,omitempty"`
}

// linkStore tracks in-flight dials so a chat that is polling — or a user
// tapping Retry — cannot stack attempts on the same peer.
type linkStore struct {
	mu   sync.Mutex
	dial map[cipher.PubKey]bool
	err  map[cipher.PubKey]string
}

var links = &linkStore{
	dial: make(map[cipher.PubKey]bool),
	err:  make(map[cipher.PubKey]string),
}

// state reports pk's link without touching the network.
func (s *linkStore) state(pk cipher.PubKey) linkState {
	ready := chatCtrl != nil && chatCtrl.HasConn(pk)
	s.mu.Lock()
	defer s.mu.Unlock()
	st := linkState{Ready: ready, Connecting: s.dial[pk]}
	// A live link makes any earlier failure history, not news.
	if !ready {
		st.Err = s.err[pk]
	}
	return st
}

// ensure starts a dial to pk when one is warranted — nothing cached, nothing
// already in flight — and returns the state as of right now. It never blocks
// on the network: the caller gets "connecting" and asks again.
func (s *linkStore) ensure(pk cipher.PubKey, netType appnet.Type) linkState {
	if chatCtrl == nil {
		return linkState{Err: "chat transport unavailable"}
	}
	if chatCtrl.HasConn(pk) {
		return linkState{Ready: true}
	}
	s.mu.Lock()
	if s.dial[pk] {
		s.mu.Unlock()
		return linkState{Connecting: true, Network: string(netType)}
	}
	s.dial[pk] = true
	delete(s.err, pk) // a new attempt is not the old attempt's failure
	s.mu.Unlock()

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), linkDialTimeout)
		defer cancel()
		err := chatCtrl.Connect(ctx, pk, netType)
		s.mu.Lock()
		delete(s.dial, pk)
		if err != nil {
			s.err[pk] = err.Error()
		}
		s.mu.Unlock()
		if err != nil && chatLog != nil {
			chatLog.Warnf("skychat: link to %s over %s failed: %v", pk.Hex()[:8], netType, err)
		}
	}()
	return linkState{Connecting: true, Network: string(netType)}
}

// linkNetwork resolves the UI's network choice to the one a link dial should
// use. It mirrors the send path's own reading of "auto" — dmsg first, because
// it connects in about the time a route takes to plan — so the link a send
// then reuses is the link that send would have made for itself.
func linkNetwork(name string) (appnet.Type, bool) {
	switch name {
	case "", "auto":
		if chatCtrlHasNetwork(appnet.TypeDmsg) {
			return appnet.TypeDmsg, true
		}
		return appnet.TypeSkynet, true
	case "dmsg":
		return appnet.TypeDmsg, true
	case "skynet":
		return appnet.TypeSkynet, true
	default:
		return "", false
	}
}

// chatCtrlHasNetwork reports whether net is one this build actually enabled.
// The controller keeps the authoritative list; these are the same two flags it
// was built from.
func chatCtrlHasNetwork(net appnet.Type) bool {
	switch net {
	case appnet.TypeDmsg:
		return useDmsg
	case appnet.TypeSkynet:
		return useSkynet
	default:
		return false
	}
}

// linkHandler serves the peer link.
//
//	GET  /link?pk=<hex>                         -> current state, no dialing
//	POST /link {"pk":"<hex>","network":"auto"}  -> start one if needed, same shape
//
// POST is deliberately not "connect and tell me when you are done": a route
// can take the better part of a minute and a browser request must not be held
// open for it. It starts the work and answers immediately; the UI polls the
// GET, which is cheap and does nothing but read a map.
func linkHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var (
			pkHex   string
			network string
		)
		switch r.Method {
		case http.MethodGet:
			pkHex = r.URL.Query().Get("pk")
			network = r.URL.Query().Get("network")
		case http.MethodPost:
			var body struct {
				PK      string `json:"pk"`
				Network string `json:"network"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
				return
			}
			pkHex, network = body.PK, body.Network
		default:
			http.Error(w, "GET or POST only", http.StatusMethodNotAllowed)
			return
		}

		// The null key is not an error to UnmarshalText — an empty string
		// parses straight into it — and it is nobody, so it must not become a
		// dial or a state anything can poll.
		var pk cipher.PubKey
		if err := pk.UnmarshalText([]byte(pkHex)); err != nil {
			http.Error(w, "bad pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		if pk.Null() {
			http.Error(w, "bad pk: empty", http.StatusBadRequest)
			return
		}

		if r.Method == http.MethodGet {
			writeJSON(w, links.state(pk))
			return
		}

		netType, ok := linkNetwork(network)
		if !ok {
			http.Error(w, "invalid network type: use 'auto', 'skynet' or 'dmsg'", http.StatusBadRequest)
			return
		}
		writeJSON(w, links.ensure(pk, netType))
	}
}
