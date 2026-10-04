package api

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/deployment"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/cxoutils"
	"github.com/skycoin/skywire/pkg/cxo/treestore"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// A visor subscribed to the conf feed gets the config GET / serves, and a
// later change reaches it over the same subscription.
func TestServicesCXOPublisherDeliversAndUpdates(t *testing.T) {
	const timeout = 30 * time.Second
	env := dmsgtest.NewEnv(t, timeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	confPK, confSK := cipher.GenerateKeyPair()
	confC, err := env.NewClientWithKeys(confPK, confSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	visorPK, visorSK := cipher.GenerateKeyPair()
	visorC, err := env.NewClientWithKeys(visorPK, visorSK, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	a := newHandlerAPI()
	sp, err := StartServicesCXOPublisher(ctx, a, confC, confSK, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sp.Close() }) //nolint:errcheck
	require.Equal(t, confPK, sp.FeedPK())

	sub, err := treestore.NewSubscriber(visorC, confPK, treestore.SubConfig{
		InMemoryDB: true,
		DmsgPort:   skyenv.DmsgConfCXOPort,
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = sub.Close() }) //nolint:errcheck

	// The single in-process dmsg server can race a handshake and drop the
	// dial; production retries, so does the test.
	for attempt := 0; ; attempt++ {
		err = sub.ConnectAndWaitForRoot(ctx, confPK)
		if err == nil || attempt == 4 || !transientDial(err) {
			break
		}
		time.Sleep(time.Duration(50<<attempt) * time.Millisecond)
	}
	require.NoError(t, err)

	got := func() deployment.Services {
		b, ok := sub.Get(deployment.ServicesCXOPath)
		require.True(t, ok, "no services leaf on the feed")
		var s deployment.Services
		require.NoError(t, json.Unmarshal(cxoutils.Gunzip(b), &s))
		return s
	}
	want := a.Services()
	first := got()
	require.Equal(t, want.ConfDmsg, first.ConfDmsg)
	require.Equal(t, want.TransportDiscoveryDmsg, first.TransportDiscoveryDmsg)
	require.Equal(t, want.TransportSetupPKs, first.TransportSetupPKs)

	newKey, _ := cipher.GenerateKeyPair()
	a.servicesMu.Lock()
	a.services.TransportSetupPKs = []cipher.PubKey{newKey}
	a.servicesMu.Unlock()
	sp.publishOnce()

	require.Eventually(t, func() bool {
		s := got()
		return len(s.TransportSetupPKs) == 1 && s.TransportSetupPKs[0] == newKey
	}, 10*time.Second, 50*time.Millisecond, "the changed config never reached the subscriber")
}

// An unchanged config is not put again.
func TestServicesCXOPublisherSkipsUnchanged(t *testing.T) {
	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	pk, sk := cipher.GenerateKeyPair()
	c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	sp, err := StartServicesCXOPublisher(context.Background(), newHandlerAPI(), c, sk, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = sp.Close() }) //nolint:errcheck

	require.Eventually(t, func() bool {
		sp.mu.Lock()
		defer sp.mu.Unlock()
		return sp.last != nil
	}, 5*time.Second, 10*time.Millisecond)
	sp.mu.Lock()
	before := sp.last
	sp.mu.Unlock()
	sp.publishOnce()
	sp.mu.Lock()
	defer sp.mu.Unlock()
	require.Same(t, &before[0], &sp.last[0], "an unchanged config was published again")
}

func transientDial(err error) bool {
	for _, frag := range []string{"dmsg error 202", "dmsg error 203", "session shutdown", "broken pipe"} {
		if strings.Contains(err.Error(), frag) {
			return true
		}
	}
	return false
}
