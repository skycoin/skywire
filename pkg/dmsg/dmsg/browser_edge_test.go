// Package dmsg pkg/dmsg/dmsg/browser_edge_test.go c1-net-dmsg
//
// Headless "browser edge" regression suite (Tier A of the wasm-visor test
// plan): a wss-only dmsg client — the browser/wasm-visor profile
// (Carriers=[ws], no raw TCP) — exercised against real in-process dmsg
// servers, NO browser involved. Locks the on-demand rendezvous model the wasm
// visor depends on:
//
//   - a peer is reached by opening an on-demand session to one of THAT peer's
//     delegated servers over a carrier the client can actually dial;
//   - a server the client CANNOT dial (tcp-only) fails cleanly and the dial
//     moves on to the peer's next server — never a futile raw-TCP fallback
//     (the #3634 regression: gating every TCP/QUIC fallback on hasCarrier);
//   - ConnectToAllServers reports per-server results with capability-honest
//     errors (the #3632 "connect to all servers" surface).
//
// The capability invariant asserted throughout: a Carriers=[ws] client NEVER
// holds a session whose carrier is anything but ws — even when a server
// advertises a perfectly reachable TCP endpoint (in these tests the TCP
// listeners are real and dialable, which is exactly what made the old
// Address!="" fallback a capability violation rather than a dead dial).
package dmsg

import (
	"context"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// testServer is one in-process dmsg server plus its discovery entry.
type testServer struct {
	pk    cipher.PubKey
	srv   *Server
	entry *disc.Entry
}

// startTestServer stands up a dmsg server on a loopback listener and posts its
// discovery entry. withWS serves the unified TCP+WS single port (ServeWithWS)
// and advertises AddressWS; otherwise it is a plain TCP-only server (no
// AddressWS in the entry — undialable for a wss-only client BY CONTRACT, even
// though the TCP listener itself is live and reachable).
func startTestServer(t *testing.T, dc disc.APIClient, name string, withWS bool) *testServer {
	t.Helper()
	const maxSessions = 10

	pk, sk := GenKeyPair(t, name)
	srv := NewServer(pk, sk, dc, &ServerConfig{MaxSessions: maxSessions, UpdateInterval: 0}, nil)
	srv.SetLogger(logging.MustGetLogger(name))

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	tcpAddr := lis.Addr().String()

	entry := disc.NewServerEntry(pk, 0, tcpAddr, maxSessions)
	if withWS {
		entry.Server.AddressWS = "ws://" + tcpAddr + wsPath
	}
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, dc.PostEntry(context.Background(), entry))

	go func() {
		if withWS {
			_ = srv.ServeWithWS(lis, tcpAddr, entry.Server.AddressWS) //nolint:errcheck
		} else {
			_ = srv.Serve(lis, tcpAddr) //nolint:errcheck
		}
	}()
	t.Cleanup(func() { _ = srv.Close() }) //nolint:errcheck

	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		t.Fatalf("server %s never became ready", name)
	}
	return &testServer{pk: pk, srv: srv, entry: entry}
}

// newPinnedClient builds a client with the given carriers, pre-establishes
// sessions to exactly the given servers (EnsureSession before Serve, so the
// maintenance loop sees MinSessions satisfied and dials nothing else), then
// runs Serve so the client publishes its entry (delegated servers = pinned
// set) and accepts streams.
func newPinnedClient(t *testing.T, dc disc.APIClient, name string, carriers []string, pin ...*testServer) *Client {
	t.Helper()
	conf := DefaultConfig()
	conf.MinSessions = 1
	conf.Carriers = carriers
	pk, sk := GenKeyPair(t, name)
	c := NewClient(pk, sk, dc, conf)
	c.SetLogger(logging.MustGetLogger(name))
	for _, s := range pin {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		err := c.EnsureSession(ctx, s.entry)
		cancel()
		require.NoErrorf(t, err, "%s: EnsureSession to pinned server", name)
	}
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	return c
}

// requireCarriersOnly asserts the capability invariant: every session the
// client holds uses one of the allowed carriers.
func requireCarriersOnly(t *testing.T, c *Client, allowed ...string) {
	t.Helper()
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	for _, s := range c.AllSessions() {
		require.Truef(t, ok[s.Carrier()],
			"capability violation: session to %s uses carrier %q (allowed: %v)",
			s.RemotePK(), s.Carrier(), allowed)
	}
}

// dialEventually dials A→B:port, retrying while B's entry propagates.
func dialEventually(t *testing.T, a *Client, b cipher.PubKey, port uint16) *Stream {
	t.Helper()
	var st *Stream
	require.Eventually(t, func() bool {
		var err error
		st, err = a.DialStream(context.TODO(), Addr{PK: b, Port: port})
		return err == nil
	}, 20*time.Second, 250*time.Millisecond, "DialStream never succeeded")
	return st
}

// echoOnce round-trips a payload A→B over a fresh stream.
func echoOnce(t *testing.T, st *Stream, lis *Listener) {
	t.Helper()
	defer st.Close() //nolint:errcheck
	conn, err := lis.Accept()
	require.NoError(t, err)
	defer conn.Close() //nolint:errcheck
	payload := cipher.RandByte(1024)
	go func() { _, _ = st.Write(payload) }() //nolint:errcheck
	got := make([]byte, len(payload))
	_, err = io.ReadFull(conn, got)
	require.NoError(t, err)
	require.Equal(t, payload, got)
}

// TestBrowserEdge_RendezvousOverWS: a wss-only client bootstrapped to server
// S1 reaches a peer delegated ONLY to server S2 by opening an on-demand WS
// session to S2 — the exact reachability model a browser visor lives on (it
// never "connects to all servers"; it rendezvouses per peer).
func TestBrowserEdge_RendezvousOverWS(t *testing.T) {
	dc := disc.NewMock(0)
	s1 := startTestServer(t, dc, "srv1-ws", true)
	s2 := startTestServer(t, dc, "srv2-ws", true)

	peer := newPinnedClient(t, dc, "peer-native", nil, s2) // native carriers, on S2 only
	edge := newPinnedClient(t, dc, "edge-wss", []string{CarrierWS}, s1)

	const port = 8080
	lis, err := peer.Listen(port)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	st := dialEventually(t, edge, peer.LocalPK(), port)
	echoOnce(t, st, lis)

	// The dial must have opened an on-demand session to S2 — over WS.
	_, onS2 := edge.Session(s2.pk)
	require.True(t, onS2, "edge should hold an on-demand session to the peer's delegated server")
	requireCarriersOnly(t, edge, CarrierWS)
	_ = s1
}

// TestBrowserEdge_NeverFallsBackToTCP (regression #3634): the peer's FIRST
// delegated server is tcp-only (live, dialable TCP — but no wss). The
// wss-only edge must fail that server cleanly and rendezvous via the peer's
// second (unified) server, ending with ws-only sessions. Before the fix, the
// carrier fallback dialed the tcp-only server's real TCP endpoint and
// SUCCEEDED — a capability violation that in a real browser turned into a
// futile `dial tcp` dead-end (dmsg error 202).
func TestBrowserEdge_NeverFallsBackToTCP(t *testing.T) {
	dc := disc.NewMock(0)
	sTCP := startTestServer(t, dc, "srv-tcponly", false)
	sWS := startTestServer(t, dc, "srv-unified", true)
	sBoot := startTestServer(t, dc, "srv-boot", true)

	peer := newPinnedClient(t, dc, "peer-native2", nil, sTCP, sWS)
	edge := newPinnedClient(t, dc, "edge-wss2", []string{CarrierWS}, sBoot)

	const port = 8081
	lis, err := peer.Listen(port)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	// Pin the peer's delegated-server ORDER: tcp-only first, so the edge's
	// rendezvous deterministically attempts the undialable server before the
	// dialable one. Wait only for the peer's entry to EXIST (the client's
	// initial publish may list just the first pinned session; its refresh
	// interval is long), then overwrite it with the explicit two-server order —
	// the manual entry is the source of truth for the dial under test.
	require.Eventually(t, func() bool {
		e, err := dc.Entry(context.Background(), peer.LocalPK())
		return err == nil && e.Client != nil && len(e.Client.DelegatedServers) >= 1
	}, 15*time.Second, 200*time.Millisecond, "peer entry never published")
	e, err := dc.Entry(context.Background(), peer.LocalPK())
	require.NoError(t, err)
	e.Client.DelegatedServers = []cipher.PubKey{sTCP.pk, sWS.pk}
	e.Sequence++
	require.NoError(t, e.Sign(peer.LocalSK()))
	require.NoError(t, dc.PutEntry(context.Background(), peer.LocalSK(), e))

	st := dialEventually(t, edge, peer.LocalPK(), port)
	echoOnce(t, st, lis)

	// The invariant that failed before #3634: no session on any carrier but ws,
	// and in particular NO session to the tcp-only server.
	requireCarriersOnly(t, edge, CarrierWS)
	_, onTCPOnly := edge.Session(sTCP.pk)
	require.False(t, onTCPOnly, "wss-only edge must never hold a session to a tcp-only server")
	_, onWS := edge.Session(sWS.pk)
	require.True(t, onWS, "edge should have rendezvoused via the unified (ws) server")
}

// TestBrowserEdge_TCPOnlyPeerUnreachableCleanly: a peer delegated ONLY to a
// tcp-only server is honestly unreachable for a wss-only edge — the dial
// errors (rather than sneaking a TCP session) and the edge's session set stays
// ws-only. This is the "surface a clear error so deployment problems are
// visible" half of the fix: masking it as TCP kept 3 broken wss fronts hidden
// in production.
func TestBrowserEdge_TCPOnlyPeerUnreachableCleanly(t *testing.T) {
	dc := disc.NewMock(0)
	sTCP := startTestServer(t, dc, "srv-tcponly2", false)
	sBoot := startTestServer(t, dc, "srv-boot2", true)

	peer := newPinnedClient(t, dc, "peer-native3", nil, sTCP)
	edge := newPinnedClient(t, dc, "edge-wss3", []string{CarrierWS}, sBoot)

	const port = 8082
	lis, err := peer.Listen(port)
	require.NoError(t, err)
	defer lis.Close() //nolint:errcheck

	// Wait for the peer's entry (delegated=[sTCP]) so the dial fails on the
	// carrier contract, not on entry propagation.
	require.Eventually(t, func() bool {
		e, eerr := dc.Entry(context.Background(), peer.LocalPK())
		return eerr == nil && e.Client != nil && len(e.Client.DelegatedServers) == 1
	}, 15*time.Second, 200*time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, err = edge.DialStream(ctx, Addr{PK: peer.LocalPK(), Port: port})
	require.Error(t, err, "wss-only edge must NOT reach a peer that is only on a tcp-only server")
	requireCarriersOnly(t, edge, CarrierWS)
	_, onTCPOnly := edge.Session(sTCP.pk)
	require.False(t, onTCPOnly)
}

// TestBrowserEdge_ConnectToAllServers (#3632 surface): the one-shot
// maximize-connectivity action honors the carrier contract per server — ws
// servers connect over ws, the tcp-only server FAILS with a clear error
// instead of silently connecting over a carrier the client doesn't have.
func TestBrowserEdge_ConnectToAllServers(t *testing.T) {
	dc := disc.NewMock(0)
	s1 := startTestServer(t, dc, "ca-srv1-ws", true)
	s2 := startTestServer(t, dc, "ca-srv2-ws", true)
	s3 := startTestServer(t, dc, "ca-srv3-tcponly", false)

	edge := newPinnedClient(t, dc, "edge-wss4", []string{CarrierWS}, s1)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	res, err := edge.ConnectToAllServers(ctx)
	require.NoError(t, err)

	require.Equal(t, 3, res.Total, "all discovery servers should be enumerated")
	require.Equal(t, 1, res.AlreadyConnected)
	require.Equal(t, 1, res.NewlyConnected, "only the second ws server is newly connectable")
	require.Len(t, res.Failed, 1)
	require.Contains(t, res.Failed, s3.pk, "the tcp-only server must FAIL for a wss-only client, not silently connect over tcp")

	_, onS2 := edge.Session(s2.pk)
	require.True(t, onS2)
	requireCarriersOnly(t, edge, CarrierWS)
}
