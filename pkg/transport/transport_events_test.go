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
