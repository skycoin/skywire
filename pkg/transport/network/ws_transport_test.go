//go:build !tinygo

// Package network ws_transport_test.go: transport-level end-to-end tests for the
// WS (swsr) skywire transport. Unlike ws_native_test.go (which exercises only the
// raw WS carrier — wsDial/wsListener bytes) these drive the full wsClient path:
// PK/AR endpoint resolution → wsDial → initTransport (Noise + yamux) → a real
// encrypted skywire transport with mutual PK auth, mirroring
// TestStcp_DialAcceptEndToEnd for stcp and TestQUICConnMutualPKAuth for QUIC.
package network

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/transport/network/stcp"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// wsRoundTrip drives a full WS transport: B dials A on skywire port 9, the
// listener accepts, and a byte round-trips both ways over the encrypted transport.
// It asserts mutual PK/port authentication was carried by the Noise handshake.
func wsRoundTrip(t *testing.T, clientB Client, lis Listener, pkA, pkB cipher.PubKey) {
	t.Helper()

	acceptCh := make(chan Transport, 1)
	go func() {
		if tp, aerr := lis.AcceptTransport(); aerr == nil {
			acceptCh <- tp
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// B dials A: Dial -> resolve ws:// URL -> wsDial -> initTransport ->
	// doHandshake -> encrypt; A's accept loop runs the responder handshake and
	// introduces the transport to the listener.
	dialed, err := clientB.Dial(ctx, pkA, 9)
	require.NoError(t, err)
	require.Equal(t, pkA, dialed.RemotePK())
	require.Equal(t, uint16(9), dialed.RemotePort())

	var accepted Transport
	select {
	case accepted = <-acceptCh:
	case <-time.After(20 * time.Second):
		t.Fatal("listener did not accept the dialed WS transport")
	}
	require.Equal(t, pkB, accepted.RemotePK(), "server must learn the dialer's skywire PK")

	// Data flows across the encrypted WS transport, both directions.
	go func() { _, _ = dialed.Write([]byte("hi")) }() //nolint:errcheck
	buf := make([]byte, 2)
	require.NoError(t, accepted.SetReadDeadline(time.Now().Add(10*time.Second)))
	n, err := accepted.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hi", string(buf[:n]))

	go func() { _, _ = accepted.Write([]byte("yo")) }() //nolint:errcheck
	buf2 := make([]byte, 2)
	require.NoError(t, dialed.SetReadDeadline(time.Now().Add(10*time.Second)))
	n, err = dialed.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, "yo", string(buf2[:n]))

	// Close both ends. The WS close handshake (coder/websocket) can return a
	// deadline error when both sides tear down at once — benign here (the data
	// round-trip above already proved the transport), so we don't assert on it,
	// matching the carrier test (ws_native_test.go).
	dialed.Close()   //nolint:errcheck,gosec
	accepted.Close() //nolint:errcheck,gosec
}

// TestWS_DialAcceptEndToEnd establishes a real WS transport between two visors
// where the dialer resolves the listener's ws:// URL from an explicit WSTable
// (the wss/pk_table path — the browser-autoconnect and static-peer analog).
func TestWS_DialAcceptEndToEnd(t *testing.T) {
	pkA, skA := keyPair(t)
	pkB, skB := keyPair(t)
	eb := appevent.NewBroadcaster(logging.MustGetLogger("eb"), time.Second)

	// A: runs the WS HTTP server on an ephemeral local TCP address.
	fA := &ClientFactory{PK: pkA, SK: skA, ListenAddr: "127.0.0.1:0", EB: eb}
	clientA, err := fA.MakeClient(types.WS, 0)
	require.NoError(t, err)
	require.NoError(t, clientA.Start())
	defer clientA.Close() //nolint:errcheck
	addrA, err := clientA.LocalAddr()
	require.NoError(t, err)
	lis, err := clientA.Listen(9)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	// B: knows A's ws:// URL via its WSTable (PK -> ws://host:port/).
	fB := &ClientFactory{
		PK: pkB, SK: skB, EB: eb,
		WSTable: stcp.NewTable(map[cipher.PubKey]string{pkA: "ws://" + addrA.String() + "/"}),
	}
	clientB, err := fB.MakeClient(types.WS, 0)
	require.NoError(t, err)
	defer clientB.Close() //nolint:errcheck

	wsRoundTrip(t, clientB, lis, pkA, pkB)
}

// TestWS_ResolveViaAR establishes a WS transport where the dialer has NO table
// entry and instead resolves the listener's endpoint from the address resolver —
// the native production path (resolveWSURLViaAR): WS rides the stcpr cmux port, so
// the peer's stcpr AR record IS its ws:// endpoint. This is the exact resolution
// a native `tp add -t swsr <pk>` performs, and what the Docker e2e test exercises.
func TestWS_ResolveViaAR(t *testing.T) {
	pkA, skA := keyPair(t)
	pkB, skB := keyPair(t)
	eb := appevent.NewBroadcaster(logging.MustGetLogger("eb"), time.Second)

	fA := &ClientFactory{PK: pkA, SK: skA, ListenAddr: "127.0.0.1:0", EB: eb}
	clientA, err := fA.MakeClient(types.WS, 0)
	require.NoError(t, err)
	require.NoError(t, clientA.Start())
	defer clientA.Close() //nolint:errcheck
	addrA, err := clientA.LocalAddr()
	require.NoError(t, err)
	lis, err := clientA.Listen(9)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	// B resolves A's stcpr record from the AR (no WSTable). The WS client wraps
	// the returned host:port as ws://host:port/.
	ar := &addrresolver.MockAPIClient{}
	ar.On("Resolve", mock.Anything, string(types.STCPR), pkA).
		Return(addrresolver.VisorData{RemoteAddr: addrA.String()}, nil)

	fB := &ClientFactory{PK: pkB, SK: skB, EB: eb, ARClient: ar}
	clientB, err := fB.MakeClient(types.WS, 0)
	require.NoError(t, err)
	defer clientB.Close() //nolint:errcheck

	wsRoundTrip(t, clientB, lis, pkA, pkB)
	ar.AssertExpectations(t)
}
