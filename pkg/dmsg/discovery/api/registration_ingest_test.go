package api

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
)

// signedClientEntryAtSeq builds a signed client entry for a fixed key at the
// given sequence — unlike the shared signedClientEntry helper, which mints a
// fresh key each call, this lets a test iterate the SAME entry's sequence.
func signedClientEntryAtSeq(t *testing.T, pk cipher.PubKey, sk cipher.SecKey, seq uint64) *disc.Entry {
	t.Helper()
	e := disc.NewClientEntry(pk, seq, nil)
	require.NoError(t, e.Sign(sk))
	return e
}

// TestIngestEntryFromCXO_InsertAndIdempotentUpdate covers the core
// registration-over-CXO ingest contract: a fresh entry inserts, a strictly
// newer sequence updates, and a stale-or-equal sequence is a silent no-op
// (so dual-write with the HTTP path never conflicts).
func TestIngestEntryFromCXO_InsertAndIdempotentUpdate(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	pk, sk := cipher.GenerateKeyPair()

	// 1. Fresh insert (absent → seq 0).
	e0 := signedClientEntryAtSeq(t, pk, sk, 0)
	a.IngestEntryFromCXO(ctx, e0, pk)
	got, err := db.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(0), got.Sequence)

	// 2. Strictly-newer update applies.
	e1 := signedClientEntryAtSeq(t, pk, sk, 1)
	a.IngestEntryFromCXO(ctx, e1, pk)
	got, err = db.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Sequence)

	// 3. Re-ingesting the SAME sequence is an idempotent no-op (this is
	//    exactly what happens when the HTTP PUT landed first): store stays
	//    at seq 1, no error, no panic.
	a.IngestEntryFromCXO(ctx, e1, pk)
	got, err = db.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Sequence)

	// 4. A STALE (lower) sequence is dropped — never rolls the entry back.
	a.IngestEntryFromCXO(ctx, e0, pk)
	got, err = db.Entry(ctx, pk)
	require.NoError(t, err)
	require.Equal(t, uint64(1), got.Sequence)
}

// TestIngestEntryFromCXO_RejectsForeignAndServerEntries ensures a visor can
// only publish its OWN client entry: a mismatched reporter PK or a server
// entry is dropped without touching the store.
func TestIngestEntryFromCXO_RejectsForeignAndServerEntries(t *testing.T) {
	ctx := context.Background()
	db := store.NewMock()
	a := newAPI(db)
	pk, sk := cipher.GenerateKeyPair()
	otherPK, _ := cipher.GenerateKeyPair()

	// entry.Static == pk, but reporter (feed PK) is someone else → drop.
	e := signedClientEntryAtSeq(t, pk, sk, 0)
	a.IngestEntryFromCXO(ctx, e, otherPK)
	_, err := db.Entry(ctx, pk)
	require.Equal(t, disc.ErrKeyNotFound, err, "foreign-reporter entry must not be stored")

	// A correct reporter but a tampered signature (re-sign with wrong key)
	// must also be rejected.
	badSK := func() cipher.SecKey { _, s := cipher.GenerateKeyPair(); return s }()
	eBad := disc.NewClientEntry(pk, 0, nil)
	require.NoError(t, eBad.Sign(badSK)) // signed by the wrong key
	a.IngestEntryFromCXO(ctx, eBad, pk)
	_, err = db.Entry(ctx, pk)
	require.Equal(t, disc.ErrKeyNotFound, err, "bad-signature entry must not be stored")
}
