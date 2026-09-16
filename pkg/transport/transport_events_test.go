package transport

import (
	"fmt"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestTransportEventsRecordOpenAndCloseReason(t *testing.T) {
	tm := newTestManager(t)
	remote, _ := cipher.GenerateKeyPair()

	mt := NewManagedTransportForTest(nil)
	mt.rPK = remote
	mt.isInitiator = true
	mt.Entry = Entry{ID: uuid.New(), Type: types.STCPR, Edges: [2]cipher.PubKey{tm.Local(), remote}}
	tm.InjectTransportForTest(mt)

	ev := tm.Events()
	require.Len(t, ev, 1)
	require.Equal(t, "open", ev[0].Event)
	require.Equal(t, mt.Entry.ID, ev[0].ID)
	require.Equal(t, remote, ev[0].Remote)
	require.True(t, ev[0].Initiator)
	require.Empty(t, ev[0].Reason)

	mt.closeWith("read: EOF")
	mt.closeWith("second caller loses") // already closed: no second event

	ev = tm.Events()
	require.Len(t, ev, 2)
	require.Equal(t, "close", ev[1].Event)
	require.Equal(t, "read: EOF", ev[1].Reason)
	require.Equal(t, "stcpr", ev[1].Type)
	require.GreaterOrEqual(t, ev[1].AgeS, 0.0)
}

func TestTransportEventRingIsBoundedAndOrdered(t *testing.T) {
	var r tpEventRing
	for i := 0; i < TransportEventRingSize+10; i++ {
		r.add(TransportEvent{Reason: fmt.Sprint(i)})
	}
	got := r.snapshot()
	require.Len(t, got, TransportEventRingSize)
	require.Equal(t, "10", got[0].Reason)
	require.Equal(t, fmt.Sprint(TransportEventRingSize+9), got[len(got)-1].Reason)
}

// A public visor's ring is flooded by autoconnect opens: the one close that
// explains why a transport died must still be findable afterwards. 3000 opens
// (more than the ring holds) must not evict the close from last_close.
func TestLastCloseSurvivesOpenFlood(t *testing.T) {
	tm := newTestManager(t)
	remote, _ := cipher.GenerateKeyPair()

	mt := NewManagedTransportForTest(nil)
	mt.rPK = remote
	mt.isInitiator = true
	mt.Entry = Entry{ID: uuid.New(), Type: types.STCPR, Edges: [2]cipher.PubKey{tm.Local(), remote}}
	tm.InjectTransportForTest(mt)
	mt.closeWith("read: connection reset by peer")

	for i := 0; i < 3000; i++ {
		other := NewManagedTransportForTest(nil)
		other.rPK = remote
		other.Entry = Entry{ID: uuid.New(), Type: types.STCPR}
		tm.InjectTransportForTest(other)
	}

	// The ring has rolled: the close is long gone from it.
	for _, ev := range tm.Events() {
		require.NotEqual(t, mt.Entry.ID, ev.ID, "the flood should have evicted every event of the closed transport")
	}

	last := tm.LastCloses()
	got, ok := last[mt.Entry.ID]
	require.True(t, ok, "the last close of a transport must survive an open flood")
	require.Equal(t, "close", got.Event)
	require.Equal(t, "read: connection reset by peer", got.Reason)
	require.Equal(t, "stcpr", got.Type)
	require.Equal(t, remote, got.Remote)
	require.GreaterOrEqual(t, got.AgeS, 0.0)
}

// The map is bounded and evicts the transport whose close is oldest; a
// transport that keeps flapping updates its own entry instead of pushing the
// rest out.
func TestLastCloseMapIsBoundedAndKeepsRepeatClosesInPlace(t *testing.T) {
	var c tpLastCloseMap
	first := uuid.New()
	c.add(TransportEvent{ID: first, Event: "close", Reason: "first"})
	for i := 0; i < 20; i++ {
		c.add(TransportEvent{ID: first, Event: "close", Reason: fmt.Sprintf("flap %d", i)})
	}
	ids := make([]uuid.UUID, 0, TransportLastCloseMax)
	for i := 0; i < TransportLastCloseMax-1; i++ {
		id := uuid.New()
		ids = append(ids, id)
		c.add(TransportEvent{ID: id, Event: "close", Reason: fmt.Sprint(i)})
	}
	got := c.snapshot()
	require.Len(t, got, TransportLastCloseMax)
	require.Equal(t, "flap 19", got[first].Reason, "a repeat close overwrites in place")

	// One more distinct transport evicts the oldest entry, which is the first.
	extra := uuid.New()
	c.add(TransportEvent{ID: extra, Event: "close", Reason: "extra"})
	got = c.snapshot()
	require.Len(t, got, TransportLastCloseMax)
	require.NotContains(t, got, first)
	require.Contains(t, got, extra)
	require.Contains(t, got, ids[0])
}
