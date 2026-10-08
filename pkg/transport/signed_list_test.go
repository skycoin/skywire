// Package transport pkg/transport/signed_list_test.go c2-net-transport
package transport

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func TestSignedList(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	ea := MakeEntry(pk, a, tptypes.STCPR, LabelAutomatic)
	eb := MakeEntry(b, pk, tptypes.SUDPH, LabelUser)

	l1, err := NewSignedList(pk, sk, 100, []*Entry{&ea, &eb})
	require.NoError(t, err)
	require.NoError(t, l1.Verify())

	// Order and build time do not change the version.
	l2, err := NewSignedList(pk, sk, 200, []*Entry{&eb, &ea})
	require.NoError(t, err)
	require.Equal(t, l1.Version(), l2.Version())

	got := l1.Transports()
	require.Len(t, got, 2)
	ids := map[string]bool{got[0].ID.String(): true, got[1].ID.String(): true}
	require.True(t, ids[ea.ID.String()] && ids[eb.ID.String()], "entries reconstruct to the same IDs")

	// A changed set is a new version.
	l3, err := NewSignedList(pk, sk, 100, []*Entry{&ea})
	require.NoError(t, err)
	require.NotEqual(t, l1.Version(), l3.Version())

	// Whoever passes it on cannot change it.
	l1.Entries = l1.Entries[:1]
	require.ErrorIs(t, l1.Verify(), ErrSignedListSig)
	l2.PK = a
	require.ErrorIs(t, l2.Verify(), ErrSignedListSig)
}
