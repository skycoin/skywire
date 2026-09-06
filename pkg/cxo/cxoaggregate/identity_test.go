// Package cxoaggregate — identity_test.go: the regression guard the
// package exists for.
//
// #4569: dmsg-discovery's aggregator built its CXO node from a bare
// node.NewConfig() with no SecKey, so node.NewNode minted a RANDOM
// keypair. Every gated visor allowlists dmsg-discovery's CONFIGURED PK,
// saw an unknown one on the handshake, and refused the subscribe — no
// visor in the deployment registered over CXO, and nothing in any log
// said so. TPD had hit the same bug earlier (#4168).
//
// These tests assert the two properties that make that unrepeatable:
// the key is required (a caller cannot omit it), and the constructed
// node actually presents the PK that key derives.
package cxoaggregate

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	skycipher "github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/cxo/node"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
	"github.com/skycoin/skywire/pkg/logging"
)

const testTimeout = 30 * time.Second

// newTestClient brings up an in-process dmsg environment and returns a
// client under the given keypair.
func newTestClient(t *testing.T, pk cipher.PubKey, sk cipher.SecKey) *dmsg.Client {
	t.Helper()
	env := dmsgtest.NewEnv(t, testTimeout)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)
	c, err := env.NewClientWithKeys(pk, sk, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	return c
}

// TestNewBindsServiceIdentity is the core assertion: the node a service
// gets back presents the service's own PK, not a freshly minted random
// one. This is the check that would have failed for months on
// dmsg-discovery.
func TestNewBindsServiceIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	dmsgC := newTestClient(t, pk, sk)

	c, err := New(dmsgC, sk, 50, Options{
		InMemoryDB: true,
		Logger:     logging.MustGetLogger("cxoaggregate-test"),
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck

	require.Equal(t, pk, c.FeedPK(),
		"the aggregator's CXO node must present the configured service PK; a mismatch is what every gated visor rejects")
	require.Equal(t, pk, cipher.PubKey(c.Node().ID()))
}

// TestNewRejectsZeroSecKey pins that omitting the identity is a startup
// error, not a silent random keypair. The compiler already prevents
// forgetting the argument (it is positional); this covers passing a zero
// value explicitly.
func TestNewRejectsZeroSecKey(t *testing.T) {
	_, err := New(nil, cipher.SecKey{}, 50, Options{InMemoryDB: true})
	require.ErrorIs(t, err, ErrNoServiceKey)
}

// TestNewRejectsZeroPort pins the other required argument: a zero DMSG
// port would listen where no visor announces.
func TestNewRejectsZeroPort(t *testing.T) {
	_, sk := cipher.GenerateKeyPair()
	_, err := New(nil, sk, 0, Options{InMemoryDB: true})
	require.Error(t, err)
}

// TestNodeConfigHookCannotUnbindIdentity: the NodeConfig escape hatch
// exists for per-service node tuning, and must not be able to undo the
// one guarantee this constructor makes.
func TestNodeConfigHookCannotUnbindIdentity(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	dmsgC := newTestClient(t, pk, sk)

	c, err := New(dmsgC, sk, 50, Options{
		InMemoryDB: true,
		Logger:     logging.MustGetLogger("cxoaggregate-test"),
		NodeConfig: func(cfg *node.Config) {
			cfg.SecKey = skycipher.SecKey{}
		},
	})
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() }) //nolint:errcheck

	require.Equal(t, pk, c.FeedPK(), "NodeConfig must not be able to unbind the service identity")
}

// TestTwoCoresShareOneDmsgClient guards #4152: node.NewConfig defaults a
// TCP listener on :8870 and RPC on :8871, so a second aggregator in the
// same process failed to construct with "address already in use" and its
// DMSG listener never came up. The shared core zeroes those listeners,
// so services can run several aggregators on one dmsg client.
func TestTwoCoresShareOneDmsgClient(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	dmsgC := newTestClient(t, pk, sk)

	first, err := New(dmsgC, sk, 50, Options{InMemoryDB: true, Logger: logging.MustGetLogger("agg-1")})
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() }) //nolint:errcheck

	second, err := New(dmsgC, sk, 69, Options{InMemoryDB: true, Logger: logging.MustGetLogger("agg-2")})
	require.NoError(t, err, "a second aggregator on the same client must construct")
	t.Cleanup(func() { _ = second.Close() }) //nolint:errcheck

	require.Equal(t, first.FeedPK(), second.FeedPK(),
		"both aggregators of one service present that service's PK")
}
