// Package node pkg/cxo/node/connect_adopt_test.go c2-net-cxo
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

// dmsgOnlyNodeConfig builds a DMSG-only node Config (no TCP/UDP/RPC
// listeners) whose CXO identity is derived from sk — so the node's
// NodeID equals its dmsg client PK, the production invariant
// (treestore.NewWithDMSG derives the CXO node SecKey from the visor SK).
func dmsgOnlyNodeConfig(prefix string, sk skycipher.SecKey) *Config {
	c := NewConfig()
	c.Logger.Prefix = "[" + prefix + "] "
	c.Config.InMemoryDB = true
	c.SecKey = sk
	c.TCP.Listen = ""
	c.TCP.ResponseTimeout = 2 * time.Second
	c.TCP.Pings = 0
	c.UDP.Listen = ""
	c.UDP.ResponseTimeout = 2 * time.Second
	c.UDP.Pings = 0
	c.RPC = ""
	if testing.Verbose() {
		c.Logger.Debug = true
	}
	return c
}

// TestConnectPKAdoptsExistingConn is the in-process guard for the
// transport-discovery "delivering conn dies mid-fill" break.
//
// Topology mirrors visor<->TPD: node A dials node B (the visor's
// AnnounceTo), so B holds an INBOUND conn from A that the TPD aggregator
// would fill a Root over. Then B dials A back (the aggregator's
// ensureConn). A CXO node enforces one conn per NodeID — a second conn
// makes handshake.evictStalePeer / DMSG.addConn close the incumbent — so
// without the ConnectPK adopt guard the reverse dial tears down the very
// conn the fill runs over. The guard makes the reverse ConnectPK ADOPT
// the existing conn instead of dialing a second one, so a single stable
// conn survives for the whole fill.
func TestConnectPKAdoptsExistingConn(t *testing.T) {
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

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// A dials B (the visor's AnnounceTo). B accepts an inbound conn.
	connAB, err := nodeA.DMSG().ConnectPK(ctx, pkB)
	require.NoError(t, err)
	require.NotNil(t, connAB)

	// B's cxo NodeID for A is pkA (identity == dmsg PK).
	var idA skycipher.PubKey
	copy(idA[:], pkA[:])

	// Wait until B has registered the accepted inbound conn from A.
	var inbound *Conn
	require.Eventually(t, func() bool {
		c, ok := nodeB.hasPeer(idA)
		if ok {
			inbound = c
		}
		return ok
	}, 10*time.Second, 20*time.Millisecond, "B never registered the inbound conn from A")

	require.Equal(t, 1, len(nodeA.Connections()))
	require.Equal(t, 1, len(nodeB.Connections()))

	// Reproduce the window the fix targets: the accepted conn is
	// registered in the node-level pk->conn map (onConnInit, which runs
	// before the conn's run loop serves any Root) but NOT yet mirrored
	// into this DMSG transport's pk->conn cache (d.cs). That window is
	// live in production between onConnInit and acceptConn's addConn, and
	// again between removeConn and closeConn on teardown. Simulate it by
	// dropping B's d.cs entry while the conn stays live and pk-registered.
	dmsgB := nodeB.DMSG()
	dmsgB.mx.Lock()
	delete(dmsgB.cs, pkA)
	dmsgB.mx.Unlock()

	// B dials A back (the aggregator's ensureConn). d.cs now misses, so
	// without the adopt guard ConnectPK would DIAL a second conn to A —
	// which handshake.evictStalePeer / addConn would use to close the
	// incumbent (the fill source). With the guard it adopts the existing,
	// pk-registered conn instead.
	reverse, err := nodeB.DMSG().ConnectPK(ctx, pkA)
	require.NoError(t, err)
	require.Same(t, inbound, reverse,
		"reverse ConnectPK must adopt the existing pk-registered conn, not dial a new one that evicts it")

	// Neither side churned, and the original A->B conn is still alive.
	require.Equal(t, 1, len(nodeA.Connections()))
	require.Equal(t, 1, len(nodeB.Connections()))
	require.True(t, connIsAlive(connAB), "A's original conn must survive the reverse dial")
	require.True(t, connIsAlive(inbound), "B's inbound conn must survive the reverse dial")
}
