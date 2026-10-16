package cxoaggregate

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
)

type consumed struct {
	reporter cipher.PubKey
	leaves   map[string]string
}

func recordConsumer(t *testing.T, service string, names ...string) *[]consumed {
	var (
		mu  sync.Mutex
		got []consumed
	)
	unregister := RegisterFeedConsumer(FeedConsumer{
		Service: service,
		Consume: func(reporter cipher.PubKey, leaf Leaves) {
			c := consumed{reporter: reporter, leaves: map[string]string{}}
			for _, n := range names {
				if b, ok := leaf(n); ok {
					c.leaves[n] = string(b)
				}
			}
			mu.Lock()
			got = append(got, c)
			mu.Unlock()
		},
	})
	t.Cleanup(unregister)
	return &got
}

// Each service gets only its own leaves, under the names of its own feed.
func TestHubRoutesByService(t *testing.T) {
	sdGot := recordConsumer(t, sysfeed.ServiceDiscovery, "services", "entry")
	dGot := recordConsumer(t, sysfeed.DmsgDiscovery, "entry", "services")
	require.ElementsMatch(t, []string{sysfeed.DmsgDiscovery, sysfeed.ServiceDiscovery}, FeedConsumerServices())

	var h Hub
	require.True(t, h.Active())
	visor, _ := cipher.GenerateKeyPair()
	h.Deliver(visor, map[string][]byte{
		sysfeed.SDPrefix + "services":  []byte("S"),
		sysfeed.DmsgdPrefix + "entry":  []byte("E"),
		sysfeed.ARPrefix + "stcpr":     []byte("A"),
		"tp-list":                      []byte("ignored"),
		sysfeed.SDPrefix + "unrelated": []byte("x"),
	})

	require.Len(t, *sdGot, 1)
	require.Equal(t, visor, (*sdGot)[0].reporter)
	require.Equal(t, map[string]string{"services": "S"}, (*sdGot)[0].leaves)
	require.Len(t, *dGot, 1)
	require.Equal(t, map[string]string{"entry": "E"}, (*dGot)[0].leaves)
	require.Equal(t, uint64(2), h.Delivered())
}

// A Root that repeats the leaves within HubRefresh is not delivered again; a
// change is, and so is the same leaves once HubRefresh has passed.
func TestHubDedupesUnchangedLeaves(t *testing.T) {
	var h Hub
	visor, _ := cipher.GenerateKeyPair()
	k := hubKey{visor, sysfeed.ServiceDiscovery}
	now := time.Now()
	require.True(t, h.due(k, 1, now))
	require.False(t, h.due(k, 1, now.Add(HubRefresh/2)), "same leaves within the refresh")
	require.True(t, h.due(k, 2, now.Add(HubRefresh/2)), "changed leaves go out at once")
	require.False(t, h.due(k, 2, now.Add(HubRefresh-time.Second)))
	require.True(t, h.due(k, 2, now.Add(HubRefresh/2+HubRefresh)), "unchanged leaves are refreshed")

	h.Forget(visor)
	require.True(t, h.due(k, 2, now.Add(HubRefresh/2+HubRefresh)), "a forgotten visor starts over")
}

// Without consumers the hub is inactive and a Deliver is a no-op.
func TestHubWithoutConsumers(t *testing.T) {
	var h Hub
	if len(FeedConsumerServices()) != 0 {
		t.Skip("consumers registered by another test")
	}
	require.False(t, h.Active())
	visor, _ := cipher.GenerateKeyPair()
	h.Deliver(visor, map[string][]byte{sysfeed.SDPrefix + "services": []byte("S")})
	require.Zero(t, h.Delivered())
}

// Unregistering leaves a newer registration for the same service in place.
func TestRegisterFeedConsumerReplace(t *testing.T) {
	first := RegisterFeedConsumer(FeedConsumer{Service: sysfeed.AddressResolver, Consume: func(cipher.PubKey, Leaves) {}})
	second := RegisterFeedConsumer(FeedConsumer{Service: sysfeed.AddressResolver, Consume: func(cipher.PubKey, Leaves) {}})
	first()
	require.Contains(t, FeedConsumerServices(), sysfeed.AddressResolver)
	second()
	require.NotContains(t, FeedConsumerServices(), sysfeed.AddressResolver)
}
