// Package regcxo — identity_test.go: the regression guard for #4569.
//
// No visor was registering over CXO because this aggregator's CXO node
// was built from a bare config, so node.NewNode minted a RANDOM keypair
// and every gated visor refused the subscribe — it allowlists
// dmsg-discovery's configured PK and saw an unknown one — observed live
// as a denied subscriber PK matching nothing the visor allowlisted.
//
// The key is now a required argument to New, so the omission cannot
// recur; this asserts the constructed aggregator really does present
// dmsg-discovery's PK.
package regcxo

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

type nopSink struct{}

func (nopSink) IngestEntryFromCXO(_ context.Context, _ *disc.Entry, _ cipher.PubKey) {}

// TestAggregatorPresentsServiceIdentity: the aggregator's CXO node must
// advertise dmsg-discovery's own PK on every handshake, because that is
// the PK gated visors allowlist for this feed.
func TestAggregatorPresentsServiceIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()

	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	dmsgC, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	agg, err := New(dmsgC, sk, nopSink{}, Config{
		InMemoryDB: true,
		Logger:     logging.MustGetLogger("dmsgd-registration-cxo-test"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = agg.Close() }) //nolint:errcheck

	require.Equal(t, pk, agg.FeedPK(),
		"the registration aggregator must present dmsg-discovery's configured PK, not a random one")
}

// TestAggregatorRejectsZeroKey: a zero key is the exact #4569 shape and
// must be a construction error, never a random identity.
func TestAggregatorRejectsZeroKey(t *testing.T) {
	_, err := New(nil, cipher.SecKey{}, nopSink{}, Config{InMemoryDB: true})
	require.ErrorIs(t, err, cxoaggregate.ErrNoServiceKey)
}
