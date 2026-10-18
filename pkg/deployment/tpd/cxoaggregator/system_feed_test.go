package cxoaggregator

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoaggregate"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/sysfeed"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	sdregcxo "github.com/skycoin/skywire/pkg/deployment/sd/regcxo"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/servicedisc"
	"github.com/skycoin/skywire/pkg/skyenv"
)

type sdIngests struct {
	mu   sync.Mutex
	seen map[cipher.PubKey]int
}

func (s *sdIngests) IngestServiceFromCXO(_ context.Context, reporter cipher.PubKey, se servicedisc.Service) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if se.Addr.PubKey() == reporter {
		s.seen[reporter]++
	}
}

func (s *sdIngests) count(pk cipher.PubKey) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.seen[pk]
}

func sdServicesLeaf(t *testing.T, pk cipher.PubKey) []byte {
	b, err := json.Marshal([]servicedisc.Service{{Type: servicedisc.ServiceTypeVisor, Addr: servicedisc.NewSWAddr(pk, 0)}})
	require.NoError(t, err)
	return cxoutils.FrameGzip(1, b)
}

func announce(ctx context.Context, t *testing.T, pub *treestore.Publisher, to cipher.PubKey) {
	require.Eventually(t, func() bool {
		actx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		return pub.AnnounceTo(actx, to) == nil
	}, time.Minute, 100*time.Millisecond, "announce to %s", to)
}

// A mixed fleet during the transition. The service discovery runs in TPD's
// process and takes entries both ways: from an old visor's own SD feed on
// port 72, and from a new visor's system feed through TPD's aggregator with
// no conn between that visor and the SD at all.
func TestSystemFeedMixedFleet(t *testing.T) {
	const timeout = 2 * time.Minute
	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	client := func(pk cipher.PubKey, sk cipher.SecKey) *dmsg.Client {
		c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
		require.NoError(t, err)
		return c
	}
	tpdPK, tpdSK := cipher.GenerateKeyPair()
	sdPK, sdSK := cipher.GenerateKeyPair()
	newPK, newSK := cipher.GenerateKeyPair()
	oldPK, oldSK := cipher.GenerateKeyPair()

	tpd, err := New(client(tpdPK, tpdSK), tpdSK, &recordingSink{}, Config{Logger: logging.MustGetLogger("tpd")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = tpd.Close() }) //nolint:errcheck
	tpd.Run(ctx)

	sink := &sdIngests{seen: map[cipher.PubKey]int{}}
	sdAgg, err := sdregcxo.New(client(sdPK, sdSK), sdSK, sink, sdregcxo.Config{Logger: logging.MustGetLogger("sd")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sdAgg.Close() }) //nolint:errcheck
	sdAgg.Run(ctx)
	t.Cleanup(cxoaggregate.RegisterFeedConsumer(sdregcxo.NewFeedConsumer(sink, logging.MustGetLogger("sd"))))

	// The new visor: its SD entries ride the system feed, announced to TPD.
	newPub, err := treestore.NewWithDMSG(client(newPK, newSK), newSK, treestore.PubConfig{
		InMemoryDB: true, BatchWindow: 5 * time.Millisecond, DmsgPort: skyenv.DmsgCXOPort,
		SubscriberAllowlist: []cipher.PubKey{tpdPK}, Logger: logging.MustGetLogger("new-visor"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = newPub.Close() }) //nolint:errcheck
	require.NoError(t, newPub.Put("tp-list", []byte(`{"c":[]}`)))
	require.NoError(t, newPub.Put(sysfeed.SDPrefix+"services", sdServicesLeaf(t, newPK)))
	announce(ctx, t, newPub, tpdPK)

	// The old visor: its own SD feed on port 72, announced to the SD.
	oldPub, err := treestore.NewWithDMSG(client(oldPK, oldSK), oldSK, treestore.PubConfig{
		InMemoryDB: true, BatchWindow: 5 * time.Millisecond, DmsgPort: skyenv.DmsgVisorSDRegCXOPort,
		SubscriberAllowlist: []cipher.PubKey{sdPK}, Logger: logging.MustGetLogger("old-visor"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = oldPub.Close() }) //nolint:errcheck
	require.NoError(t, oldPub.Put("services", sdServicesLeaf(t, oldPK)))
	announce(ctx, t, oldPub, sdPK)

	require.Eventually(t, func() bool { return sink.count(newPK) > 0 }, timeout, 100*time.Millisecond,
		"new visor's entry reaches the SD through TPD")
	require.Eventually(t, func() bool { return sink.count(oldPK) > 0 }, timeout, 100*time.Millisecond,
		"old visor's entry reaches the SD over its own feed")

	st := tpd.Stats()
	require.Contains(t, st.Consumers, sysfeed.ServiceDiscovery)
	require.NotZero(t, st.Delivered)
	require.NotEmpty(t, newPub.Subscriptions(tpdPK))
	require.Empty(t, newPub.Subscriptions(sdPK), "the new visor holds no conn to the SD")
}
