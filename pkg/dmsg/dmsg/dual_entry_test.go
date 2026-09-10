// Package dmsg pkg/dmsg/dmsg/dual_entry_test.go c1-net-dmsg
package dmsg

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

// dualEntity wires an EntityCommon and a discovery endpoint over one mock
// discovery, so both entry-update paths can be driven against the same key.
func dualEntity(t *testing.T, name string) (*EntityCommon, *discoveryEndpoint, disc.APIClient, cipher.PubKey) {
	t.Helper()
	pk, sk := GenKeyPair(t, name)
	dc := disc.NewMock(0)
	c := new(EntityCommon)
	c.init(pk, sk, dc, logging.MustGetLogger(name), 0)
	return c, &discoveryEndpoint{Client: dc, PK: pk}, dc, pk
}

// A visor running the in-process dmsg server on its own key registers both
// roles in ONE entry. Whichever half registers first, the second must be
// ADDED to that entry rather than refused or replacing it, and the entry must
// stay correctly signed.
func TestDualEntry_ServerHalfAddedToExistingClientEntry(t *testing.T) {
	c, ep, dc, pk := dualEntity(t, "client-first")
	ctx := context.Background()
	srv, _ := cipher.GenerateKeyPair()

	_, err := c.updateClientEntryOnEndpoint(ctx, ep, "visor", []cipher.PubKey{srv}, true)
	require.NoError(t, err)
	got, err := dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.NotNil(t, got.Client)
	require.Nil(t, got.Server, "only the client half is registered yet")

	// The dmsg server comes up on the same key.
	require.NoError(t, c.updateServerEntryOnEndpoint(ctx, ep, "1.2.3.4:8081", "", 100, ""))

	got, err = dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.NotNil(t, got.Server, "the server half must be added")
	require.Equal(t, "1.2.3.4:8081", got.Server.Address)
	require.Equal(t, 100, got.Server.AvailableSessions)
	require.NotNil(t, got.Client, "the client half must survive")
	require.Equal(t, []cipher.PubKey{srv}, got.Client.DelegatedServers)
	require.NoError(t, got.VerifySignature())
	require.NoError(t, got.Validate(false))
}

// The reverse order: the server registered first, then the visor's client.
// This is the direction that used to fail hardest — the client path replaced
// the whole entry with a fresh sequence-0 one, which both dropped the server
// half and was rejected because a new sequence must exceed the stored one.
func TestDualEntry_ClientHalfAddedToExistingServerEntry(t *testing.T) {
	c, ep, dc, pk := dualEntity(t, "server-first")
	ctx := context.Background()
	srv, _ := cipher.GenerateKeyPair()

	require.NoError(t, c.updateServerEntryOnEndpoint(ctx, ep, "5.6.7.8:8081", "", 42, ""))
	got, err := dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.NotNil(t, got.Server)
	require.Nil(t, got.Client, "only the server half is registered yet")
	seqBefore := got.Sequence

	entry, err := c.updateClientEntryOnEndpoint(ctx, ep, "visor", []cipher.PubKey{srv}, true)
	require.NoError(t, err, "the client half must register, not be rejected")
	require.NotNil(t, entry)

	got, err = dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.NotNil(t, got.Client, "the client half must be added")
	require.Equal(t, []cipher.PubKey{srv}, got.Client.DelegatedServers)
	require.NotNil(t, got.Server, "the server half must survive")
	require.Equal(t, "5.6.7.8:8081", got.Server.Address)
	require.Equal(t, 42, got.Server.AvailableSessions)
	require.Greater(t, got.Sequence, seqBefore, "the merge iterates the entry, it does not restart it")
	require.NoError(t, got.VerifySignature())
	require.NoError(t, got.Validate(false))
}

// Once merged, each loop keeps refreshing its own half without disturbing the
// other — the steady state for a co-resident visor and dmsg server.
func TestDualEntry_RefreshKeepsBothHalves(t *testing.T) {
	c, ep, dc, pk := dualEntity(t, "steady")
	ctx := context.Background()
	srvA, _ := cipher.GenerateKeyPair()
	srvB, _ := cipher.GenerateKeyPair()

	_, err := c.updateClientEntryOnEndpoint(ctx, ep, "visor", []cipher.PubKey{srvA}, true)
	require.NoError(t, err)
	require.NoError(t, c.updateServerEntryOnEndpoint(ctx, ep, "9.9.9.9:8081", "", 10, ""))

	// The server's session count moves.
	require.NoError(t, c.updateServerEntryOnEndpoint(ctx, ep, "9.9.9.9:8081", "", 7, ""))
	got, err := dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, 7, got.Server.AvailableSessions)
	require.NotNil(t, got.Client)

	// The client's delegated servers move.
	_, err = c.updateClientEntryOnEndpoint(ctx, ep, "visor", []cipher.PubKey{srvB}, true)
	require.NoError(t, err)
	got, err = dc.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{srvB}, got.Client.DelegatedServers)
	require.NotNil(t, got.Server, "the server half must still be there")
	require.Equal(t, "9.9.9.9:8081", got.Server.Address)
	require.Equal(t, 7, got.Server.AvailableSessions)
	require.NoError(t, got.VerifySignature())
}
