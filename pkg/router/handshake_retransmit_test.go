// Package router pkg/router/handshake_retransmit_test.go
package router

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// countingTransport is a working transport that records how many times Write
// was called, so a test can observe (re)transmitted handshake packets.
type countingTransport struct {
	closed   chan struct{}
	writes   int32
	pk1, pk2 cipher.PubKey
}

func newCountingTransport() *countingTransport {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	return &countingTransport{closed: make(chan struct{}), pk1: pk1, pk2: pk2}
}

func (t *countingTransport) Write(b []byte) (int, error) {
	select {
	case <-t.closed:
		return 0, net.ErrClosed
	default:
		atomic.AddInt32(&t.writes, 1)
		return len(b), nil
	}
}
func (t *countingTransport) writeCount() int { return int(atomic.LoadInt32(&t.writes)) }

func (t *countingTransport) Read([]byte) (int, error) { <-t.closed; return 0, net.ErrClosed }
func (t *countingTransport) Close() error {
	select {
	case <-t.closed:
	default:
		close(t.closed)
	}
	return nil
}
func (t *countingTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (t *countingTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (t *countingTransport) SetDeadline(time.Time) error      { return nil }
func (t *countingTransport) SetReadDeadline(time.Time) error  { return nil }
func (t *countingTransport) SetWriteDeadline(time.Time) error { return nil }
func (t *countingTransport) LocalPK() cipher.PubKey           { return t.pk1 }
func (t *countingTransport) RemotePK() cipher.PubKey          { return t.pk2 }
func (t *countingTransport) LocalPort() uint16                { return 0 }
func (t *countingTransport) RemotePort() uint16               { return 0 }
func (t *countingTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (t *countingTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (t *countingTransport) Network() types.Type              { return "test" }

// The reciprocal handshake arriving stops the retransmit loop and returns nil,
// even though the initiator had begun retransmitting.
func TestAwaitHandshakeAck_StopsOnProcessed(t *testing.T) {
	processed := make(chan struct{})
	var calls int32
	retransmit := func() { atomic.AddInt32(&calls, 1) }

	go func() {
		time.Sleep(35 * time.Millisecond) // let at least one retransmit tick fire
		close(processed)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := awaitHandshakeAck(ctx, processed, retransmit, 10*time.Millisecond)
	require.NoError(t, err)
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&calls)), 1, "initiator should retransmit while waiting")
}

// A responder passes retransmit == nil: it must simply wait for the forward
// handshake with no retransmission and no panic.
func TestAwaitHandshakeAck_ResponderNoRetransmit(t *testing.T) {
	processed := make(chan struct{})
	go func() {
		time.Sleep(20 * time.Millisecond)
		close(processed)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	require.NoError(t, awaitHandshakeAck(ctx, processed, nil, 0))
}

// When the peer never acks, the wait returns the context error (the caller then
// hard-fails the dial) — but only after retransmitting several times, proving a
// lost handshake would have been given multiple chances to recover.
func TestAwaitHandshakeAck_TimeoutAfterRetransmits(t *testing.T) {
	var calls int32
	retransmit := func() { atomic.AddInt32(&calls, 1) }

	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()
	err := awaitHandshakeAck(ctx, make(chan struct{}), retransmit, 20*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, int(atomic.LoadInt32(&calls)), 2, "handshake must be retransmitted multiple times before failing")
}

// End-to-end at the route-group level: an initiator that never receives the
// reciprocal handshake keeps re-sending the real handshake packet over its
// transport (more than the single legacy shot) until the deadline.
func TestRouteGroup_InitiatorRetransmitsHandshake(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 1)
	t.Cleanup(func() {
		if cerr := rg.Close(); cerr != nil {
			t.Logf("route group close: %v", cerr)
		}
	})
	rg.initiator = true

	conn := newCountingTransport()
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry = transport.Entry{ID: rg.fwd[0].NextTransportID(), Type: "test"}
	rg.mu.Lock()
	rg.tps[0] = mt // swap the leg's transport for a counting one
	rg.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 130*time.Millisecond)
	defer cancel()
	retransmit := func() {
		if herr := rg.sendHandshake(true); herr != nil {
			t.Logf("retransmit handshake: %v", herr)
		}
	}
	err := awaitHandshakeAck(ctx, rg.handshakeProcessed, retransmit, 25*time.Millisecond)
	require.ErrorIs(t, err, context.DeadlineExceeded)
	require.GreaterOrEqual(t, conn.writeCount(), 2, "initiator should retransmit the handshake packet repeatedly")
}

// A duplicate forward handshake at the responder (the initiator retransmitting
// because its reverse ack was lost) must trigger a re-ack: the responder
// re-sends its handshake so the initiator's retransmit loop can complete.
func TestRouteGroup_ResponderReAcksDuplicateHandshake(t *testing.T) {
	rg, _, _ := createMuxRouteGroup(t, 1)
	t.Cleanup(func() {
		if cerr := rg.Close(); cerr != nil {
			t.Logf("route group close: %v", cerr)
		}
	})
	require.False(t, rg.initiator, "createMuxRouteGroup builds a responder")

	conn := newCountingTransport()
	mt := transport.NewManagedTransportForTest(conn)
	mt.Entry = transport.Entry{ID: rg.fwd[0].NextTransportID(), Type: "test"}
	rg.mu.Lock()
	rg.tps[0] = mt
	rid := rg.rvs[0].KeyRouteID()
	rg.mu.Unlock()

	hs := routing.MakeHandshakePacket(rid, true, routing.CapMux|routing.CapSACK)

	// First handshake: processed closes, responder does NOT re-ack from here
	// (its saveRouteGroupRules would send the initial reverse handshake).
	require.NoError(t, rg.handlePacket(hs))
	select {
	case <-rg.handshakeProcessed:
	default:
		t.Fatal("first handshake must close handshakeProcessed")
	}
	require.Equal(t, 0, conn.writeCount(), "first handshake must not itself re-ack")

	// Duplicate handshake: the reverse ack was lost and the initiator is
	// retransmitting — the responder must re-emit its handshake.
	require.NoError(t, rg.handlePacket(hs))
	require.Eventually(t, func() bool { return conn.writeCount() >= 1 }, time.Second, 10*time.Millisecond,
		"duplicate forward handshake must trigger a reverse re-ack")
}
