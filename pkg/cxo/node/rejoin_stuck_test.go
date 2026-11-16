package node

import (
	"context"
	"testing"
	"time"

	skycipher "github.com/skycoin/skycoin/src/cipher"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	cxotransport "github.com/skycoin/skywire/pkg/cxo/node/transport"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgtest"
)

// TestRejoinWhileOldConnTearsDown: a peer redials while our conn to it is
// still tearing down. The new handshake must not wait for that teardown,
// or the dialer gives up first and every later redial fails the same way.
func TestRejoinWhileOldConnTearsDown(t *testing.T) {
	const port = cxotransport.DefaultCXOPort

	env := dmsgtest.NewEnv(t, 30*time.Second)
	require.NoError(t, env.Startup(0, 1, 0, &dmsg.Config{MinSessions: 1}))
	t.Cleanup(env.Shutdown)

	pkA, skA := cipher.GenerateKeyPair()
	pkB, skB := cipher.GenerateKeyPair()
	clientA, err := env.NewClientWithKeys(pkA, skA, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)
	clientB, err := env.NewClientWithKeys(pkB, skB, &dmsg.Config{MinSessions: 1})
	require.NoError(t, err)

	var skAsky, skBsky skycipher.SecKey
	copy(skAsky[:], skA[:])
	copy(skBsky[:], skB[:])
	nodeA, err := NewNode(dmsgOnlyNodeConfig("A", skAsky))
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeA.Close() }) //nolint:errcheck
	nodeB, err := NewNode(dmsgOnlyNodeConfig("B", skBsky))
	require.NoError(t, err)
	t.Cleanup(func() { _ = nodeB.Close() }) //nolint:errcheck
	require.NoError(t, nodeA.EnableDMSG(cxotransport.NewDMSGFactory(clientA, port)))
	require.NoError(t, nodeB.EnableDMSG(cxotransport.NewDMSGFactory(clientB, port)))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	require.Eventually(t, func() bool {
		c, cerr := nodeB.DMSG().ConnectPK(ctx, pkA)
		return cerr == nil && c != nil
	}, 10*time.Second, 100*time.Millisecond)

	var idB skycipher.PubKey
	copy(idB[:], pkB[:])
	var old *Conn
	require.Eventually(t, func() bool {
		c, ok := nodeA.hasPeer(idB)
		old = c
		return ok
	}, 10*time.Second, 20*time.Millisecond)

	// Hold A's conn to B in its teardown, as a handler that has not
	// returned yet would.
	old.await.Add(1)
	defer old.await.Done()

	nodeB.DMSG().Drop(pkA)
	start := time.Now()
	c, err := nodeB.DMSG().ConnectPK(ctx, pkA)
	require.NoError(t, err, "redial while A's old conn tears down")
	require.NotNil(t, c)
	require.Less(t, time.Since(start), nodeB.config.UDP.ResponseTimeout)
}
