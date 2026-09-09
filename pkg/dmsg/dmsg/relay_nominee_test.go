package dmsg

import (
	"context"
	"fmt"
	"net"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// A client that already holds its MinSessions server sessions, then has a
// relay nominated at runtime, attaches to the relay without being told about
// any server address, keeps its server sessions, and dials destinations
// through the relay first.
func TestRelayNominee_AttachesAtRuntimeAndIsPreferred(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()

	// An ordinary client on the shared discovery: one server session.
	pk, sk := GenKeyPair(t, "nominee-client")
	c := NewClient(pk, sk, env.dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("nominee-client"))
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { _, ok := c.Session(env.srvPK); return ok }, 15*time.Second, 50*time.Millisecond)
	require.False(t, c.hasRelaySession())

	// Nominate the relay: the serve loop, parked at MinSessions, must wake and dial it.
	c.SetRelayPeers([]cipher.PubKey{relayPK}, 70)
	require.Eventually(t, c.hasRelaySession, 15*time.Second, 50*time.Millisecond)
	_, direct := c.Session(env.srvPK)
	require.True(t, direct, "the server session stays; the relay is added, not swapped in")
	require.Equal(t, []cipher.PubKey{relayPK}, c.RelayPeers())

	// A stream to dst goes through the relay (phase 0), not the shared server.
	accepted := acceptOne(t, env.dstLis)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	str, err := c.DialStream(ctx, Addr{PK: env.dstPK, Port: relayedDstPort})
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck
	s := <-accepted
	require.NotNil(t, s)
	defer s.Close() //nolint:errcheck
	require.Equal(t, pk, s.RawRemoteAddr().PK)
	require.Equal(t, 1, env.relay.RelayedStreams(), "the stream must be carried by the relay")
	echoCheck(t, str, s)

	// The idle reaper never takes the relay session: with MinSessions 1 and two
	// sessions, the streamless SERVER session is the surplus.
	streak := map[cipher.PubKey]int{}
	c.reapExcessIdleSessions(streak, 1)
	require.Eventually(t, func() bool { _, ok := c.Session(env.srvPK); return !ok }, 5*time.Second, 50*time.Millisecond)
	require.True(t, c.hasRelaySession())
}

// A nominee that refuses is backed off: the client does not keep re-dialing it,
// and stays satisfied with its server sessions.
func TestRelayNominee_RefusalBacksOff(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	relayPK := env.relay.LocalPK()

	pk, sk := GenKeyPair(t, "nominee-refused")
	c := NewClient(pk, sk, env.dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("nominee-refused"))
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, func(cipher.PubKey) bool { return false }, &dials))
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { _, ok := c.Session(env.srvPK); return ok }, 15*time.Second, 50*time.Millisecond)

	c.SetRelayPeers([]cipher.PubKey{relayPK}, 70)
	require.Eventually(t, func() bool { return dials.Load() >= 1 }, 15*time.Second, 50*time.Millisecond)
	require.Eventually(t, func() bool { return len(c.relayEntries()) == 0 }, 5*time.Second, 50*time.Millisecond, "a refused nominee must be backed off")
	require.True(t, c.sessionsSatisfied(), "with the nominee backed off the server session suffices")
	time.Sleep(time.Second)
	require.LessOrEqual(t, dials.Load(), int32(2), "no redial storm against a refusing nominee")
	require.False(t, c.hasRelaySession())

	// Withdrawing the nomination clears the candidate list entirely.
	c.SetRelayPeers(nil, 70)
	require.Empty(t, c.RelayPeers())
}

func TestRelayNominee_EntriesSkipSelfAndSessions(t *testing.T) {
	pk, sk := GenKeyPair(t, "nominee-entries")
	c := NewClient(pk, sk, disc.NewMock(0), &Config{MinSessions: 1})
	other, _ := GenKeyPair(t, "nominee-other")
	require.True(t, c.SetRelayPeers([]cipher.PubKey{pk, other}, 70))
	require.False(t, c.SetRelayPeers([]cipher.PubKey{other}, 70), "same nominees: no change")
	require.Equal(t, []cipher.PubKey{other}, c.RelayPeers(), "a client never nominates itself")
	entries := c.relayEntries()
	require.Len(t, entries, 1)
	require.Equal(t, SkynetAddr(other, 70), entries[0].Server.Address)
	c.noteRelayFailure(other, relayFailureBackoff)
	require.Empty(t, c.relayEntries())
}

// addServer registers one more dmsg server on the env's discovery.
func addServer(t *testing.T, dc disc.APIClient, name string) cipher.PubKey {
	t.Helper()
	pk, sk := GenKeyPair(t, name)
	srv := NewServer(pk, sk, dc, nil, nil)
	srv.SetLogger(logging.MustGetLogger(name))
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	entry := disc.NewServerEntry(pk, 0, addr, 10)
	require.NoError(t, entry.Sign(sk))
	require.NoError(t, dc.PostEntry(context.Background(), entry))
	go func() { _ = srv.Serve(lis, addr) }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })    //nolint:errcheck
	return pk
}

// A nomination must not fan the client out across the remaining servers. The
// serve loop parks inside its per-server walk once MinSessions is met; woken
// by a nomination it has to rebuild the candidate list with the relay in front
// rather than carry on down the server list (where sessionsSatisfied stays
// false until a relay session exists, so every server would be dialed).
func TestRelayNominee_DoesNotFanOutAcrossServers(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	relayPK := env.relay.LocalPK()
	for i := 0; i < 3; i++ {
		addServer(t, env.dc, fmt.Sprintf("fanout-srv-%d", i))
	}

	pk, sk := GenKeyPair(t, "nominee-fanout")
	c := NewClient(pk, sk, env.dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("nominee-fanout"))
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { return c.SessionCount() == 1 }, 15*time.Second, 50*time.Millisecond)
	// Let the walk park on the second candidate.
	time.Sleep(300 * time.Millisecond)
	require.Equal(t, 1, c.SessionCount())

	c.SetRelayPeers([]cipher.PubKey{relayPK}, 70)
	require.Eventually(t, c.hasRelaySession, 15*time.Second, 50*time.Millisecond)
	time.Sleep(time.Second)
	require.Equal(t, 2, c.SessionCount(), "one server session plus the relay; no fan-out to the other servers")
	require.Equal(t, int32(1), dials.Load())
}

// hideEntryDisc hides one client entry from the dialer, the way the deployment
// services (which register no client entry) look to every client.
type hideEntryDisc struct {
	disc.APIClient
	hidden cipher.PubKey
}

func (d hideEntryDisc) Entry(ctx context.Context, pk cipher.PubKey) (*disc.Entry, error) {
	if pk == d.hidden {
		return nil, disc.ErrKeyNotFound
	}
	return d.APIClient.Entry(ctx, pk)
}

// With a relay attached, every stream goes through it first: a destination
// without a discovery entry (which otherwise takes the connected-servers
// fallback) and a destination with a cached server route alike.
func TestRelayNominee_RelayGoesBeforeLookupAndCache(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()

	pk, sk := GenKeyPair(t, "nominee-first")
	c := NewClient(pk, sk, hideEntryDisc{APIClient: env.dc, hidden: env.dstPK}, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("nominee-first"))
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { _, ok := c.Session(env.srvPK); return ok }, 15*time.Second, 50*time.Millisecond)
	c.SetRelayPeers([]cipher.PubKey{relayPK}, 70)
	require.Eventually(t, c.hasRelaySession, 15*time.Second, 50*time.Millisecond)

	dialThroughRelay := func() {
		accepted := acceptOne(t, env.dstLis)
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		str, err := c.DialStream(ctx, Addr{PK: env.dstPK, Port: relayedDstPort})
		require.NoError(t, err)
		defer str.Close() //nolint:errcheck
		s := <-accepted
		require.NotNil(t, s)
		defer s.Close() //nolint:errcheck
		require.Equal(t, 1, env.relay.RelayedStreams(), "the stream must be carried by the relay")
		echoCheck(t, str, s)
	}
	// No entry for dst on the dialer's discovery: still the relay, not the
	// connected-servers fallback.
	dialThroughRelay()
	require.Eventually(t, func() bool { return env.relay.RelayedStreams() == 0 }, 5*time.Second, 20*time.Millisecond)
	// A cached server route for dst does not outrank the relay either.
	c.setCachedRoute(env.dstPK, env.srvPK)
	dialThroughRelay()
}

// A nominee whose failure backoff has lapsed is dialed again on the next
// (unchanged) nomination: nothing else wakes the parked serve loop.
func TestRelayNominee_RetriesAfterBackoffLapses(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()

	pk, sk := GenKeyPair(t, "nominee-retry")
	c := NewClient(pk, sk, env.dc, &Config{MinSessions: 1})
	c.SetLogger(logging.MustGetLogger("nominee-retry"))
	var admit atomic.Bool
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, func(cipher.PubKey) bool { return admit.Load() }, &dials))
	go c.Serve(context.Background())
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck
	require.Eventually(t, func() bool { _, ok := c.Session(env.srvPK); return ok }, 15*time.Second, 50*time.Millisecond)

	// Refused once: backed off, no relay session.
	require.True(t, c.SetRelayPeers([]cipher.PubKey{relayPK}, 70))
	require.Eventually(t, func() bool { return dials.Load() >= 1 && c.relayBackedOff(relayPK) }, 15*time.Second, 50*time.Millisecond)
	require.False(t, c.hasRelaySession())

	// The same nomination while still backed off is a no-op.
	require.False(t, c.SetRelayPeers([]cipher.PubKey{relayPK}, 70))
	time.Sleep(300 * time.Millisecond)
	require.False(t, c.hasRelaySession())

	// Backoff lapses (and the relay now admits us): the next unchanged
	// nomination wakes the loop and the client attaches.
	admit.Store(true)
	c.noteRelayFailure(relayPK, -time.Second)
	require.False(t, c.SetRelayPeers([]cipher.PubKey{relayPK}, 70), "the set did not change")
	require.Eventually(t, c.hasRelaySession, 15*time.Second, 50*time.Millisecond)
}

// The relay forwards via the server that last reached the destination first,
// and learns that server from a forward it carried.
func TestRelayForwardSessions_CachedRouteFirst(t *testing.T) {
	env := newRelayedTestEnv(t, nil)
	env.relay.maxRelayedStreams = 8
	relayPK := env.relay.LocalPK()
	other := addServer(t, env.dc, "cached-other")
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	entry, err := env.dc.Entry(ctx, other)
	require.NoError(t, err)
	require.NoError(t, env.relay.EnsureSession(ctx, entry))

	// No cache: the destination's delegated server (srv) leads.
	got := env.relay.relayForwardSessions(env.dstPK)
	require.Len(t, got, 2)
	require.Equal(t, env.srvPK, got[0].RemotePK())
	// A cached route to the other server outranks it.
	env.relay.setCachedRoute(env.dstPK, other)
	got = env.relay.relayForwardSessions(env.dstPK)
	require.Len(t, got, 2, "the cached server is not listed twice")
	require.Equal(t, other, got[0].RemotePK())
	env.relay.evictCachedRoute(env.dstPK)

	// A forward the relay carries teaches it the route.
	c, _ := newSkynetDialer(t, env, "cached-dialer")
	var dials atomic.Int32
	c.SetSessionDialer(skynetDialer(t, env.relay, relayPK, nil, &dials))
	require.Eventually(t, func() bool { _, ok := c.Session(relayPK); return ok }, 15*time.Second, 50*time.Millisecond)
	accepted := acceptOne(t, env.dstLis)
	str, err := c.DialStream(ctx, Addr{PK: env.dstPK, Port: relayedDstPort})
	require.NoError(t, err)
	defer str.Close() //nolint:errcheck
	s := <-accepted
	require.NotNil(t, s)
	defer s.Close() //nolint:errcheck
	learned, ok := env.relay.getCachedRoute(env.dstPK)
	require.True(t, ok)
	require.Equal(t, env.srvPK, learned)
}
