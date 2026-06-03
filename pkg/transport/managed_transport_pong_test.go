package transport

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// okTransport is a mock network.Transport whose Write always succeeds (so
// sendTransportPing counts a ping without erroring) and whose Read blocks —
// modeling a half-open link where the local socket still looks alive.
type okTransport struct{ pk1, pk2 cipher.PubKey }

func (o *okTransport) Write(p []byte) (int, error)      { return len(p), nil }
func (o *okTransport) Read([]byte) (int, error)         { select {} }
func (o *okTransport) Close() error                     { return nil }
func (o *okTransport) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (o *okTransport) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (o *okTransport) SetDeadline(time.Time) error      { return nil }
func (o *okTransport) SetReadDeadline(time.Time) error  { return nil }
func (o *okTransport) SetWriteDeadline(time.Time) error { return nil }
func (o *okTransport) LocalPK() cipher.PubKey           { return o.pk1 }
func (o *okTransport) RemotePK() cipher.PubKey          { return o.pk2 }
func (o *okTransport) LocalPort() uint16                { return 0 }
func (o *okTransport) RemotePort() uint16               { return 0 }
func (o *okTransport) LocalRawAddr() net.Addr           { return &net.TCPAddr{} }
func (o *okTransport) RemoteRawAddr() net.Addr          { return &net.TCPAddr{} }
func (o *okTransport) Network() types.Type              { return "test" }

func testPong() routing.Packet { return routing.MakeTransportPongPacket(time.Now().UnixNano()) }

// TestManagedTransport_HalfOpenClosesAfterMissedPongs: a transport that has
// answered a ping (armed) but then goes silent must be closed once
// pongMissThreshold pings go unanswered — the fix that drains dead edges from
// TPD (a closed transport is skipped by reRegisterTransports + deregistered).
func TestManagedTransport_HalfOpenClosesAfterMissedPongs(t *testing.T) {
	mt := NewManagedTransportForTest(&okTransport{})
	mt.handleTransportPong(testPong()) // arm: pongSeen=true, missedPongs=0
	require.False(t, mt.IsClosed())

	closed := false
	for i := 0; i < pongMissThreshold+3; i++ {
		mt.tickPing()
		if mt.IsClosed() {
			closed = true
			break
		}
	}
	assert.True(t, closed, "armed transport with no pongs must close after pongMissThreshold pings")
}

// TestManagedTransport_NeverPongedNeverCloses: a peer that never answers a
// transport ping (e.g. an older visor without ping/pong support) must NOT be
// reaped — the detector is armed only by a first pong (pongSeen).
func TestManagedTransport_NeverPongedNeverCloses(t *testing.T) {
	mt := NewManagedTransportForTest(&okTransport{})
	for i := 0; i < pongMissThreshold+10; i++ {
		mt.tickPing()
	}
	assert.False(t, mt.IsClosed(), "a peer that never pongs (old visor) must NOT be reaped")
}

// TestManagedTransport_HealthyPeerStaysOpen: a peer that keeps answering pings
// resets the miss counter each cycle and is never closed.
func TestManagedTransport_HealthyPeerStaysOpen(t *testing.T) {
	mt := NewManagedTransportForTest(&okTransport{})
	for i := 0; i < pongMissThreshold+10; i++ {
		mt.tickPing()
		mt.handleTransportPong(testPong())
	}
	assert.False(t, mt.IsClosed(), "a peer that keeps answering pings must stay open")
}
