// Package router pkg/router/datagram_setup_test.go integration test
// for the route-setup-side datagram sibling (#2607 stage-4b). Verifies
// the receive path end to end WITH crypto: a datagram sealed by an
// initiator-side cipher (same master key, derived from the shared
// session key export) flows through the responder's handleTransportPacket
// and is decrypted by the in-cipher that buildDatagramSibling installed.
package router

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/routing"
)

// fakeSessionConn is a net.Conn that only implements ExportKey. It
// stands in for the *noise.Conn that network.EncryptConn returns.
type fakeSessionConn struct {
	net.Conn
	key [32]byte
}

func (f fakeSessionConn) ExportKey(string) ([32]byte, error) { return f.key, nil }

func TestBuildDatagramSiblingReceivePathWithCrypto(t *testing.T) {
	r := newDispatchTestRouter()
	r.datagramPorts = make(map[routing.Port]struct{})

	// Responder identity (this router) + remote initiator identity.
	respPK, respSK := cipher.GenerateKeyPair()
	initPK, _ := cipher.GenerateKeyPair()

	keyRouteID := routing.RouteID(55)
	consume := routing.ConsumeRule(time.Hour, keyRouteID, respPK, initPK, 10, 20)
	require.NoError(t, r.rt.SaveRule(consume))
	desc := consume.RouteDescriptor()

	// Build the responder's datagram sibling exactly as saveRouteGroupRules
	// would on the accept side: Initiator=false, keyed off the shared
	// session key via a fake encrypted conn.
	session := fakeSessionConn{key: [32]byte{1, 2, 3}}
	nsConf := noise.Config{LocalPK: respPK, LocalSK: respSK, RemotePK: initPK, Initiator: false}
	rules := routing.EdgeRules{Desc: desc, Forward: consume, Reverse: consume}
	r.buildDatagramSibling(rules, nsConf, session, nil)

	dg, ok := r.datagramRouteGroup(desc)
	require.True(t, ok, "buildDatagramSibling must register a datagram route group")
	require.NotNil(t, dg)

	// Initiator side: derive the SAME master key from the SAME session,
	// seal a payload with the initiator-direction cipher bound to the
	// receiver (responder) PK — mirroring what the initiator's
	// DatagramRouteGroup.WriteTo does.
	master, err := deriveDatagramMasterKey(session)
	require.NoError(t, err)
	initOut, err := NewDatagramCipher(master, RoleInitiator, respPK, nil)
	require.NoError(t, err)

	payload := []byte("udp-voice-frame")
	sealed, err := initOut.Seal(uint32(keyRouteID), payload)
	require.NoError(t, err)

	pkt, err := routing.MakeDatagramPacket(keyRouteID, sealed)
	require.NoError(t, err)

	// Drive it through the real dispatch.
	require.NoError(t, r.handleTransportPacket(context.Background(), pkt))

	// The sibling decrypted + delivered the original plaintext.
	require.NoError(t, dg.SetReadDeadline(time.Now().Add(2*time.Second)))
	buf := make([]byte, 1500)
	n, _, err := dg.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf[:n])
	assert.Zero(t, dg.AEADAuthFailures(), "no auth failures for a correctly-sealed datagram")
}

// TestBuildDatagramSiblingSkipsUnencrypted verifies a route whose conn
// exposes no session key export (unencrypted/legacy) gets NO sibling —
// the datagram AEAD has no key to derive, so it must skip rather than
// register a keyless group.
func TestBuildDatagramSiblingSkipsUnencrypted(t *testing.T) {
	r := newDispatchTestRouter()
	r.datagramPorts = make(map[routing.Port]struct{})

	respPK, respSK := cipher.GenerateKeyPair()
	initPK, _ := cipher.GenerateKeyPair()
	consume := routing.ConsumeRule(time.Hour, routing.RouteID(1), respPK, initPK, 1, 2)
	desc := consume.RouteDescriptor()
	nsConf := noise.Config{LocalPK: respPK, LocalSK: respSK, RemotePK: initPK, Initiator: false}
	rules := routing.EdgeRules{Desc: desc, Forward: consume, Reverse: consume}

	// A plain net.Conn (no ExportKey) — e.g. an unencrypted route.
	r.buildDatagramSibling(rules, nsConf, fakeNoSessionConn{}, nil)

	_, ok := r.datagramRouteGroup(desc)
	assert.False(t, ok, "no sibling should be registered for an unencrypted route")
}

// fakeNoSessionConn is a net.Conn with no ExportKey method.
type fakeNoSessionConn struct{ net.Conn }

// TestDatagramPortRegistry covers the accept-side intent registry.
func TestDatagramPortRegistry(t *testing.T) {
	r := newDispatchTestRouter()

	assert.False(t, r.isDatagramPort(80))
	r.RegisterDatagramPort(80)
	assert.True(t, r.isDatagramPort(80))
	assert.False(t, r.isDatagramPort(81))
	r.UnregisterDatagramPort(80)
	assert.False(t, r.isDatagramPort(80))
}

// TestBuildDatagramSiblingAcceptSideOffers verifies that an accept-side
// sibling (Initiator=false) is handed to AcceptDatagram with the right
// local port, while a dial-side sibling (Initiator=true) is NOT offered
// (the dialer keeps it via DialRoutesDatagram).
func TestBuildDatagramSiblingAcceptSideOffers(t *testing.T) {
	r := newDispatchTestRouter()

	localPK, localSK := cipher.GenerateKeyPair()
	remotePK, _ := cipher.GenerateKeyPair()
	session := fakeSessionConn{key: [32]byte{4, 5, 6}}

	// Accept side: dst port 7777 is the local serving port.
	consume := routing.ConsumeRule(time.Hour, routing.RouteID(3), localPK, remotePK, 1234, 7777)
	desc := consume.RouteDescriptor()
	acceptConf := noise.Config{LocalPK: localPK, LocalSK: localSK, RemotePK: remotePK, Initiator: false}
	rules := routing.EdgeRules{Desc: desc, Forward: consume, Reverse: consume}
	r.buildDatagramSibling(rules, acceptConf, session, nil)

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	dg, dstPort, err := r.AcceptDatagram(ctx)
	require.NoError(t, err)
	require.NotNil(t, dg)
	assert.Equal(t, routing.Port(7777), dstPort, "accept must surface the local serving port")

	// Dial side: same shape but Initiator=true must NOT offer.
	dialConf := noise.Config{LocalPK: localPK, LocalSK: localSK, RemotePK: remotePK, Initiator: true}
	r.buildDatagramSibling(rules, dialConf, session, nil)

	ctx2, cancel2 := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel2()
	_, _, err = r.AcceptDatagram(ctx2)
	assert.ErrorIs(t, err, context.DeadlineExceeded, "dial-side sibling must not be offered to AcceptDatagram")
}
