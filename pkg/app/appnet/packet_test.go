// Package appnet pkg/app/appnet/packet_test.go: tests for the
// UDP/datagram app-API surface. Stage 5 of #2607.
//
// Stage 5 ships the API; the route-setup integration that actually
// constructs a working DatagramPacketConn lands in a follow-up PR.
// Tests here cover (a) the interface contract, (b) the
// PacketNetworker opt-in / not-supported error path, and (c) the
// top-level DialPacket / DialPacketContext entry points.

package appnet

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

// stubPacketConn satisfies DatagramPacketConn for tests. Read/Write
// are no-ops; the surface is what matters here.
type stubPacketConn struct {
	maxPayload       int
	inboundDropped   uint64
	aeadAuthFailures uint64
}

func (s *stubPacketConn) ReadFrom(_ []byte) (int, net.Addr, error)  { return 0, nil, nil }
func (s *stubPacketConn) WriteTo(p []byte, _ net.Addr) (int, error) { return len(p), nil }
func (s *stubPacketConn) Close() error                              { return nil }
func (s *stubPacketConn) LocalAddr() net.Addr                       { return nil }
func (s *stubPacketConn) SetDeadline(_ time.Time) error             { return nil }
func (s *stubPacketConn) SetReadDeadline(_ time.Time) error         { return nil }
func (s *stubPacketConn) SetWriteDeadline(_ time.Time) error        { return nil }
func (s *stubPacketConn) MaxPayload() int                           { return s.maxPayload }
func (s *stubPacketConn) InboundDropped() uint64                    { return atomic.LoadUint64(&s.inboundDropped) }
func (s *stubPacketConn) AEADAuthFailures() uint64                  { return atomic.LoadUint64(&s.aeadAuthFailures) }

// fakePacketNetworker satisfies the base Networker contract AND the
// PacketNetworker opt-in. Pure local-test stub — no real network
// activity.
type fakePacketNetworker struct {
	dialPacketCalls int
	dialPacketAddr  Addr
}

func (f *fakePacketNetworker) Dial(_ Addr) (net.Conn, error)              { return nil, nil }
func (f *fakePacketNetworker) DialContext(_ context.Context, _ Addr) (net.Conn, error) {
	return nil, nil
}
func (f *fakePacketNetworker) Ping(_ cipher.PubKey, _ Addr) (net.Conn, error) { return nil, nil }
func (f *fakePacketNetworker) PingContext(_ context.Context, _ cipher.PubKey, _ Addr) (net.Conn, error) {
	return nil, nil
}
func (f *fakePacketNetworker) Listen(_ Addr) (net.Listener, error) { return nil, nil }
func (f *fakePacketNetworker) ListenContext(_ context.Context, _ Addr) (net.Listener, error) {
	return nil, nil
}

func (f *fakePacketNetworker) DialPacketContext(_ context.Context, addr Addr) (DatagramPacketConn, error) {
	f.dialPacketCalls++
	f.dialPacketAddr = addr
	return &stubPacketConn{maxPayload: 65511}, nil
}

// fakeBaseNetworker is base-Networker-only — used to prove the
// PacketNetworker-not-supported error path.
type fakeBaseNetworker struct{}

func (f *fakeBaseNetworker) Dial(_ Addr) (net.Conn, error)              { return nil, nil }
func (f *fakeBaseNetworker) DialContext(_ context.Context, _ Addr) (net.Conn, error) {
	return nil, nil
}
func (f *fakeBaseNetworker) Ping(_ cipher.PubKey, _ Addr) (net.Conn, error) { return nil, nil }
func (f *fakeBaseNetworker) PingContext(_ context.Context, _ cipher.PubKey, _ Addr) (net.Conn, error) {
	return nil, nil
}
func (f *fakeBaseNetworker) Listen(_ Addr) (net.Listener, error) { return nil, nil }
func (f *fakeBaseNetworker) ListenContext(_ context.Context, _ Addr) (net.Listener, error) {
	return nil, nil
}

func TestDialPacketResolvesAndCallsNetworker(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()

	pn := &fakePacketNetworker{}
	require.NoError(t, AddNetworker(TypeSkynet, pn))

	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 1234}

	conn, err := DialPacket(addr)
	require.NoError(t, err)
	require.NotNil(t, conn)
	assert.Equal(t, 65511, conn.MaxPayload(), "MaxPayload should reflect AEAD overhead")

	assert.Equal(t, 1, pn.dialPacketCalls)
	assert.Equal(t, addr, pn.dialPacketAddr)
}

func TestDialPacketContextPassesContext(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()

	pn := &fakePacketNetworker{}
	require.NoError(t, AddNetworker(TypeSkynet, pn))

	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 1234}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	_, err := DialPacketContext(ctx, addr)
	require.NoError(t, err)
}

func TestDialPacketUnsupportedNetworkerErrors(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()

	// Register a Networker that does NOT implement PacketNetworker.
	require.NoError(t, AddNetworker(TypeSkynet, &fakeBaseNetworker{}))

	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 1234}

	_, err := DialPacket(addr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrPacketNetworkerNotSupported)
}

func TestDialPacketNoNetworkerForTypeErrors(t *testing.T) {
	ClearNetworkers()
	defer ClearNetworkers()
	// No AddNetworker call — DialPacket must surface
	// ErrNoSuchNetworker.

	pk, _ := cipher.GenerateKeyPair()
	addr := Addr{Net: TypeSkynet, PubKey: pk, Port: 1234}

	_, err := DialPacket(addr)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNoSuchNetworker)
}

func TestDatagramPacketConnInterfaceSurface(t *testing.T) {
	// Ensure DatagramPacketConn includes net.PacketConn (callers
	// can pass a DatagramPacketConn anywhere a net.PacketConn is
	// expected — e.g. into libraries that don't yet know about
	// MaxPayload / InboundDropped).
	var conn DatagramPacketConn = &stubPacketConn{maxPayload: 65511}
	var _ net.PacketConn = conn

	// And the extension methods are reachable.
	assert.Equal(t, 65511, conn.MaxPayload())
	assert.Equal(t, uint64(0), conn.InboundDropped())
	assert.Equal(t, uint64(0), conn.AEADAuthFailures())
}
