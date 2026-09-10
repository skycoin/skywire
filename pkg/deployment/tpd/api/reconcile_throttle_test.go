// Package api pkg/deployment/tpd/api/reconcile_throttle_test.go c4-net-discovery
package api

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/storeconfig"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/deployment/tpd/metrics"
	"github.com/skycoin/skywire/pkg/deployment/tpd/store"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport"
)

func TestReconcileThrottle_Plan(t *testing.T) {
	th := newReconcileThrottle(100*time.Second, 30*time.Second)
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e1 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	e2 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "sudph"}
	t0 := time.Date(2026, 9, 10, 19, 0, 0, 0, time.UTC)

	reg, hb := th.plan(t0, []*transport.Entry{e1, e2})
	require.Len(t, reg, 2, "first sight registers everything")
	require.Len(t, hb, 2)

	// The other edge's snapshot 20 s later: nothing to write.
	reg, hb = th.plan(t0.Add(20*time.Second), []*transport.Entry{e1, e2})
	require.Empty(t, reg)
	require.Empty(t, hb)

	// 45 s: heartbeat due again, registration is not.
	reg, hb = th.plan(t0.Add(45*time.Second), []*transport.Entry{e1, e2})
	require.Empty(t, reg)
	require.Len(t, hb, 2)

	// A changed label registers at once; the unchanged sibling waits.
	e1b := *e1
	e1b.Label = "renamed"
	reg, _ = th.plan(t0.Add(50*time.Second), []*transport.Entry{&e1b, e2})
	require.Equal(t, []*transport.Entry{&e1b}, reg)

	// The refresh gap elapses: the unchanged entry is registered again.
	reg, _ = th.plan(t0.Add(100*time.Second), []*transport.Entry{&e1b, e2})
	require.Equal(t, []*transport.Entry{e2}, reg)

	// A failed write is retried on the very next snapshot.
	th.forget([]*transport.Entry{e2})
	reg, _ = th.plan(t0.Add(101*time.Second), []*transport.Entry{&e1b, e2})
	require.Equal(t, []*transport.Entry{e2}, reg)

	// Marks of transports not reported for a while are dropped.
	reg, _ = th.plan(t0.Add(10*time.Minute), []*transport.Entry{e2})
	require.Len(t, reg, 1)
	th.mu.Lock()
	_, hasE1 := th.marks[e1.ID]
	th.mu.Unlock()
	require.False(t, hasE1, "e1 unseen for 4 gaps must be swept")
}

// countingStore counts the two write paths the throttle guards.
type countingStore struct {
	store.Store
	registered atomic.Int64
	heartbeats atomic.Int64
}

func (c *countingStore) RegisterTransportsBatch(ctx context.Context, r cipher.PubKey, es []*transport.SignedEntry) error {
	c.registered.Add(int64(len(es)))
	return c.Store.RegisterTransportsBatch(ctx, r, es)
}

func (c *countingStore) RecordTransportHeartbeat(ctx context.Context, id uuid.UUID, typ string, at time.Time) error {
	c.heartbeats.Add(1)
	return c.Store.RecordTransportHeartbeat(ctx, id, typ, at)
}

// Two snapshots of the same list in quick succession (the two edges, or one
// visor's 45 s heartbeat) must write the transports once, and the second
// snapshot must still be reconciled (absent entries deregistered).
func TestReconcileTransportsFromCXO_ThrottlesRepeats(t *testing.T) {
	ctx := context.Background()
	base, err := store.New(ctx, storeconfig.Config{Type: storeconfig.Memory}, 10*time.Minute, logging.MustGetLogger("test"))
	require.NoError(t, err)
	cs := &countingStore{Store: base}
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, cs, nonceMock, false, tpdiscmetrics.NewEmpty(), "", "")

	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	e1 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}
	e2 := &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(a, b), Type: "stcpr"}

	require.NoError(t, api.ReconcileTransportsFromCXO(ctx, []*transport.Entry{e1, e2}, a, "v"))
	require.EqualValues(t, 2, cs.registered.Load())
	require.EqualValues(t, 2, cs.heartbeats.Load())

	// The other edge reports the same two: no writes.
	require.NoError(t, api.ReconcileTransportsFromCXO(ctx, []*transport.Entry{e1, e2}, b, "v"))
	require.EqualValues(t, 2, cs.registered.Load())
	require.EqualValues(t, 2, cs.heartbeats.Load())

	// Edge a drops e2: e2 is deregistered even though nothing was registered.
	require.NoError(t, api.ReconcileTransportsFromCXO(ctx, []*transport.Entry{e1}, a, "v"))
	_, err = base.GetTransportByID(ctx, e2.ID)
	require.ErrorIs(t, err, store.ErrTransportNotFound)
	got, err := base.GetTransportByID(ctx, e1.ID)
	require.NoError(t, err)
	require.Equal(t, e1.ID, got.ID)
	require.EqualValues(t, 2, cs.registered.Load())
}
