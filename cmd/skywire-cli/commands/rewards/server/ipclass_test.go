package clirewardsserver

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestReadIPClasses(t *testing.T) {
	dir := t.TempDir()
	survey := func(ip string) string {
		pk, _ := cipher.GenerateKeyPair()
		require.NoError(t, os.MkdirAll(filepath.Join(dir, pk.Hex()), 0o750))
		require.NoError(t, os.WriteFile(filepath.Join(dir, pk.Hex(), "node-info.json"), []byte(`{"ip_address":"`+ip+`"}`), 0o600))
		return pk.Hex()
	}
	a, b, c, noIP := survey("1.2.3.4"), survey("1.2.3.4"), survey("5.6.7.8"), survey("")
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "not-a-key"), 0o750))

	_, sk := cipher.GenerateKeyPair()
	got := readIPClasses(dir, ipClassKey(sk))
	require.Len(t, got, 3, "the survey without an IP, and the non-key directory, are skipped")
	require.NotContains(t, got, noIP)
	require.Equal(t, got[a], got[b], "one IP, one class")
	require.NotEqual(t, got[a], got[c])

	_, other := cipher.GenerateKeyPair()
	require.NotEqual(t, got[a], readIPClasses(dir, ipClassKey(other))[a], "classes depend on the server's key")
}
