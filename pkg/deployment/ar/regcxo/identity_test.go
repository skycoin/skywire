// Package regcxo — identity_test.go: pins the AR-bind aggregator's
// service-identity binding.
//
// A visor gates this feed's subscriber allowlist on the CXO node's
// PeerID, allowing the AR PK it holds in transport.address_resolver_dmsg.
// If the aggregator's node is built without the AR's SecKey,
// node.NewNode mints a random keypair and every gated visor rejects the
// subscribe — silently, with the HTTP/UDP bind path still carrying
// registration so nothing looks broken. That is #4168 on TPD and #4569
// on dmsg-discovery; the AR had no test for it.
package regcxo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

type nopSink struct{}

func (nopSink) IngestBindFromCXO(_ context.Context, _ cipher.PubKey, _ types.Type, _ addrresolver.LocalAddresses) {
}

// TestAggregatorPresentsServiceIdentity: the aggregator's CXO node must
// advertise the AR's own PK on every handshake.
func TestAggregatorPresentsServiceIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	dmsgC, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	agg, err := New(dmsgC, sk, nopSink{}, Config{
		InMemoryDB: true,
		Logger:     logging.MustGetLogger("ar-bind-cxo-test"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck

	require.Equal(t, pk, agg.FeedPK(),
		"the AR-bind aggregator must present the AR's configured PK, not a random one")
}

// TestAggregatorRejectsZeroKey: a zero key must be a construction error,
// never a random identity.
func TestAggregatorRejectsZeroKey(t *testing.T) {
	_, err := New(nil, cipher.SecKey{}, nopSink{}, Config{InMemoryDB: true})
	require.ErrorIs(t, err, cxoaggregate.ErrNoServiceKey)
}
