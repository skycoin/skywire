// Package router pkg/router/coverage_extras_test.go
//
// Focused unit coverage for small, pure-logic helpers that had no direct
// test: the ClientPool lifecycle ops, the cascade ackRegistry unregister
// path, the RSNRelayCache, the idReserver query/return helpers, the
// setup-node reordering helper, and the transport-query JSON codec.
package router

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// ---- ClientPool -----------------------------------------------------------

// TestClientPool_DiscardSizeAndPut walks the Put/Size/Discard/replace paths and
// the nil-client guards, using the in-memory countingDialer.
func TestClientPool_DiscardSizeAndPut(t *testing.T) {
	d := &countingDialer{}
	pool := NewClientPool(d, DefaultPoolTTL)
	t.Cleanup(pool.Close)

	require.Zero(t, pool.Size(), "new pool is empty")

	// nil is a no-op for both Put and Discard.
	pool.Put(nil)
	pool.Discard(nil)
	require.Zero(t, pool.Size())

	pk, _ := cipher.GenerateKeyPair()
	c1, err := pool.Get(context.Background(), pk)
	require.NoError(t, err)
	require.EqualValues(t, 1, d.count())

	pool.Put(c1)
	require.Equal(t, 1, pool.Size(), "a returned client is pooled")

	// Putting a second client for the same PK replaces (and closes) the older.
	c2, err := pool.Get(context.Background(), pk) // reuses c1, empties pool
	require.NoError(t, err)
	require.Same(t, c1, c2)
	require.Zero(t, pool.Size())
	pool.Put(c2)
	// Dial a genuinely fresh one and Put it under the same PK to hit replace.
	pool.mu.Lock()
	pool.clients[pk].lastUsed = pool.clients[pk].lastUsed.Add(-2 * DefaultPoolTTL)
	pool.mu.Unlock()
	c3, err := pool.Get(context.Background(), pk) // stale -> fresh dial
	require.NoError(t, err)
	require.EqualValues(t, 2, d.count())
	pool.Put(c3)
	require.Equal(t, 1, pool.Size())

	// Discard closes a client without pooling it.
	c4, err := pool.Get(context.Background(), pk)
	require.NoError(t, err)
	pool.Discard(c4)
	require.Zero(t, pool.Size(), "discarded client is not pooled")
}

// TestClientPool_Evict reaps entries idled past the TTL and leaves fresh ones.
func TestClientPool_Evict(t *testing.T) {
	d := &countingDialer{}
	pool := NewClientPool(d, DefaultPoolTTL)
	t.Cleanup(pool.Close)

	pk, _ := cipher.GenerateKeyPair()
	c, err := pool.Get(context.Background(), pk)
	require.NoError(t, err)
	pool.Put(c)
	require.Equal(t, 1, pool.Size())

	// Not yet past TTL: evict keeps it.
	pool.evict()
	require.Equal(t, 1, pool.Size())

	// Age it past the TTL: evict reaps it.
	pool.mu.Lock()
	pool.clients[pk].lastUsed = pool.clients[pk].lastUsed.Add(-2 * DefaultPoolTTL)
	pool.mu.Unlock()
	pool.evict()
	require.Zero(t, pool.Size(), "entry past TTL is evicted")
}

// TestClientPool_CloseIdempotent proves Close reaps live entries and can be
// called more than once safely.
func TestClientPool_CloseIdempotent(t *testing.T) {
	d := &countingDialer{}
	pool := NewClientPool(d, DefaultPoolTTL)

	pk, _ := cipher.GenerateKeyPair()
	c, err := pool.Get(context.Background(), pk)
	require.NoError(t, err)
	pool.Put(c)
	require.Equal(t, 1, pool.Size())

	pool.Close()
	require.Zero(t, pool.Size(), "Close reaps all pooled entries")
	pool.Close() // second Close must not panic
}

// ---- ackRegistry ----------------------------------------------------------

// TestAckRegistry_Unregister proves unregister drops the wait channel so a
// later dispatch finds no waiter.
func TestAckRegistry_Unregister(t *testing.T) {
	r := newAckRegistry()

	ch := r.register(42)
	require.NotNil(t, ch)

	r.unregister(42)
	// After unregister, dispatch reports no waiter and does not block.
	require.False(t, r.dispatch(42, nil), "dispatch to an unregistered session finds no waiter")

	// unregister of an unknown session is a no-op.
	r.unregister(999)
}

// ---- RSNRelayCache --------------------------------------------------------

// TestRSNRelayCache_UpdateGet exercises the cache store/read and the no-peers
// error path of FindRelayTransport.
func TestRSNRelayCache_UpdateGet(t *testing.T) {
	rc := NewRSNRelayCache(logging.MustGetLogger("relaycache_test"))

	rsnPK, _ := cipher.GenerateKeyPair()
	require.Nil(t, rc.Get(rsnPK), "unknown RSN has no cached peers")

	p1, _ := cipher.GenerateKeyPair()
	p2, _ := cipher.GenerateKeyPair()
	rc.Update(rsnPK, []cipher.PubKey{p1, p2})
	require.Equal(t, []cipher.PubKey{p1, p2}, rc.Get(rsnPK))

	// FindRelayTransport with no cached peers returns a clear error.
	other, _ := cipher.GenerateKeyPair()
	_, _, err := rc.FindRelayTransport(context.Background(), other, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no cached relay peers")
}

// ---- idReserver query / return helpers ------------------------------------

// TestIDReserver_QueryAndReturnToPool covers TotalIDs, Client, String and
// ReturnToPool on a mock-dialed reserver.
func TestIDReserver_QueryAndReturnToPool(t *testing.T) {
	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()

	dialer := newMockDialer(t, nil)
	paths := [][]routing.Hop{makeHops(pkA, pkB), makeHops(pkB, pkA)}
	rtIDR, err := NewIDReserver(context.Background(), dialer, nil, paths)
	require.NoError(t, err)

	v := rtIDR.(*idReserver)
	require.Equal(t, 4, v.TotalIDs())
	require.NotNil(t, v.Client(pkA))
	require.NotNil(t, v.Client(pkB))

	// String renders the (empty, pre-reserve) ids map as JSON without error.
	require.NotEmpty(t, v.String())

	// ReturnToPool hands the clients to a pool for reuse instead of closing.
	pool := NewClientPool(&countingDialer{}, DefaultPoolTTL)
	t.Cleanup(pool.Close)
	v.ReturnToPool(pool)
	require.Equal(t, 2, pool.Size(), "both clients returned to the pool")
}

// ---- setup-node reordering ------------------------------------------------

// TestReorderSetupNodes is table-driven over the move-to-front logic.
func TestReorderSetupNodes(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	c, _ := cipher.GenerateKeyPair()
	d, _ := cipher.GenerateKeyPair()

	cases := []struct {
		name    string
		nodes   []cipher.PubKey
		success cipher.PubKey
		want    []cipher.PubKey
	}{
		{"empty", nil, a, nil},
		{"single", []cipher.PubKey{a}, a, []cipher.PubKey{a}},
		{"already-first", []cipher.PubKey{a, b, c}, a, []cipher.PubKey{a, b, c}},
		{"not-found", []cipher.PubKey{a, b, c}, d, []cipher.PubKey{a, b, c}},
		{"middle", []cipher.PubKey{a, b, c}, c, []cipher.PubKey{c, a, b}},
		{"second", []cipher.PubKey{a, b, c, d}, b, []cipher.PubKey{b, a, c, d}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ReorderSetupNodes(tc.nodes, tc.success)
			assert.Equal(t, tc.want, got)
		})
	}
}

// ---- transport-query JSON codec -------------------------------------------

// TestTransportQuery_MarshalRoundTrip proves the query and response marshal and
// unmarshal back to an equal value, and that a malformed payload errors.
func TestTransportQuery_MarshalRoundTrip(t *testing.T) {
	rsnPK, _ := cipher.GenerateKeyPair()
	dstPK, _ := cipher.GenerateKeyPair()
	srcPK, _ := cipher.GenerateKeyPair()

	q := &TransportQuery{RSNPK: rsnPK, TargetPK: dstPK, RequesterPK: srcPK, Nonce: 1234}
	raw, err := q.Marshal()
	require.NoError(t, err)

	q2, err := UnmarshalTransportQuery(raw)
	require.NoError(t, err)
	require.Equal(t, q, q2)

	_, err = UnmarshalTransportQuery([]byte("not json"))
	require.Error(t, err)

	resp := &TransportQueryResponse{
		TargetPK: dstPK,
		Entries: []transport.CompactEntry{
			{Remote: srcPK, Type: "stcpr"},
		},
	}
	rraw, err := resp.Marshal()
	require.NoError(t, err)

	resp2, err := UnmarshalTransportQueryResponse(rraw)
	require.NoError(t, err)
	require.Equal(t, resp, resp2)

	_, err = UnmarshalTransportQueryResponse([]byte("{bad"))
	require.Error(t, err)
}

// ---- hasRouteSelectingHook ------------------------------------------------

// plainDialHook implements DialHook only (no SelectRoute).
type plainDialHook struct{}

func (plainDialHook) BeforeDial(_ context.Context, _ DialInfo) (DialAdjustment, error) {
	return DialAdjustment{}, nil
}

// selectingDialHook implements the RouteSelectingHook super-interface.
type selectingDialHook struct{ plainDialHook }

func (selectingDialHook) SelectRoute(_ context.Context, _ DialInfo, _, _ []CandidateInfo) (RouteSelection, error) {
	return RouteSelection{}, nil
}

// TestHasRouteSelectingHook covers the three branches: no config, a plain hook,
// and a route-selecting hook.
func TestHasRouteSelectingHook(t *testing.T) {
	// nil conf -> false.
	r := &router{}
	require.False(t, r.hasRouteSelectingHook())

	// conf present, no hook -> false.
	r.conf = &Config{}
	require.False(t, r.hasRouteSelectingHook())

	// plain DialHook (not selecting) -> false.
	r.conf = &Config{DialHook: plainDialHook{}}
	require.False(t, r.hasRouteSelectingHook())

	// route-selecting hook -> true.
	r.conf = &Config{DialHook: selectingDialHook{}}
	require.True(t, r.hasRouteSelectingHook())
}

// ---- Map pool helpers -----------------------------------------------------

// TestMap_PooledAndDiscard covers MakePooledMap (empty and populated) and
// Map.DiscardFromPool.
func TestMap_PooledAndDiscard(t *testing.T) {
	pool := NewClientPool(&countingDialer{}, DefaultPoolTTL)
	t.Cleanup(pool.Close)

	// Empty PK set returns an empty map without dialing.
	empty, err := MakePooledMap(context.Background(), pool, nil)
	require.NoError(t, err)
	require.Empty(t, empty)

	pkA, _ := cipher.GenerateKeyPair()
	pkB, _ := cipher.GenerateKeyPair()
	m, err := MakePooledMap(context.Background(), pool, []cipher.PubKey{pkA, pkB})
	require.NoError(t, err)
	require.Len(t, m, 2)

	// DiscardFromPool closes every client and empties the map (does NOT pool).
	m.DiscardFromPool(pool)
	require.Empty(t, m)
	require.Zero(t, pool.Size(), "discarded clients are not returned to the pool")
}
