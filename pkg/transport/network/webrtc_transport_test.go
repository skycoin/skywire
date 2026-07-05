//go:build !tinygo

// Package network webrtc_transport_test.go: transport-level end-to-end test for
// the WEBRTC (webrtc) skywire transport. Unlike webrtc_native_test.go (which
// drives only the raw pion carrier over a net.Pipe standing in for signaling)
// this exercises the full webrtcClient path: dmsg signaling stream (dialSignaling
// → webrtcSignalPort) → SDP offer/answer + ICE over dmsg → direct DataChannel →
// initTransport (Noise + yamux) → a real encrypted skywire transport with mutual
// PK auth. It is the WEBRTC analog of TestWS_DialAcceptEndToEnd / TestStcp_
// DialAcceptEndToEnd, and mirrors what the Docker e2e test does over live visors.
//
// ICE uses host candidates only (no STUN/iceURLs): the two peers share one host,
// exactly as in the carrier test.
package network

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appevent"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestWebRTC_DialAcceptEndToEnd establishes a real WebRTC transport between two
// visors: the answerer listens for signaling on its dmsg webrtcSignalPort, the
// offerer dials, and the two negotiate a direct DataChannel over dmsg-carried
// signaling. A byte round-trips both ways over the encrypted transport, proving
// the whole webrtcClient surface (Dial/serve/webrtcListener + signaling) works.
func TestWebRTC_DialAcceptEndToEnd(t *testing.T) {
	// In-process dmsg fabric (2 servers, 2 clients) to carry WebRTC signaling.
	env := dmsgtest.NewEnv(t, dmsgtest.DefaultTimeout)
	require.NoError(t, env.Startup(dmsgtest.DefaultTimeout, 2, 2, &dmsg.Config{MinSessions: 2}))
	t.Cleanup(env.Shutdown)

	dcA := env.AllClients()[0] // offerer / dialer
	dcB := env.AllClients()[1] // answerer / listener
	eb := appevent.NewBroadcaster(logging.MustGetLogger("eb"), time.Second)

	// B: accepts WebRTC transports (signals over its dmsg webrtcSignalPort).
	fB := &ClientFactory{PK: dcB.LocalPK(), SK: dcB.LocalSK(), DmsgC: dcB, EB: eb}
	clientB, err := fB.MakeClient(types.WEBRTC, 0)
	require.NoError(t, err)
	require.NoError(t, clientB.Start())
	defer clientB.Close() //nolint:errcheck
	lis, err := clientB.Listen(9)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	// A: dials B by PK; signaling is resolved over dmsg (no PK table / AR needed).
	fA := &ClientFactory{PK: dcA.LocalPK(), SK: dcA.LocalSK(), DmsgC: dcA, EB: eb}
	clientA, err := fA.MakeClient(types.WEBRTC, 0)
	require.NoError(t, err)
	defer clientA.Close() //nolint:errcheck

	acceptCh := make(chan Transport, 1)
	go func() {
		if tp, aerr := lis.AcceptTransport(); aerr == nil {
			acceptCh <- tp
		}
	}()

	// Generous timeout: dmsg signaling + ICE (DTLS+SCTP) DataChannel negotiation.
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()

	dialed, err := clientA.Dial(ctx, dcB.LocalPK(), 9)
	require.NoError(t, err)
	require.Equal(t, dcB.LocalPK(), dialed.RemotePK())
	require.Equal(t, uint16(9), dialed.RemotePort())

	var accepted Transport
	select {
	case accepted = <-acceptCh:
	case <-time.After(40 * time.Second):
		t.Fatal("listener did not accept the dialed WebRTC transport")
	}
	require.Equal(t, dcA.LocalPK(), accepted.RemotePK(), "answerer must learn the offerer's skywire PK")

	// Data flows across the encrypted WebRTC DataChannel, both directions.
	go func() { _, _ = dialed.Write([]byte("hi")) }() //nolint:errcheck
	buf := make([]byte, 2)
	require.NoError(t, accepted.SetReadDeadline(time.Now().Add(15*time.Second)))
	n, err := accepted.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "hi", string(buf[:n]))

	go func() { _, _ = accepted.Write([]byte("yo")) }() //nolint:errcheck
	buf2 := make([]byte, 2)
	require.NoError(t, dialed.SetReadDeadline(time.Now().Add(15*time.Second)))
	n, err = dialed.Read(buf2)
	require.NoError(t, err)
	require.Equal(t, "yo", string(buf2[:n]))

	require.NoError(t, dialed.Close())
	require.NoError(t, accepted.Close())
}
