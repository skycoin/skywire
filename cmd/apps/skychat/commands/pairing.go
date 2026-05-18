// Package commands cmd/apps/skychat/commands/pairing.go
//
// Pair-feed integration: HTTP endpoints, structured-message handshake
// over the legacy direct path, and the inbound-poll bridge into the
// existing SSE pipeline.
//
// All pairing functionality is gated behind --pair-enable so the CI
// e2e tests that exercise the legacy plain-text DMs are unaffected.
// When the flag is on, skychat opens an RPC connection to the local
// visor (localhost:3435 by default) and exposes the visor's pair
// methods over its HTTP API.
//
// Handshake flow (Alice initiates with Bob):
//
//  1. Alice's UI: POST /pair {peer_pk: bob}
//  2. Alice's skychat: visor.PairAdd(bob) (status pending);
//     send {type:"pair-invite"} to Bob via legacy direct dial.
//  3. Bob's skychat handleConn: stores invite in pending set,
//     pushes a channel="pair-invite" SSE event so the UI shows
//     accept/decline buttons. NO automatic PairAdd on Bob's side.
//  4. Bob accepts via UI → POST /pair/invites/<alice>/accept:
//     visor.PairAdd(alice) + visor.PairMarkActive(alice); send
//     {type:"pair-ack"} to alice. (Decline path sends
//     {type:"pair-decline"} and drops the pending entry.)
//  5. Alice's skychat handleConn: pair-ack → visor.PairMarkActive(bob);
//     pair-decline → visor.PairRemove(bob). Both sides now in sync.
//
// Legacy plain-text DMs continue to work alongside the structured
// envelope: handleConn first tries to JSON-decode each frame; on
// parse error or unrecognized type it falls back to the plain-text
// path.
package commands

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/visor"
)

// Pairing flags.
var (
	pairEnable       bool
	pairRPCAddr      string
	pairPollInterval time.Duration
)

// pairRPC is the connection to the local visor's RPC. nil when
// --pair-enable is false or the dial failed.
var pairRPC visor.API

// pairPollerCancel stops the SSE-bridge goroutine on shutdown.
var pairPollerCancel context.CancelFunc

// Structured-message envelope. Plain-text DMs continue to be sent
// raw (no JSON envelope) so the existing CI tests are unchanged;
// only pair-control messages use this format.
type pairMsg struct {
	Type   string `json:"type"`
	FromPK string `json:"from_pk,omitempty"`
}

const (
	pairTypeInvite  = "pair-invite"
	pairTypeAck     = "pair-ack"
	pairTypeDecline = "pair-decline"
)

// pendingInvite is one inbound pair-invite that the user hasn't yet
// acted on. Stored in skychat memory only; lost on restart (a peer
// can re-invite if the connection is re-established).
type pendingInvite struct {
	PeerPK     cipher.PubKey `json:"peer_pk"`
	ReceivedAt time.Time     `json:"received_at"`
}

var (
	pendingInvitesMu sync.Mutex
	pendingInvites   = map[cipher.PubKey]pendingInvite{}
)

// pendingPut records a fresh invite. Idempotent — re-invites from the
// same peer just bump the timestamp.
func pendingPut(peerPK cipher.PubKey) {
	pendingInvitesMu.Lock()
	defer pendingInvitesMu.Unlock()
	pendingInvites[peerPK] = pendingInvite{PeerPK: peerPK, ReceivedAt: time.Now().UTC()}
}

// pendingDelete drops a pending invite (called on accept/decline).
func pendingDelete(peerPK cipher.PubKey) {
	pendingInvitesMu.Lock()
	delete(pendingInvites, peerPK)
	pendingInvitesMu.Unlock()
}

// pendingHas reports whether peerPK has an outstanding invite.
func pendingHas(peerPK cipher.PubKey) bool {
	pendingInvitesMu.Lock()
	_, ok := pendingInvites[peerPK]
	pendingInvitesMu.Unlock()
	return ok
}

// pendingList returns a snapshot copy of the pending set.
func pendingList() []pendingInvite {
	pendingInvitesMu.Lock()
	defer pendingInvitesMu.Unlock()
	out := make([]pendingInvite, 0, len(pendingInvites))
	for _, inv := range pendingInvites {
		out = append(out, inv)
	}
	return out
}

// notifyInviteSSE broadcasts a structured envelope to every connected
// SSE client so the browser hears about new invites in real time.
// The hub drops to per-client buffers internally if any consumer is
// slow.
func notifyInviteSSE(peerPK cipher.PubKey, kind string) {
	envelope := map[string]string{
		"channel": "pair-invite",
		"event":   kind, // "received" | "accepted" | "declined"
		"peer":    peerPK.Hex(),
		"ts":      time.Now().UTC().Format(time.RFC3339Nano),
	}
	body, err := json.Marshal(envelope)
	if err != nil {
		return
	}
	hub.broadcast(string(body))
}

// connectPairRPC dials the local visor's RPC and stores the client
// in pairRPC. Best-effort — a failure logs and continues without
// the pair RPC; the watchdog (startPairRPCWatchdog) will retry the
// dial periodically. All actual mutation of pairRPC goes through
// connectPairRPCLocked so the package-level pointer is guarded by
// pairRPCMu.
func connectPairRPC() {
	if !pairEnable {
		return
	}
	if connectPairRPCLocked("startup") == nil {
		appLog("Pairing: initial visor RPC dial failed — watchdog will retry")
	}
}

// startPairPoller bridges visor.PairPoll into the existing SSE
// pipeline. Polls the visor at pairPollInterval, marshals each
// message into the same JSON shape the SSE handler emits, and
// broadcasts via the hub.
//
// The hub drops per-client when their buffer is full so a slow
// SSE consumer doesn't block the poller. The visor's bounded
// inbox already protects against unbounded growth of buffered
// messages.
func startPairPoller(parent context.Context) {
	if !pairEnable {
		return
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec
	pairPollerCancel = cancel

	go func() {
		var since time.Time
		ticker := time.NewTicker(pairPollInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			var msgs []visor.PairMessage
			err := pairRPCCall("PairPoll", func(c visor.API) error {
				out, e := c.PairPoll(since)
				msgs = out
				return e
			})
			if err != nil {
				if !errors.Is(err, errPairRPCUnavailable) {
					appLog("Pairing: PairPoll error: %v", err)
				}
				continue
			}
			for _, m := range msgs {
				if m.TS.After(since) {
					since = m.TS
				}
				envelope := map[string]string{
					"sender":  m.PeerPK.Hex(),
					"message": m.Text,
					"peer":    m.PeerPK.Hex(),
					"ts":      m.TS.Format(time.RFC3339Nano),
					"channel": "pair",
				}
				body, err := json.Marshal(envelope)
				if err != nil {
					appLog("Pairing: marshal SSE message: %v", err)
					continue
				}
				hub.broadcast(string(body))
			}
		}
	}()
}

// stopPairPoller cancels the inbound-poll goroutine. Idempotent.
func stopPairPoller() {
	if pairPollerCancel != nil {
		pairPollerCancel()
		pairPollerCancel = nil
	}
}

// registerPairHTTPHandlers wires the /pair endpoints onto mux.
// No-op when --pair-enable is false.
//
// Pattern precedence: net/http picks the longest matching prefix, so
// /pair/invites and /pair/invites/ shadow /pair/ for the invite
// subtree. This lets the invite handlers be cleanly separate from
// the per-pair item handler without parsing the path twice.
func registerPairHTTPHandlers(ctx context.Context, mux *http.ServeMux) {
	if !pairEnable {
		return
	}
	mux.HandleFunc("/pair", requireAuthFunc(pairRootHandler(ctx)))
	mux.HandleFunc("/pair/invites", requireAuthFunc(pairInvitesListHandler()))
	mux.HandleFunc("/pair/invites/", requireAuthFunc(pairInvitesItemHandler(ctx)))
	mux.HandleFunc("/pair/inbox", requireAuthFunc(pairInboxHandler()))
	mux.HandleFunc("/pair/", requireAuthFunc(pairItemHandler(ctx)))
}

// pairInboxHandler serves GET /pair/inbox — snapshot of inbound pair
// messages newer than ?since=<RFC3339>, capped at ?limit=N (default
// 100, max 1000). Non-destructive; the visor's pairInbox keeps the
// ring intact across reads, so this endpoint and the SSE-bridge
// poller co-exist without consuming each other's view.
//
// Pairs with the CLI's `cli skychat pair poll` command (#2699), which
// hits this exact URL shape and decodes the response as a JSON array
// of visor.PairMessage. Without this handler the path falls into the
// /pair/<pk> catchall and is rejected as an invalid peer-pk.
func pairInboxHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		q := r.URL.Query()
		limit := 100
		if s := q.Get("limit"); s != "" {
			n, err := strconv.Atoi(s)
			if err != nil || n < 1 || n > 1000 {
				http.Error(w, "limit must be 1-1000", http.StatusBadRequest)
				return
			}
			limit = n
		}
		var since time.Time
		if s := q.Get("since"); s != "" {
			t, err := time.Parse(time.RFC3339Nano, s)
			if err != nil {
				// Fall back to plain RFC3339 for callers without sub-second precision.
				t, err = time.Parse(time.RFC3339, s)
				if err != nil {
					http.Error(w, "since must be RFC3339", http.StatusBadRequest)
					return
				}
			}
			since = t
		}
		var msgs []visor.PairMessage
		if err := pairRPCCall("PairPoll", func(c visor.API) error {
			out, e := c.PairPoll(since)
			msgs = out
			return e
		}); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		// Take the newest `limit` entries when the ring window is
		// larger than the caller wants. snapshotAfter returns oldest-
		// first; the CLI prints oldest-first too, so truncating the
		// HEAD (newest) would surprise it — drop the OLDEST extras.
		if len(msgs) > limit {
			msgs = msgs[len(msgs)-limit:]
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(msgs) //nolint:errcheck
	}
}

// pairInvitesListHandler serves GET /pair/invites — current pending
// invites.
func pairInvitesListHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(pendingList()) //nolint:errcheck
	}
}

// pairInvitesItemHandler serves POST /pair/invites/<pk>/accept and
// POST /pair/invites/<pk>/decline.
func pairInvitesItemHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		// Path: /pair/invites/<pk>/<action>
		rest := strings.TrimPrefix(r.URL.Path, "/pair/invites/")
		segments := strings.SplitN(rest, "/", 2)
		if len(segments) != 2 || segments[0] == "" || segments[1] == "" {
			http.Error(w, "expected /pair/invites/<pk>/{accept,decline}", http.StatusBadRequest)
			return
		}
		peer, err := parsePK(segments[0])
		if err != nil {
			http.Error(w, "invalid peer-pk: "+err.Error(), http.StatusBadRequest)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if !pendingHas(peer) {
			http.Error(w, "no pending invite from this peer", http.StatusNotFound)
			return
		}

		switch segments[1] {
		case "accept":
			// Create the local pair (status starts at pending; the
			// initiator's PairMarkActive on receiving our ack will
			// flip them, but on this side we'd want active too).
			if err := pairRPCCall("PairAdd", func(c visor.API) error { return c.PairAdd(peer) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Mark our own side active immediately — the user
			// just consented, so there's no further confirmation
			// needed.
			if err := pairRPCCall("PairMarkActive", func(c visor.API) error { return c.PairMarkActive(peer) }); err != nil {
				appLog("Pairing: PairMarkActive after accept failed: %v", err)
			}
			pendingDelete(peer)
			// Best-effort ack to the initiator. If the legacy
			// connection has dropped, the pair record is still
			// good and the initiator can converge on next contact.
			if err := sendPairControl(ctx, peer, pairTypeAck); err != nil {
				appLog("Pairing: pair-ack to %s failed: %v", peer.Hex(), err)
			}
			w.WriteHeader(http.StatusNoContent)

		case "decline":
			pendingDelete(peer)
			if err := sendPairControl(ctx, peer, pairTypeDecline); err != nil {
				appLog("Pairing: pair-decline to %s failed: %v", peer.Hex(), err)
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "expected /pair/invites/<pk>/{accept,decline}", http.StatusBadRequest)
		}
	}
}

// pairRootHandler serves POST /pair (add) and GET /pair (list).
func pairRootHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			var pairs []visor.PairInfo
			err := pairRPCCall("PairList", func(c visor.API) error {
				out, e := c.PairList()
				pairs = out
				return e
			})
			if err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(pairs) //nolint:errcheck

		case http.MethodPost:
			var body struct {
				PeerPK string `json:"peer_pk"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			peer, err := parsePK(body.PeerPK)
			if err != nil {
				http.Error(w, "invalid peer_pk: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := pairRPCCall("PairAdd", func(c visor.API) error { return c.PairAdd(peer) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Best-effort send pair-invite via legacy direct path.
			// Failure isn't fatal: if the peer is offline, both sides
			// can converge later with each calling PairAdd manually.
			if err := sendPairControl(ctx, peer, pairTypeInvite); err != nil {
				appLog("Pairing: pair-invite to %s failed: %v (record kept; peer can converge later)",
					peer.Hex(), err)
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
	}
}

// pairItemHandler serves DELETE /pair/{pk} and POST /pair/{pk}/message.
func pairItemHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !pairRPCAlive() {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		// Path forms accepted:
		//   /pair/<pk>            DELETE  → remove
		//   /pair/<pk>/message    POST    → send via CXO
		rest := strings.TrimPrefix(r.URL.Path, "/pair/")
		segments := strings.SplitN(rest, "/", 2)
		if len(segments) == 0 || segments[0] == "" {
			http.Error(w, "missing peer-pk in path", http.StatusBadRequest)
			return
		}
		peer, err := parsePK(segments[0])
		if err != nil {
			http.Error(w, "invalid peer-pk: "+err.Error(), http.StatusBadRequest)
			return
		}

		switch {
		case len(segments) == 1 && r.Method == http.MethodDelete:
			if err := pairRPCCall("PairRemove", func(c visor.API) error { return c.PairRemove(peer) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		case len(segments) == 2 && segments[1] == "message" && r.Method == http.MethodPost:
			var body struct {
				Text string `json:"text"`
			}
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				http.Error(w, "invalid body: "+err.Error(), http.StatusBadRequest)
				return
			}
			if err := pairRPCCall("PairSend", func(c visor.API) error { return c.PairSend(peer, body.Text) }); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			w.WriteHeader(http.StatusNoContent)

		default:
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		}
		_ = ctx
	}
}

// handlePairControlFrame attempts to interpret a frame as a pair
// control message. Returns true when the frame was a recognized
// control type and was handled (so handleConn shouldn't surface it
// as a chat message); false when the frame is plain text or
// malformed JSON, in which case handleConn falls through to the
// legacy text path.
func handlePairControlFrame(ctx context.Context, peerPK cipher.PubKey, raw []byte) bool {
	if !pairEnable || !pairRPCAlive() {
		return false
	}
	// Quick reject: pair envelopes are objects, so a non-`{` first
	// byte can't be one. Avoids invoking the JSON parser on every
	// plain-text DM.
	trimmed := bytesTrimSpace(raw)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return false
	}
	var env pairMsg
	if err := json.Unmarshal(trimmed, &env); err != nil || env.Type == "" {
		return false
	}
	switch env.Type {
	case pairTypeInvite:
		// The from_pk in the envelope is informational — we use the
		// legacy connection's RemoteAddr.PubKey as the authoritative
		// identity. The pair is NOT created here: the invite is
		// stored as pending and surfaced to the UI, which calls
		// /pair/invites/<pk>/accept when the user consents.
		appLog("Pairing: pair-invite from %s (pending user consent)", peerPK.Hex())
		pendingPut(peerPK)
		notifyInviteSSE(peerPK, "received")
		return true

	case pairTypeAck:
		// Initiator side: the peer accepted our invite. Promote the
		// pending pair record to active so the UI shows it as
		// confirmed.
		appLog("Pairing: pair-ack from %s — peer accepted", peerPK.Hex())
		if err := pairRPCCall("PairMarkActive", func(c visor.API) error { return c.PairMarkActive(peerPK) }); err != nil {
			appLog("Pairing: PairMarkActive after ack failed: %v", err)
		}
		notifyInviteSSE(peerPK, "accepted")
		return true

	case pairTypeDecline:
		// Initiator side: the peer declined our invite. Drop the
		// pending pair record so it doesn't sit in pending forever.
		appLog("Pairing: pair-decline from %s — peer declined", peerPK.Hex())
		if err := pairRPCCall("PairRemove", func(c visor.API) error { return c.PairRemove(peerPK) }); err != nil {
			appLog("Pairing: PairRemove after decline failed: %v", err)
		}
		notifyInviteSSE(peerPK, "declined")
		return true
	}
	_ = ctx
	return false
}

// sendPairControl sends a structured pair-control message to peerPK
// via the legacy direct path. Reuses the existing conns map and
// dial logic from messageHandler — we don't call messageHandler
// directly because that's HTTP-shaped.
func sendPairControl(ctx context.Context, peerPK cipher.PubKey, msgType string) error {
	body, err := json.Marshal(pairMsg{Type: msgType})
	if err != nil {
		return err
	}
	conn, err := dialOrReusePeerConn(ctx, peerPK)
	if err != nil {
		return err
	}
	return conn.WriteFrame(body)
}

// dialOrReusePeerConn returns a live framed connection to peerPK,
// dialing over skynet (or dmsg as fallback) if none exists. Mirrors
// what messageHandler does but doesn't take the HTTP request
// context's shape.
func dialOrReusePeerConn(ctx context.Context, peerPK cipher.PubKey) (*framedConn, error) {
	connsMu.Lock()
	conn, ok := conns[peerPK]
	connsMu.Unlock()
	if ok {
		return conn, nil
	}

	var lastErr error
	for _, netType := range []appnet.Type{appnet.TypeSkynet, appnet.TypeDmsg} {
		addr := appnet.Addr{Net: netType, PubKey: peerPK, Port: 1}
		var dialed net.Conn
		err := r.Do(ctx, func() error {
			c, dialErr := appCl.Dial(addr)
			if dialErr != nil {
				return dialErr
			}
			dialed = c
			return nil
		})
		if err == nil && dialed != nil {
			fc := newFramedConn(dialed)
			connsMu.Lock()
			conns[peerPK] = fc
			connsMu.Unlock()
			go handleConn(fc) //nolint:gosec
			return fc, nil
		}
		lastErr = err
	}
	if lastErr == nil {
		lastErr = errors.New("pairing: no network type succeeded")
	}
	return nil, lastErr
}

// parsePK parses a hex-encoded public key and returns a cipher.PubKey.
func parsePK(s string) (cipher.PubKey, error) {
	var pk cipher.PubKey
	if err := pk.UnmarshalText([]byte(s)); err != nil {
		return cipher.PubKey{}, err
	}
	return pk, nil
}

// bytesTrimSpace drops leading whitespace bytes from b — a tiny
// helper kept here to avoid importing strings or bytes for the
// JSON-vs-text fast-reject in handlePairControlFrame.
func bytesTrimSpace(b []byte) []byte {
	for i := 0; i < len(b); i++ {
		switch b[i] {
		case ' ', '\t', '\n', '\r':
			continue
		}
		return b[i:]
	}
	return nil
}
