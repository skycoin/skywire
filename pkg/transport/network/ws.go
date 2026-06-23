// Package network pkg/transport/network/ws.go
//
// WS is a first-class skywire transport type: a direct visor-to-visor link over
// a WebSocket, with the peer's wss:// endpoint resolved from a PK table (like
// stcp, but over WebSocket instead of raw TCP). It is distinct from the dmsg
// WebSocket carrier, which reaches a dmsg server rather than a peer visor.
//
// A browser visor can DIAL this transport (the browser's WebSocket API) but
// cannot accept it (no listening socket) — so server-visors run the WS listener
// (ws_native.go) while the dial side is build-tagged: coder/websocket on native,
// the browser-native WebSocket under TinyGo/js (ws_tinygo.go). The accepted /
// dialed WebSocket is adapted to a net.Conn and flows through the SAME
// genericClient initTransport (Noise + yamux) path as every other carrier.
package network

import (
	"context"
	"errors"
	"io"
	"net"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// wsClient is the WS-transport implementation of Client. table maps a peer PK to
// its wss:// (or ws://) URL.
type wsClient struct {
	*genericClient
	table stcp.PKTable
	// sharedListener, when set, is the TCP listener the WS HTTP server serves over
	// instead of binding its own — the HTTP virtual listener of a unified
	// transport_port socket (see tcpDemux, !tinygo). net.Listener so this stays in
	// the untagged file; only ws_native.go (the server) consumes it.
	sharedListener net.Listener
}

func newWS(generic *genericClient, table stcp.PKTable) Client {
	client := &wsClient{genericClient: generic, table: table}
	client.netType = types.WS
	return client
}

// ErrWSEntryNotFound is returned when the requested PK has no WS URL in the table.
var ErrWSEntryNotFound = errors.New("ws: entry not found in PK table")

// Dial implements Client: resolve the peer's WS URL, dial it (build-tagged
// carrier: coder/websocket on native, browser WebSocket on TinyGo/js), and wrap
// the resulting net.Conn in a skywire transport.
func (c *wsClient) Dial(ctx context.Context, rPK cipher.PubKey, rPort uint16) (Transport, error) {
	if c.isClosed() {
		return nil, io.ErrClosedPipe
	}

	url, ok := c.table.Addr(rPK)
	if !ok {
		return nil, ErrWSEntryNotFound
	}
	c.log.Debugf("Dialing WS %v @ %s", rPK, url)

	conn, err := wsDial(ctx, url)
	if err != nil {
		return nil, err
	}

	tp, err := c.initTransport(ctx, conn, rPK, rPort)
	if err != nil {
		conn.Close() //nolint:errcheck,gosec
		return nil, err
	}
	return tp, nil
}
