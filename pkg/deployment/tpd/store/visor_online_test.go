// Package store pkg/deployment/tpd/store/visor_online_test.go c4-net-discovery
package store

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// A visor is online with two transports of any type but dmsg, whether the
// store answers from redis or from its live set. Only stcpr and sudph used to
// count, so visors on squicr, swtr or webrtc read as offline.
func TestVisorOnlineCountsEveryTypeButDmsg(t *testing.T) {
	for _, live := range []bool{false, true} {
		s := newTestRedisStore(t)
		ctx := context.Background()
		a, _ := cipher.GenerateKeyPair()
		b, _ := cipher.GenerateKeyPair()
		c, _ := cipher.GenerateKeyPair()
		d, _ := cipher.GenerateKeyPair()
		tp := func(x, y cipher.PubKey, typ string) *transport.SignedEntry {
			return &transport.SignedEntry{Entry: &transport.Entry{ID: uuid.New(), Edges: transport.SortEdges(x, y), Type: tptypes.Type(typ)}}
		}
		require.NoError(t, s.RegisterTransportsBatch(ctx, cipher.PubKey{}, []*transport.SignedEntry{
			tp(a, b, "squicr"), tp(a, c, "webrtc"), // a: two non-dmsg transports
			tp(d, b, "dmsg"), tp(d, c, "dmsg"), // d: dmsg only
		}))
		if live {
			require.NoError(t, s.EnableLiveSet(ctx, ""))
		}
		sums, err := s.GetAllVisorSummaries(ctx, false, false)
		require.NoError(t, err)
		online := map[cipher.PubKey]bool{}
		for _, v := range sums {
			online[v.PK] = v.Online
		}
		require.True(t, online[a], "live=%v: squicr and webrtc count", live)
		require.False(t, online[d], "live=%v: dmsg transports do not count", live)
	}
}
