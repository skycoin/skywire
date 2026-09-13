// Package transport pkg/transport/vstream_streaminfo_test.go c2-net-transport
package transport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// TestVStreamMux_StreamInfoAttributesTheApp is the regression for a working
// proxy reporting no route at all.
//
// A dial eligible for the AppDirectMux shortcut builds NO route group, so every
// surface that describes "the route" (proxy status, proxy tree, the
// status.skysocks page) had nothing to render and said "(no active route
// group)" — on a session that was passing traffic to the right exit. The path
// was always well defined; the app name that makes it reportable was dropped at
// the Dial → DialOnTransport boundary.
func TestVStreamMux_StreamInfoAttributesTheApp(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-info-test"))

	s, err := mux.DialOnTransportAs(mt, "skysocks-client")
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	infos := mux.StreamInfo("")
	require.Len(t, infos, 1)
	require.Equal(t, "skysocks-client", infos[0].AppName, "the dial's app name must survive to the report")
	require.Equal(t, mt.Remote(), infos[0].RemotePK)
	require.Equal(t, mt.Entry.ID, infos[0].TpID, "the report must name the transport actually carrying it")

	// The per-app filter is what `proxy status` asks with.
	require.Len(t, mux.StreamInfo("skysocks-client"), 1)
	require.Empty(t, mux.StreamInfo("vpn-client"), "another app's streams must not be attributed here")
}

// Counters move with traffic, so the status surfaces can show a direct session
// carrying bytes rather than an unannotated peer.
func TestVStreamMux_StreamInfoCountsBytes(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-info-test"))

	s, err := mux.DialOnTransportAs(mt, "skysocks-client")
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	require.Zero(t, mux.StreamInfo("")[0].SentBytes)

	n, err := s.Write([]byte("hello over the direct path"))
	require.NoError(t, err)
	require.Equal(t, 26, n)
	require.Equal(t, uint64(26), mux.StreamInfo("")[0].SentBytes)

	mux.deliver(s, []byte("reply"))
	require.Equal(t, uint64(5), mux.StreamInfo("")[0].RecvBytes)
}

// An internal (non-app) stream — setup-RPC, ping-tree — stays unattributed, so
// the per-app filter never claims it belongs to a proxy.
func TestVStreamMux_StreamInfoLeavesInternalStreamsUnattributed(t *testing.T) {
	tm := newTestManager(t)
	mt, _ := servingTransport(t, tm, mustPK(t), types.STCPR)
	mux := NewVStreamMux(tm, routing.DHTPacket, logging.MustGetLogger("vstream-info-test"))

	s, err := mux.DialOnTransport(mt)
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	infos := mux.StreamInfo("")
	require.Len(t, infos, 1)
	require.Empty(t, infos[0].AppName)
	require.Empty(t, mux.StreamInfo("skysocks-client"))
}

// TestVStreamMux_DialPicksThePreferredTransport: with several transports to a
// peer, the direct path must take the most preferred one.
//
// Dial used to stop at the first transport WalkTransports yielded, so which one
// carried every direct session was decided by map iteration order. Observed
// live: a visor holding both stcpr and webrtc to an exit ran its proxy over
// webrtc — seventh in the default preference order, and much the worse of the
// two — while the stcpr sat idle.
func TestVStreamMux_DialPicksThePreferredTransport(t *testing.T) {
	tm := newTestManager(t)
	remote := mustPK(t)

	// Same peer, two carriers. webrtc is created first so a first-match walk
	// would be likely to take it.
	_, _ = servingTransport(t, tm, remote, types.WEBRTC)
	_, _ = servingTransport(t, tm, remote, types.STCPR)

	mux := NewVStreamMux(tm, routing.AppDirectPacket, logging.MustGetLogger("vstream-pref-test"))
	s, err := mux.Dial(remote, "skysocks-client")
	require.NoError(t, err)
	defer s.Close() //nolint:errcheck

	info := mux.StreamInfo("")
	require.Len(t, info, 1)

	tp, err := tm.GetTransportByID(info[0].TpID)
	require.NoError(t, err)
	require.Equal(t, types.STCPR, tp.Type(),
		"stcpr outranks webrtc in the preference order; the direct dial must take it")
}
