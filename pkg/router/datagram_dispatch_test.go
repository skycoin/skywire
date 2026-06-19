// Package router pkg/router/datagram_dispatch_test.go: integration
// tests for the router-side DatagramPacket dispatch wired in stage 4
// of #2607. Unlike datagram_route_group_test.go (which calls Handle
// directly), these drive a real DatagramPacket through
// handleTransportPacket → GetRule → datagramRouteGroup → Handle, i.e.
// the path a frame off a transport actually takes.
package router

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// newDispatchTestRouter builds the minimal router needed to exercise
// the consume-side datagram dispatch: a routing table and the
// datagram route-group map. The forward/intermediary path needs a
// transport manager and is covered separately.
func newDispatchTestRouter() *router {
	return &router{
		logger:         logging.MustGetLogger("test_dg_dispatch"),
		rt:             routing.NewTable(logging.MustGetLogger("test_dg_rt")),
		rgsDatagrams:   make(map[routing.RouteDescriptor]*DatagramRouteGroup),
		datagramPorts:  make(map[routing.Port]struct{}),
		acceptDatagram: make(chan datagramAccept, acceptDatagramBuf),
		done:           make(chan struct{}),
	}
}

// TestDatagramDispatchDeliversToRegisteredGroup verifies the full
// receive-side wiring: a DatagramPacket whose route ID maps to a
// local consume rule is delivered to the DatagramRouteGroup
// registered for that rule's descriptor.
func TestDatagramDispatchDeliversToRegisteredGroup(t *testing.T) {
	r := newDispatchTestRouter()

	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	keyRouteID := routing.RouteID(42)

	// Install a consume (reverse) rule keyed by keyRouteID. Its
	// RouteDescriptor() is what the datagram group is registered under.
	consume := routing.ConsumeRule(time.Hour, keyRouteID, lPK, rPK, 100, 200)
	require.NoError(t, r.rt.SaveRule(consume))
	desc := consume.RouteDescriptor()

	dg := NewDatagramRouteGroup(nil, nil, desc, nil)
	defer dg.Close() //nolint:errcheck
	r.setDatagramRouteGroup(desc, dg)

	// A DatagramPacket stamped with keyRouteID enters via the same
	// switch a transport read loop uses.
	payload := []byte("faithful-udp-payload")
	pkt, err := routing.MakeDatagramPacket(keyRouteID, payload)
	require.NoError(t, err)
	require.NoError(t, r.handleTransportPacket(context.Background(), pkt))

	// The group received it.
	require.NoError(t, dg.SetReadDeadline(time.Now().Add(2*time.Second)))
	buf := make([]byte, 1500)
	n, addr, err := dg.ReadFrom(buf)
	require.NoError(t, err)
	assert.Equal(t, payload, buf[:n])
	assert.Equal(t, dg.RemoteAddr().String(), addr.String())
}

// TestDatagramDispatchDropsWhenNoGroup verifies that a DatagramPacket
// for a known consume rule with NO registered group is dropped
// silently (faithful-UDP loss tolerance) rather than erroring or
// parking — the receive-side setup race must not buffer datagrams.
func TestDatagramDispatchDropsWhenNoGroup(t *testing.T) {
	r := newDispatchTestRouter()

	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	keyRouteID := routing.RouteID(7)
	consume := routing.ConsumeRule(time.Hour, keyRouteID, lPK, rPK, 1, 2)
	require.NoError(t, r.rt.SaveRule(consume))

	pkt, err := routing.MakeDatagramPacket(keyRouteID, []byte("dropped"))
	require.NoError(t, err)
	// No group registered → handled (dropped) without error.
	require.NoError(t, r.handleTransportPacket(context.Background(), pkt))
}

// TestDatagramDispatchUnknownRouteErrors verifies that a
// DatagramPacket for a route ID with no rule at all surfaces the
// rule-lookup error (annotated with route ID + packet type), matching
// the reliable dispatch's behavior.
func TestDatagramDispatchUnknownRouteErrors(t *testing.T) {
	r := newDispatchTestRouter()
	pkt, err := routing.MakeDatagramPacket(routing.RouteID(999), []byte("x"))
	require.NoError(t, err)
	err = r.handleTransportPacket(context.Background(), pkt)
	require.Error(t, err)
}

// TestDatagramDispatchClosedGroupDeregisters verifies that delivering
// to a closed group de-registers it (so subsequent datagrams take the
// fast drop path) and does not surface an error.
func TestDatagramDispatchClosedGroupDeregisters(t *testing.T) {
	r := newDispatchTestRouter()

	lPK, _ := cipher.GenerateKeyPair()
	rPK, _ := cipher.GenerateKeyPair()
	keyRouteID := routing.RouteID(11)
	consume := routing.ConsumeRule(time.Hour, keyRouteID, lPK, rPK, 5, 6)
	require.NoError(t, r.rt.SaveRule(consume))
	desc := consume.RouteDescriptor()

	dg := NewDatagramRouteGroup(nil, nil, desc, nil)
	require.NoError(t, dg.Close())
	r.setDatagramRouteGroup(desc, dg)

	pkt, err := routing.MakeDatagramPacket(keyRouteID, []byte("to-closed"))
	require.NoError(t, err)
	require.NoError(t, r.handleTransportPacket(context.Background(), pkt))

	// The closed group was de-registered by the dispatch.
	_, ok := r.datagramRouteGroup(desc)
	assert.False(t, ok, "closed datagram group should be de-registered after dispatch")
}
