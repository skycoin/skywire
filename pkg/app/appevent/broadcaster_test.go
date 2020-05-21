package appevent

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appcommon"
)

func TestBroadcaster_Broadcast(t *testing.T) {
	const timeout = time.Second * 2

	// makeMockClient creates a mock RPCClient that appends received events to 'gotEvents'.
	makeMockClient := func(subs map[string]bool, gotEvents *[]*Event) RPCClient {
		mockC := new(MockRPCClient)
		mockC.On("Close").Return(nil)
		mockC.On("Hello").Return(&appcommon.Hello{ProcKey: appcommon.RandProcKey(), EventSubs: subs})
		mockC.On("Notify", mock.Anything, mock.Anything).Return(nil).Run(func(args mock.Arguments) {
			*gotEvents = append(*gotEvents, args.Get(1).(*Event))
		})
		return mockC
	}

	// makeEvents makes (n) number of random events.
	makeEvents := func(n int) []*Event {
		evs := make([]*Event, 0, n)
		i := 0
		for {
			for t := range AllTypes() {
				evs = append(evs, NewEvent(t, struct{}{}))
				if i++; i == n {
					return evs
				}
			}
		}
	}

	// extractEvents returns events that are part of the subs.
	extractEvents := func(events []*Event, subs map[string]bool) []*Event {
		out := make([]*Event, 0, len(events))
		for _, ev := range events {
			if subs[ev.Type] {
				out = append(out, ev)
			}
		}
		return out
	}

	// Ensure Broadcast correctly broadcasts events to the internal RPCClients.
	// Arrange:
	// - There is a n(C) number of RPCClients within the Broadcaster.
	// - All the aforementioned RPCClients are subscribed to all possible event types.
	// Act:
	// - Broadcast n(E) number of events using Broadcaster.Broadcast.
	// Assert:
	// - Each of the n(C) RPCClients should receive n(E) event objects.
	// - Received event objects should be in the order of sent.
	t.Run("broadcast_events", func(t *testing.T) {

		// Arrange: constants.
		const nClients = 12
		const nEvents = 52

		// Arrange: prepare broadcaster.
		bc := NewBroadcaster(nil, timeout)
		defer func() { assert.NoError(t, bc.Close()) }()

		// Arrange: events to broadcast and results slice.
		events := makeEvents(nEvents)
		results := make([][]*Event, nClients)
		for i := 0; i < nClients; i++ {
			bc.AddClient(makeMockClient(AllTypes(), &results[i]))
		}

		// Act: broadcast events.
		for _, ev := range events {
			require.NoError(t, bc.Broadcast(context.Background(), ev))
		}

		// Assert: received events of each RPCClient.
		for i, r := range results {
			assert.Len(t, r, nEvents, i)
			assert.Equal(t, events, r, i)
		}
	})

	// Ensure Broadcaster only broadcasts an event to a RPCClient if the RPCClient is subscribed to the event type.
	// Arrange:
	// - There is a RPCClient and a Broadcaster.
	// - The RPCClient is only subscribed to one event type.
	// Act:
	// - Broadcaster broadcasts all event types.
	// Assert:
	// - The RPCClient should have only received events that are of subscribed types.
	t.Run("broadcast_only_subscribed_events", func(t *testing.T) {

		// Arrange: constants/variables
		const nEvents = 64
		subs := map[string]bool{TCPDial: true}

		// Arrange: events to broadcast and results slice.
		events := makeEvents(nEvents)
		result := make([]*Event, 0, nEvents)

		// Arrange: prepare RPCClient.
		mockC := makeMockClient(subs, &result)
		defer func() { assert.NoError(t, mockC.Close()) }()

		// Arrange: prepare broadcaster.
		bc := NewBroadcaster(nil, timeout)
		bc.AddClient(mockC)
		defer func() { assert.NoError(t, bc.Close()) }()

		// Act: broadcast events.
		for _, ev := range events {
			require.NoError(t, bc.Broadcast(context.TODO(), ev))
		}

		// Assert: resultant events slice outputted from mock client.
		expectedEvents := extractEvents(events, subs)
		assert.Len(t, result, len(expectedEvents))
		assert.Equal(t, expectedEvents, result)
		expJ, err := json.Marshal(expectedEvents)
		require.NoError(t, err)
		resJ, err := json.Marshal(result)
		require.NoError(t, err)
		assert.JSONEq(t, string(expJ), string(resJ))
	})
}
