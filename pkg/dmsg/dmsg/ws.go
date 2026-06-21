//go:build !(tinygo && js && wasm)

// Package dmsg pkg/dmsg/dmsg/ws.go: dmsg-over-WebSocket.
//
// A WebSocket connection is a bidirectional, ordered, reliable byte pipe over
// HTTP(S) — exactly what dmsg's session layer wants. coder/websocket's NetConn
// adapts it to a net.Conn, so a WS session reuses the EXISTING Noise+yamux
// stack (makeServerSession/makeClientSession + yamux) with no new session
// semantics, no new mux, and no new crypto — the per-hop Noise handshake and
// the end-to-end per-stream noise are both unchanged. WS is purely a different
// way to carry the same bytes.
//
// Why it matters: a browser tab (JS or Go-js/wasm) cannot open a raw TCP or
// UDP socket, so neither the legacy TCP transport nor dmsg-over-QUIC can reach
// the mesh from a browser. WebSocket is the one transport the browser sandbox
// allows, which makes a WASM dmsg *client* — a real PK-authenticated leaf node
// running in a web page — possible. It is also the universal fallback for
// restrictive networks that only permit HTTP(S)/443 egress and for CDN
// fronting. coder/websocket is used precisely because it also compiles to
// js/wasm, so the same dial path serves the future browser client.
package dmsg

import (
	"context"
	"fmt"

	"github.com/coder/websocket"
	"github.com/hashicorp/yamux"

	"github.com/skycoin/skywire/pkg/dmsg/disc"
)

// dialSessionWS dials a dmsg server's WebSocket endpoint (Server.AddressWS) and
// builds a yamux+Noise client session over it — the WS analog of the TCP dial
// path in dialSession. The returned session is fully set up (Noise handshake
// done, yamux client started); dialSession's shared store/serve logic handles
// the rest. Used when the client prefers WS (Config.PreferWS) — always the case
// for the js/wasm build, which cannot dial TCP/QUIC.
func (ce *Client) dialSessionWS(ctx context.Context, entry *disc.Entry) (ClientSession, error) {
	c, _, err := websocket.Dial(ctx, entry.Server.AddressWS, nil)
	if err != nil {
		return ClientSession{}, fmt.Errorf("ws: dial %q: %w", entry.Server.AddressWS, err)
	}
	conn := websocket.NetConn(context.Background(), c, websocket.MessageBinary)

	dSes, err := makeClientSession(&ce.EntityCommon, ce.porter, conn, entry.Static)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	dSes.sm.yamux, err = yamux.Client(conn, YamuxConfig())
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return ClientSession{}, err
	}
	ce.log.Infof("ws stream session initial for %s", dSes.RemotePK().String())
	return dSes, nil
}
