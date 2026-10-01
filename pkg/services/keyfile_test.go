package services

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestWithKeyFile(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	// A backslash in the path, as on Windows (C:\Users\...), must reach the
	// config escaped.
	path := filepath.Join(t.TempDir(), `x\Users`)
	quoted, err := json.Marshal(path)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(path, []byte(sk.Hex()+"\n"), 0o600))

	out, err := withKeyFile(json.RawMessage(`{"type":"route-finder","addr":":9092","keyfile":` + string(quoted) + `}`))
	require.NoError(t, err)
	var got Common
	require.NoError(t, json.Unmarshal(out, &got))
	require.Equal(t, sk, got.SecKey)
	require.Equal(t, pk, got.PubKey)
	require.Equal(t, ":9092", got.Addr, "other fields kept")

	// An inline secret_key wins; no keyfile is left alone.
	_, other := cipher.GenerateKeyPair()
	inline := json.RawMessage(`{"secret_key":"` + other.Hex() + `","keyfile":` + string(quoted) + `}`)
	out, err = withKeyFile(inline)
	require.NoError(t, err)
	require.JSONEq(t, string(inline), string(out))
	plain := json.RawMessage(`{"addr":":1"}`)
	out, err = withKeyFile(plain)
	require.NoError(t, err)
	require.Equal(t, string(plain), string(out))
}
