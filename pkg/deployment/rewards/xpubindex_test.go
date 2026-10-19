package rewards

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skycoin/src/cipher/bip32"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/visor/rewardconfig"
)

func testXpub(t *testing.T) string {
	m, err := bip32.NewMasterKey([]byte("xpub index test seed, 32 bytes!!"))
	require.NoError(t, err)
	k := m
	for _, c := range []uint32{44, 8000, 0} {
		k, err = k.NewPrivateChildKey(bip32.FirstHardenedChild + c)
		require.NoError(t, err)
	}
	pub, err := k.PublicKey()
	require.NoError(t, err)
	return pub.String()
}

func TestXpubIndexAssign(t *testing.T) {
	xpub := testXpub(t)
	addr := func(i uint32) string {
		a, err := rewardconfig.DeriveExternalAddressFromXpub(xpub, i)
		require.NoError(t, err)
		return a
	}
	path := filepath.Join(t.TempDir(), XpubIndexFile)

	// Indexes 0 to 2 already received coins before the record existed.
	onChain := map[string]bool{addr(0): true, addr(1): true, addr(2): true}
	used := func(a string) (bool, error) { return onChain[a], nil }

	x, err := LoadXpubIndex(path)
	require.NoError(t, err)
	i, err := x.Assign(xpub, "2026-10-17", used)
	require.NoError(t, err)
	require.Equal(t, uint32(3), i)

	// The same date keeps its index, even before it is paid.
	i, err = x.Assign(xpub, "2026-10-17", used)
	require.NoError(t, err)
	require.Equal(t, uint32(3), i)

	// A pending date is never handed out again to the next date.
	i, err = x.Assign(xpub, "2026-10-18", used)
	require.NoError(t, err)
	require.Equal(t, uint32(4), i)

	// The record survives a reload and does not store the xpub.
	b, err := os.ReadFile(path) //nolint:gosec
	require.NoError(t, err)
	require.False(t, strings.Contains(string(b), xpub))
	x, err = LoadXpubIndex(path)
	require.NoError(t, err)
	maxIdx, ok := x.MaxIndex(xpub)
	require.True(t, ok)
	require.Equal(t, uint32(4), maxIdx)

	// Addresses the owner used past the record are skipped too.
	onChain[addr(5)] = true
	i, err = x.Assign(xpub, "2026-10-19", used)
	require.NoError(t, err)
	require.Equal(t, uint32(6), i)

	// A failed chain check assigns nothing.
	_, err = x.Assign(xpub, "2026-10-20", func(string) (bool, error) { return false, errors.New("node down") })
	require.Error(t, err)
	maxIdx, _ = x.MaxIndex(xpub)
	require.Equal(t, uint32(6), maxIdx)
}
