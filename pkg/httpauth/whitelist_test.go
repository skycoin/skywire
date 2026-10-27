package httpauth

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestWhitelistAuthorize(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	other, otherSK := cipher.GenerateKeyPair()
	wl := NewWhitelist()
	wl.Set(pk.Hex())

	signed := func(pk cipher.PubKey, sk cipher.SecKey, remote string) *http.Request {
		r := httptest.NewRequest(http.MethodDelete, "/deregister", nil)
		r.RemoteAddr = remote
		sig, err := cipher.SignPayload([]byte(pk.Hex()), sk)
		require.NoError(t, err)
		r.Header.Set("NM-PK", pk.Hex())
		r.Header.Set("NM-Sign", sig.Hex())
		return r
	}

	t.Run("dmsg caller on the list", func(t *testing.T) {
		r := httptest.NewRequest(http.MethodDelete, "/deregister", nil)
		r.RemoteAddr = pk.Hex() + ":80"
		got, _, err := wl.Authorize(r)
		require.NoError(t, err)
		require.Equal(t, pk, got)
	})
	t.Run("dmsg caller replaying a listed key's headers", func(t *testing.T) {
		r := signed(pk, sk, other.Hex()+":80")
		_, status, err := wl.Authorize(r)
		require.ErrorIs(t, err, ErrNotWhitelisted)
		require.Equal(t, http.StatusForbidden, status)
	})
	t.Run("http caller with valid headers", func(t *testing.T) {
		got, _, err := wl.Authorize(signed(pk, sk, "192.0.2.1:1234"))
		require.NoError(t, err)
		require.Equal(t, pk, got)
	})
	t.Run("http caller not on the list", func(t *testing.T) {
		_, status, err := wl.Authorize(signed(other, otherSK, "192.0.2.1:1234"))
		require.ErrorIs(t, err, ErrNotWhitelisted)
		require.Equal(t, http.StatusForbidden, status)
	})
	t.Run("http caller with a signature by another key", func(t *testing.T) {
		r := signed(pk, otherSK, "192.0.2.1:1234")
		_, status, err := wl.Authorize(r)
		require.ErrorIs(t, err, ErrNotWhitelisted)
		require.Equal(t, http.StatusForbidden, status)
	})
	t.Run("http caller with a malformed signature", func(t *testing.T) {
		r := signed(pk, sk, "192.0.2.1:1234")
		r.Header.Set("NM-Sign", "zz")
		_, status, err := wl.Authorize(r)
		require.ErrorIs(t, err, ErrBadWhitelistAuth)
		require.Equal(t, http.StatusBadRequest, status)
	})
}

func TestReadKeyList(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	r := httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`["`+pk.Hex()+`"]`))
	pks, err := ReadKeyList(r)
	require.NoError(t, err)
	require.Equal(t, []cipher.PubKey{pk}, pks)

	r = httptest.NewRequest(http.MethodDelete, "/", bytes.NewBufferString(`["nope"]`))
	_, err = ReadKeyList(r)
	require.Error(t, err)
}
