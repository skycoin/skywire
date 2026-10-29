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
	other, _ := cipher.GenerateKeyPair()
	wl := NewWhitelist()
	wl.Set(pk.Hex())

	req := func(remote string) *http.Request {
		r := httptest.NewRequest(http.MethodDelete, "/deregister", nil)
		r.RemoteAddr = remote
		return r
	}

	t.Run("dmsg caller on the list", func(t *testing.T) {
		got, _, err := wl.Authorize(req(pk.Hex() + ":80"))
		require.NoError(t, err)
		require.Equal(t, pk, got)
	})
	t.Run("dmsg caller not on the list", func(t *testing.T) {
		_, status, err := wl.Authorize(req(other.Hex() + ":80"))
		require.ErrorIs(t, err, ErrNotWhitelisted)
		require.Equal(t, http.StatusForbidden, status)
	})
	t.Run("plain HTTP is refused even with a listed key's signed headers", func(t *testing.T) {
		r := req("192.0.2.1:1234")
		sig, err := cipher.SignPayload([]byte(pk.Hex()), sk)
		require.NoError(t, err)
		r.Header.Set("NM-PK", pk.Hex())
		r.Header.Set("NM-Sign", sig.Hex())
		_, status, err := wl.Authorize(r)
		require.ErrorIs(t, err, ErrNotDmsg)
		require.Equal(t, http.StatusForbidden, status)
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
