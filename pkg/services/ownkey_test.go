package services

import (
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestOwnKey(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	inline, err := json.Marshal(map[string]string{"type": "x", "secret_key": sk.Hex()})
	require.NoError(t, err)
	_, got, ok, err := OwnKey(inline)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, pk, got)

	kf := filepath.Join(t.TempDir(), "x.key")
	fromFile, err := json.Marshal(map[string]string{"type": "x", "keyfile": kf})
	require.NoError(t, err)
	resolved, filePK, ok, err := OwnKey(fromFile)
	require.NoError(t, err)
	require.True(t, ok, "a keyfile names a key, generated on first use")
	_, again, _, err := OwnKey(fromFile)
	require.NoError(t, err)
	require.Equal(t, filePK, again, "the same keyfile gives the same key")
	require.Contains(t, string(resolved), filePK.Hex())

	_, _, ok, err = OwnKey(json.RawMessage(`{"type":"x"}`))
	require.NoError(t, err)
	require.False(t, ok)
}
