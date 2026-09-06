// Package regcxo — aggregator_test.go: pins the SD-registration
// aggregator's service-identity binding and its batched-leaf decoding.
//
// A visor gates this feed's subscriber allowlist on the CXO node's PeerID,
// allowing the SD PK it holds in launcher.service_discovery(_dmsg). If the
// aggregator's node is built without the SD's SecKey, node.NewNode mints a
// random keypair and every gated visor rejects the subscribe — silently,
// with the HTTP register still carrying registration so nothing looks
// broken. That is #4168 on TPD and #4569 on dmsg-discovery.
package regcxo

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
)

type nopSink struct{}

func (nopSink) IngestServiceFromCXO(_ context.Context, _ cipher.PubKey, _ servicedisc.Service) {}

// TestAggregatorPresentsServiceIdentity: the aggregator's CXO node must
// advertise the SD's own PK on every handshake.
func TestAggregatorPresentsServiceIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	dmsgC, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	agg, err := New(dmsgC, sk, nopSink{}, Config{
		InMemoryDB: true,
		Logger:     logging.MustGetLogger("sd-reg-cxo-test"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck

	require.Equal(t, pk, agg.FeedPK(),
		"the SD-registration aggregator must present the SD's configured PK, not a random one")
}

// TestAggregatorRejectsZeroKey: a zero key must be a construction error,
// never a random identity.
func TestAggregatorRejectsZeroKey(t *testing.T) {
	_, err := New(nil, cipher.SecKey{}, nopSink{}, Config{InMemoryDB: true})
	require.ErrorIs(t, err, cxoaggregate.ErrNoServiceKey)
}

// TestDecodeServicesBatch round-trips the leaf shape the visor publisher
// writes, and pins the version gate: a leaf framed with an unknown version
// is skipped, not misparsed.
func TestDecodeServicesBatch(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	in := []servicedisc.Service{
		{Addr: servicedisc.NewSWAddr(pk, 44), Type: servicedisc.ServiceTypeVisor, Version: "v1.3.30"},
		{Addr: servicedisc.NewSWAddr(pk, 3), Type: servicedisc.ServiceTypeVPN, Version: "v1.3.30"},
	}
	payload, err := json.Marshal(in)
	require.NoError(t, err)

	out, ok := decodeServicesBatch(cxoutils.FrameGzip(servicesBatchVersion, payload))
	require.True(t, ok)
	require.Equal(t, in, out)

	_, ok = decodeServicesBatch(cxoutils.FrameGzip(servicesBatchVersion+1, payload))
	require.False(t, ok, "an unknown version byte must be skipped, not decoded")

	_, ok = decodeServicesBatch([]byte("not a frame"))
	require.False(t, ok)
}
