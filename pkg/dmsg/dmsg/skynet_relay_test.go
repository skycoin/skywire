package dmsg

import (
	"context"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// skynetDialer stands in for the visor's skywire route: each dial hands the
// far end of a net.Pipe to relay.AcceptRelaySession, exactly as the visor's
// relay listener does with an accepted skynet conn.
func skynetDialer(t *testing.T, relay *Client, relayPK cipher.PubKey, allow func(cipher.PubKey) bool, dials *atomic.Int32) SessionDialer {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		require.Equal(t, CarrierSkynet, network)
		require.Equal(t, SkynetAddr(relayPK, 70), addr)
		dials.Add(1)
		c1, c2 := net.Pipe()
		// The relay session outlives the dial that opened it, as the visor's does.
		go func() { _ = relay.AcceptRelaySession(context.WithoutCancel(ctx), c2, allow) }() //nolint:errcheck
		return c1, nil
	}
}

// entryOnlyDisc resolves entries by key but lists no servers, so the client's
// serve loop can only ever reach the relay it was seeded with — while its
// DialStream fallback can still resolve a destination's delegated server.
type entryOnlyDisc struct{ disc.APIClient }

func (entryOnlyDisc) AvailableServers(context.Context) ([]*disc.Entry, error) { return nil, nil }
func (entryOnlyDisc) AllServers(context.Context) ([]*disc.Entry, error)       { return nil, nil }

// newSkynetDialer makes a client that knows dst's entry but NO dmsg server:
// its only seeded "server" is the relay, at a skynet address.
func newSkynetDialer(t *testing.T, env *relayedTestEnv, name string, extra ...*disc.Entry) (*Client, cipher.PubKey) {
	t.Helper()
	dc := disc.NewMock(0)
	var dialerDisc disc.APIClient = entryOnlyDisc{dc}
	dstEntry, err := env.dc.Entry(context.Background(), env.dstPK)
	require.NoError(t, err)
	require.NoError(t, dc.PostEntry(context.Background(), dstEntry))
	for _, e := range extra {
		require.NoError(t, dc.PostEntry(context.Background(), e))
	}
	relayPK := env.relay.LocalPK()
	pk, sk := GenKeyPair(t, name)
	c := NewClient(pk, sk, dialerDisc, &Config{MinSessions: 1, NoRegister: true})
	c.SetLogger(logging.MustGetLogger(name))
	c.SeedEntryCache(relayPK, &disc.Entry{
		Static: relayPK,
		Server: &disc.Server{Address: SkynetAddr(relayPK, 70), AvailableSessions: 1},
	})
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	return c, pk
}

func acceptOne(t *testing.T, lis *Listener) <-chan *Stream {
	t.Helper()
	ch := make(chan *Stream, 1)
	go func() {
		s, err := lis.AcceptStream()
		if err != nil {
			ch <- nil
			return
		}
		ch <- s
	}()
	return ch
}

func echoCheck(t *testing.T, a, b net.Conn) {
	t.Helper()
	_, err := a.Write([]byte("over the relay"))
	require.NoError(t, err)
	buf := make([]byte, 14)
	require.NoError(t, b.SetReadDeadline(time.Now().Add(5*time.Second)))
	_, err = b.Read(buf)
	require.NoError(t, err)
	require.Equal(t, "over the relay", string(buf))
}

// A client seeded only with a relay at a skynet address forms exactly one
// session — carrier "skynet" — and reaches a destination through the relay's
// own server session under its OWN key, without ever touching a dmsg server.
func TestSkynetRelay_DialerReachesDestinationUnderOwnKey(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()

	var dials atomic.Int32
	var dialerPK cipher.PubKey
	allow := func(pk cipher.PubKey) bool { return pk == dialerPK }
	c, pk := newSkynetDialer(t, env, "skynet-dialer")
	dialerPK = pk
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, allow, &dials))

	require.Eventually(t, func() bool { _, ok := c.Session(relayPK); return ok }, 15*time.Second, 50*time.Millisecond)
	carriers := c.SessionCarriers()
	require.Len(t, carriers, 1)
	require.Equal(t, CarrierSkynet, carriers[0].Carrier)
	require.Equal(t, "skynet", ProtocolLabel(carriers[0].Carrier, carriers[0].Addr))
	require.Eventually(t, func() bool { return len(env.relay.RelaySessions()) == 1 }, 5*time.Second, 50*time.Millisecond)
	require.Equal(t, pk, env.relay.RelaySessions()[0])

	accepted := acceptOne(t, env.dstLis)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	str, err := c.DialStream(ctx, Addr{PK: env.dstPK, Port: relayedDstPort})
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck

	var s *Stream
	select {
	case s = <-accepted:
		require.NotNil(t, s)
	case <-time.After(10 * time.Second):
		t.Fatal("dst never accepted the relayed stream")
	}
	defer s.Close() //nolint:errcheck
	require.Equal(t, pk, s.RawRemoteAddr().PK, "the destination must see the dialer's key")
	echoCheck(t, str, s)
	echoCheck(t, s, str)

	require.Len(t, c.AllSessions(), 1, "the dialer must hold no session but the relay")
	_, onServer := env.srv.session(pk)
	require.False(t, onServer, "the dialer must never appear on the dmsg server")
	require.Equal(t, 1, env.relay.RelayedStreams())
	require.Equal(t, int32(1), dials.Load())
}

// Two peers attached to the same relay reach each other through it, bridged
// locally without any server involved.
func TestSkynetRelay_AttachedPeersBridgeLocally(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()
	var dials atomic.Int32

	a, aPK := newSkynetDialer(t, env, "skynet-a")
	a.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	b, bPK := newSkynetDialer(t, env, "skynet-b")
	b.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	require.Eventually(t, func() bool { return len(env.relay.RelaySessions()) == 2 }, 15*time.Second, 50*time.Millisecond)

	bLis, err := b.Listen(81)
	require.NoError(t, err)
	defer bLis.Close() //nolint:errcheck
	accepted := acceptOne(t, bLis)

	// a has no entry for b anywhere; seed one whose delegated servers are
	// irrelevant — the relay resolves b as an attached peer before forwarding.
	a.SeedEntryCache(bPK, disc.NewClientEntry(bPK, 0, []cipher.PubKey{relayPK}))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	str, err := a.DialStream(ctx, Addr{PK: bPK, Port: 81})
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck
	s := <-accepted
	require.NotNil(t, s)
	defer s.Close() //nolint:errcheck
	require.Equal(t, aPK, s.RawRemoteAddr().PK)
	echoCheck(t, str, s)
}

// A relay that refuses to carry a stream (no relay slots) closes it at once,
// and the dialer falls back to a normal session with a dmsg server it CAN
// resolve — well inside the handshake timeout it used to sit out.
func TestSkynetRelay_RefusedFallsBackToServerSession(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	require.Equal(t, 0, env.relay.maxRelayedStreams, "an unconfigured client must not relay")
	relayPK := env.relay.LocalPK()
	var dials atomic.Int32

	srvEntry, err := env.dc.Entry(context.Background(), env.srvPK)
	require.NoError(t, err)
	c, pk := newSkynetDialer(t, env, "skynet-fallback", srvEntry)
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	require.Eventually(t, func() bool { _, ok := c.Session(relayPK); return ok }, 15*time.Second, 50*time.Millisecond)

	accepted := acceptOne(t, env.dstLis)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	start := time.Now()
	str, err := c.DialStream(ctx, Addr{PK: env.dstPK, Port: relayedDstPort})
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck
	require.Less(t, time.Since(start), HandshakeTimeout, "a refused relay must fail fast, not time out")
	s := <-accepted
	require.NotNil(t, s)
	defer s.Close() //nolint:errcheck
	require.Equal(t, pk, s.RawRemoteAddr().PK)
	_, direct := c.Session(env.srvPK)
	require.True(t, direct, "the fallback is a normal session to the destination's server")
	echoCheck(t, str, s)
}

// The relay's allow callback is the admission control: a refused peer gets no
// session, and the dialer keeps failing rather than reaching anything.
func TestSkynetRelay_UnlistedPeerRefused(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()
	var dials atomic.Int32

	c, _ := newSkynetDialer(t, env, "skynet-unlisted")
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, func(cipher.PubKey) bool { return false }, &dials))
	require.Eventually(t, func() bool { return dials.Load() >= 1 }, 15*time.Second, 50*time.Millisecond)
	// The dialer completes its half of the handshake before the relay refuses,
	// so its session may exist for an instant; it must not survive.
	require.Eventually(t, func() bool {
		_, ok := c.Session(relayPK)
		return !ok && len(env.relay.RelaySessions()) == 0
	}, 5*time.Second, 50*time.Millisecond)
	time.Sleep(300 * time.Millisecond)
	_, ok := c.Session(relayPK)
	require.False(t, ok)
}

func TestSkynetAddrRoundTrip(t *testing.T) {
	pk, _ := GenKeyPair(t, "skynet-addr")
	addr := SkynetAddr(pk, 70)
	gotPK, port, err := ParseSkynetAddr(addr)
	require.NoError(t, err)
	require.Equal(t, pk, gotPK)
	require.Equal(t, uint16(70), port)
	_, _, err = ParseSkynetAddr("127.0.0.1:8080")
	require.Error(t, err)
	require.Equal(t, CarrierSkynet, func() string { n, _ := pickCarrier(nil, &disc.Entry{Server: &disc.Server{Address: addr}}); return n }())
}
