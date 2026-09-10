package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
)

// signedDualEntry builds a signed entry carrying BOTH sections — what a visor
// running the in-process dmsg server on its own key publishes.
func signedDualEntry(t *testing.T, pk cipher.PubKey, sk cipher.SecKey, seq uint64, addr string) *disc.Entry {
	t.Helper()
	e := disc.NewClientEntry(pk, seq, nil)
	e.Server = &disc.Server{Address: addr, AvailableSessions: 100}
	require.NoError(t, e.Sign(sk))
	return e
}

// An entry carrying both sections must be ingested, not dropped. Dropping it
// forced a co-resident visor and dmsg server back onto the HTTP re-PUT that
// registration-over-CXO exists to avoid, one Noise+PQ handshake per refresh.
func TestIngestEntryFromCXO_AcceptsDualEntry(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	pk, sk := cipher.GenerateKeyPair()

	a.IngestEntryFromCXO(ctx, signedDualEntry(t, pk, sk, 0, "1.2.3.4:8081"), pk)
	got, err := db.Entry(ctx, pk)
	require.NoError(t, err, "a dual entry must be ingested")
	require.NotNil(t, got.Client, "the client half is what this path registers")
	require.NotNil(t, got.Server, "the server half must be stored, not stripped")
	require.Equal(t, "1.2.3.4:8081", got.Server.Address)

	// It iterates like any other entry.
	a.IngestEntryFromCXO(ctx, signedDualEntry(t, pk, sk, 1, "1.2.3.4:9091"), pk)
	got, err = db.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Sequence)
	require.Equal(t, "1.2.3.4:9091", got.Server.Address)
}

// The guard that matters is unchanged: a visor may publish only its own key,
// so it cannot advertise a reachable address for somebody else's.
func TestIngestEntryFromCXO_DualEntryStillBoundToReporter(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	pk, sk := cipher.GenerateKeyPair()
	other, _ := cipher.GenerateKeyPair()

	a.IngestEntryFromCXO(ctx, signedDualEntry(t, pk, sk, 0, "1.2.3.4:8081"), other)
	_, err := db.Entry(ctx, pk)
	require.Error(t, err, "an entry whose key is not the feed's must be dropped")
}

// A server-only entry has no client half, so this path has nothing to register.
func TestIngestEntryFromCXO_IgnoresServerOnlyEntry(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	pk, sk := cipher.GenerateKeyPair()

	e := disc.NewServerEntry(pk, 0, "1.2.3.4:8081", 100)
	require.NoError(t, e.Sign(sk))
	a.IngestEntryFromCXO(ctx, e, pk)
	_, err := db.Entry(ctx, pk)
	require.Error(t, err)
}
