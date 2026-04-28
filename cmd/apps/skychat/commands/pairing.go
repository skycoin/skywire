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
//  2. Skychat: visor.PairAdd(bob); send {type:"pair-invite", from_pk:alice}
//     to Bob via legacy direct dial.
//  3. Bob's skychat handleConn: recognizes pair-invite, calls
//     visor.PairAdd(alice) on his side; replies {type:"pair-ack",
//     from_pk:bob} on the same legacy connection.
//  4. Alice's skychat handleConn: recognizes pair-ack — both sides
//     now have publishers up; CXO subscribers connect on the next
//     publish-batch cycle.
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
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
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
	pairTypeInvite = "pair-invite"
	pairTypeAck    = "pair-ack"
)

// connectPairRPC dials the local visor's RPC and stores the client
// in pairRPC. Best-effort — a failure logs and disables pairing for
// this run rather than failing skychat startup.
func connectPairRPC() {
	if !pairEnable {
		return
	}
	const dialTimeout = 5 * time.Second
	conn, err := net.DialTimeout("tcp", pairRPCAddr, dialTimeout)
	if err != nil {
		appLog("Pairing: visor RPC dial %s failed: %v — pairing disabled", pairRPCAddr, err)
		return
	}
	log := logging.MustGetLogger(fmt.Sprintf("skychat-pair-rpc://%s", pairRPCAddr))
	pairRPC = visor.NewRPCClient(log, conn, visor.RPCPrefix, 30*time.Second)
	appLog("Pairing: connected to visor RPC at %s", pairRPCAddr)
}

// startPairPoller bridges visor.PairPoll into the existing SSE
// pipeline. Polls the visor at pairPollInterval, marshals each
// message into the same JSON shape the SSE handler emits, and
// pushes onto clientCh.
//
// Drops if clientCh is full so a slow SSE client doesn't block the
// poller. The visor's bounded inbox already protects against
// unbounded growth of buffered messages.
func startPairPoller(parent context.Context) {
	if !pairEnable || pairRPC == nil {
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
			msgs, err := pairRPC.PairPoll(since)
			if err != nil {
				appLog("Pairing: PairPoll error: %v", err)
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
				select {
				case clientCh <- string(body):
				default:
				}
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
func registerPairHTTPHandlers(ctx context.Context) {
	if !pairEnable {
		return
	}
	http.HandleFunc("/pair", pairRootHandler(ctx))
	http.HandleFunc("/pair/", pairItemHandler(ctx))
}

// pairRootHandler serves POST /pair (add) and GET /pair (list).
func pairRootHandler(ctx context.Context) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if pairRPC == nil {
			http.Error(w, "pairing disabled (visor RPC unavailable)", http.StatusServiceUnavailable)
			return
		}
		switch r.Method {
		case http.MethodGet:
			pairs, err := pairRPC.PairList()
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
			if err := pairRPC.PairAdd(peer); err != nil {
				http.Error(w, err.Error(), http.StatusInternalServerError)
				return
			}
			// Best-effort send pair-invite via legacy direct path.
			// Failure isn't fatal: if the peer is offline, both sides
			// can converge later with each calling PairAdd manually.
			if err := sendPairControl(ctx, peer, pairTypeInvite); err != nil {
				appLog("Pairing: pair-invite to %s failed: %v (record kept; peer can converge later)",
					peer.Hex()[:8], err)
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
		if pairRPC == nil {
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
			if err := pairRPC.PairRemove(peer); err != nil {
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
			if err := pairRPC.PairSend(peer, body.Text); err != nil {
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
	if !pairEnable || pairRPC == nil {
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
		// identity. Either way they should match.
		appLog("Pairing: pair-invite from %s", peerPK.Hex()[:8])
		if err := pairRPC.PairAdd(peerPK); err != nil {
			appLog("Pairing: PairAdd from invite failed: %v", err)
			return true
		}
		// Best-effort ack.
		if err := sendPairControlOnExistingConn(peerPK, pairTypeAck); err != nil {
			appLog("Pairing: pair-ack to %s failed: %v", peerPK.Hex()[:8], err)
		}
		return true

	case pairTypeAck:
		appLog("Pairing: pair-ack from %s — both sides paired", peerPK.Hex()[:8])
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
	_, err = conn.Write(body)
	return err
}

// sendPairControlOnExistingConn writes a pair-control message on an
// already-open conns[peerPK] entry, used from handleConn where we
// already have the connection live.
func sendPairControlOnExistingConn(peerPK cipher.PubKey, msgType string) error {
	connsMu.Lock()
	conn, ok := conns[peerPK]
	connsMu.Unlock()
	if !ok {
		return errors.New("pairing: no live connection to peer")
	}
	body, err := json.Marshal(pairMsg{Type: msgType})
	if err != nil {
		return err
	}
	_, err = conn.Write(body)
	return err
}

// dialOrReusePeerConn returns a live connection to peerPK, dialing
// over skynet (or dmsg as fallback) if none exists. Mirrors what
// messageHandler does but doesn't take the HTTP request context's
// shape.
func dialOrReusePeerConn(ctx context.Context, peerPK cipher.PubKey) (net.Conn, error) {
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
			connsMu.Lock()
			conns[peerPK] = dialed
			connsMu.Unlock()
			go handleConn(dialed) //nolint:gosec
			return dialed, nil
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
