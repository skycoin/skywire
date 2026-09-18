// Package appserver pkg/app/appserver/rpc_ingress_mux_event_test.go
package appserver

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// muxEventTunnelPort is the local port of the one tunnel muxEventGateway's app
// process has dialed — the only port it owns.
const muxEventTunnelPort routing.Port = 100

// muxEventGateway returns a gateway whose app process has dialed exactly one
// tunnel, from muxEventTunnelPort.
func muxEventGateway(t *testing.T) *RPCIngressGateway {
	localPort := muxEventTunnelPort
	t.Helper()
	appnet.ClearNetworkers()

	dialAddr := prepAddr(appnet.TypeDmsg)
	dialConn := &appcommon.MockConn{}
	dialConn.On("LocalAddr").Return(dmsg.Addr{Port: uint16(localPort)})
	dialConn.On("RemoteAddr").Return(dmsg.Addr{})

	n := &appnet.MockNetworker{}
	var dialErr error
	n.On("DialContext", mock.Anything, dialAddr).Return(dialConn, dialErr)
	require.NoError(t, appnet.AddNetworker(appnet.TypeDmsg, n))
	// A skynet networker that is NOT a *SkywireNetworker: the call degrades
	// quietly past it, so what these tests read is the ingress's own verdict.
	require.NoError(t, appnet.AddNetworker(appnet.TypeSkynet, &appnet.MockNetworker{}))

	rpc := NewRPCGateway(logging.MustGetLogger("rpc_gateway"), nil)
	var resp DialResp
	require.NoError(t, rpc.Dial(&dialAddr, &resp))
	require.Equal(t, localPort, resp.LocalPort)
	return rpc
}

// The router's tunnel-event lookup is by PORT ALONE and spans every app on the
// visor, so the gateway must prove the port is the calling app's own. Without
// this any app process could re-stamp another app's tunnel role and write
// events attributed to the victim's app name.
func TestNoteMuxEvent_RejectsAPortThisAppDoesNotOwn(t *testing.T) {
	const own = muxEventTunnelPort
	rpc := muxEventGateway(t)

	err := rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own + 1,
		Event:     router.MuxEventTunnelPromoted,
		Reason:    "another app's tunnel",
		Role:      "active",
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not this app's")

	require.Error(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: 0,
		Event:     router.MuxEventTunnelRetired,
	}, nil), "port 0 names nothing")

	// Its own port passes the ownership check — it is the networker, not the
	// gateway, that has nothing to record on here.
	require.NoError(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelRetired,
		Reason:    "liveness: no pong and no bytes for 47s",
	}, nil))
}

// Event and Role are stored verbatim in the router's shared 256-entry ring and
// shown in `visor state`, so the vocabulary is fixed rather than app-chosen.
func TestNoteMuxEvent_RejectsUnknownEventAndRole(t *testing.T) {
	const own = muxEventTunnelPort
	rpc := muxEventGateway(t)

	err := rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     "leg_parked",
		Reason:    "not a tunnel event",
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown event")

	require.Error(t, rpc.NoteMuxEvent(&NoteMuxEventReq{LocalPort: own, Event: ""}, nil))

	err = rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelParked,
		Role:      strings.Repeat("a", 4096),
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown role")

	for _, role := range []string{"", tunnelRoleActive, tunnelRoleStandby} {
		require.NoError(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
			LocalPort: own,
			Event:     router.MuxEventTunnelParked,
			Role:      role,
		}, nil))
	}
}

// A Reason is app-supplied text kept for the visor's lifetime in a ring that
// never shrinks; it is truncated at the ingress.
func TestNoteMuxEvent_TruncatesTheReason(t *testing.T) {
	const own = muxEventTunnelPort
	rpc := muxEventGateway(t)

	long := strings.Repeat("r", 64<<10)
	require.Len(t, truncateMuxEventReason(long), maxMuxEventReasonLen)
	require.Equal(t, "short", truncateMuxEventReason("short"))
	require.NoError(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelRetired,
		Reason:    long,
	}, nil))
}

// The snub pair (tunnel_snubbed / tunnel_unsnubbed) was reported by the client
// from the day the snub landed and rejected here from the same day: the
// whitelist listed only promoted/parked/retired, so every snub came back
// "NoteMuxEvent: unknown event" and the one mechanism that re-issues a tunnel's
// whole outstanding chunk set left no trace in the router's ring, in `visor
// state`, or in a bench run's mux_events.
func TestNoteMuxEvent_AcceptsTheSnubPair(t *testing.T) {
	const own = muxEventTunnelPort
	rpc := muxEventGateway(t)

	require.True(t, validTunnelEvent(router.MuxEventTunnelSnubbed))
	require.True(t, validTunnelEvent(router.MuxEventTunnelUnsnubbed))

	// A snub also re-labels the tunnel's role, and "snubbed" rides the same
	// field active/standby do — rejecting the role would have dropped the event
	// just as silently as rejecting the kind.
	require.NoError(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelSnubbed,
		Reason:    "snub: 2 chunk(s) outstanding and no byte or ack for 20.1s (bound 20s)",
		Role:      tunnelRoleSnubbed,
	}, nil))
	require.NoError(t, rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelUnsnubbed,
		Reason:    "snub hold of 10s served; re-trying it with one chunk",
		Role:      tunnelRoleActive,
	}, nil))

	// The vocabulary is still fixed in both fields.
	require.False(t, validTunnelEvent("tunnel_snubbedd"))
	require.False(t, validTunnelRole("wedged"))
	err := rpc.NoteMuxEvent(&NoteMuxEventReq{
		LocalPort: own,
		Event:     router.MuxEventTunnelSnubbed,
		Role:      "wedged",
	}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "unknown role")
}
