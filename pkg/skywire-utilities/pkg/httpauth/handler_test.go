// Package httpauth pkg/httpauth/handler_test.go
package httpauth

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/httputil"
	"github.com/skycoin/skywire-utilities/pkg/storeconfig"
)

var testPubKey, testSec = cipher.GenerateKeyPair()

// validHeaders returns a valid set of headers
func validHeaders(t *testing.T, payload []byte) http.Header {
	nonce := Nonce(0)
	sig, err := Sign(payload, nonce, testSec)
	require.NoError(t, err)

	hdr := http.Header{}
	hdr.Set("SW-Public", testPubKey.Hex())
	hdr.Set("SW-Sig", sig.Hex())
	hdr.Set("SW-Nonce", nonce.String())

	return hdr
}

func validHeadersWithNonce(t *testing.T, nonce Nonce, payload []byte) http.Header {
	sig, err := Sign(payload, nonce, testSec)
	require.NoError(t, err)

	hdr := http.Header{}
	hdr.Set("SW-Public", testPubKey.Hex())
	hdr.Set("SW-Sig", sig.Hex())
	hdr.Set("SW-Nonce", nonce.String())

	return hdr
}

func invalidHeaders(t *testing.T, payload []byte) http.Header {
	_, invalidSec := cipher.GenerateKeyPair()
	nonce := Nonce(0)
	sig, err := Sign(payload, nonce, invalidSec)
	require.NoError(t, err)

	hdr := http.Header{}
	hdr.Set("SW-Public", testPubKey.Hex())
	hdr.Set("SW-Sig", sig.Hex())
	hdr.Set("SW-Nonce", nonce.String())

	return hdr
}

func TestServer_Wrap(t *testing.T) {
	storeConfig := storeconfig.Config{Type: storeconfig.Memory}
	ctx := context.TODO()
	mock, err := NewNonceStore(ctx, storeConfig, "")
	require.NoError(t, err)

	t.Run("Without headers", func(t *testing.T) {
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", nil)

		m := chi.NewRouter()

		m.Use(MakeMiddleware(mock))
		m.Post("/foo", func(writer http.ResponseWriter, request *http.Request) {
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		})

		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
		nonce, err := mock.Nonce(context.TODO(), testPubKey)
		require.NoError(t, err)

		assert.Equal(t, Nonce(0), nonce)
	})

	t.Run("Context has verified pubkey", func(t *testing.T) {
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", bytes.NewReader([]byte("hi")))
		r.Header = validHeaders(t, []byte("hi"))

		handler := func(writer http.ResponseWriter, request *http.Request) {
			pk, ok := request.Context().Value(ContextAuthKey).(cipher.PubKey)
			if !ok {
				httputil.WriteJSON(writer, request, http.StatusBadRequest, "")
			}
			if pk != testPubKey {
				httputil.WriteJSON(writer, request, http.StatusBadRequest, "")
			}
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		}

		m := chi.NewRouter()
		m.Use(MakeMiddleware(mock))
		m.Post("/foo", handler)
		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		_, err := mock.Nonce(context.TODO(), testPubKey)
		require.NoError(t, err)
	})

	t.Run("Valid", func(t *testing.T) {
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", bytes.NewReader([]byte("hi")))
		r.Header = validHeaders(t, []byte("hi"))

		handler := func(writer http.ResponseWriter, request *http.Request) {
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		}

		m := chi.NewRouter()
		m.Use(MakeMiddleware(mock))
		m.Post("/foo", handler)
		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
		nonce, err := mock.Nonce(context.TODO(), testPubKey)
		require.NoError(t, err)

		assert.Equal(t, Nonce(1), nonce)
	})

	t.Run("Valid with nonzero nonce", func(t *testing.T) {
		_, err := mock.IncrementNonce(context.TODO(), testPubKey)
		require.NoError(t, err)
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", bytes.NewReader([]byte("foo")))
		r.Header = validHeadersWithNonce(t, Nonce(1), []byte("foo"))

		handler := func(writer http.ResponseWriter, request *http.Request) {
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		}

		m := chi.NewRouter()
		m.Use(MakeMiddleware(mock))
		m.Post("/foo", handler)
		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code, w.Body.String())
	})

	t.Run("Invalid with nonzero nonce", func(t *testing.T) {
		_, err := mock.IncrementNonce(context.TODO(), testPubKey)
		require.NoError(t, err)
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()

		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", nil)
		r.Header = validHeadersWithNonce(t, Nonce(3), nil)

		handler := func(writer http.ResponseWriter, request *http.Request) {
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		}

		m := chi.NewRouter()
		m.Use(MakeMiddleware(mock))
		m.Post("/foo", handler)
		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	})

	t.Run("Invalid signature", func(t *testing.T) {
		defer func() {
			storeConfig := storeconfig.Config{Type: storeconfig.Memory}
			nmock, err := NewNonceStore(ctx, storeConfig, "")
			require.NoError(t, err)

			mock = nmock
		}()
		w := httptest.NewRecorder()
		r := httptest.NewRequest("POST", "/foo", nil)
		r.Header = invalidHeaders(t, nil)

		handler := func(writer http.ResponseWriter, request *http.Request) {
			httputil.WriteJSON(writer, request, http.StatusOK, "")
		}

		m := chi.NewRouter()
		m.Use(MakeMiddleware(mock))
		m.Post("/foo", handler)
		m.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code, w.Body.String())
	})
}

func TestAuthFormat(t *testing.T) {
	headers := []string{"SW-Public", "SW-Sig", "SW-Nonce"}
	for _, header := range headers {
		header := header
		t.Run(header+"-IsMissing", func(t *testing.T) {
			hdr := validHeaders(t, nil)
			hdr.Del(header)

			_, err := AuthFromHeaders(hdr, true)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), header)
		})
	}

	t.Run("NonceFormat", func(t *testing.T) {
		nonces := []string{"not_a_number", "-1", "0x0"}
		hdr := validHeaders(t, nil)
		for _, n := range nonces {
			hdr.Set("SW-Nonce", n)
			_, err := AuthFromHeaders(hdr, true)
			assert.Error(t, err)
			assert.Contains(t, err.Error(), "SW-Nonce: invalid syntax")
		}
	})
}

func TestAuthSignatureVerification(t *testing.T) {
	nonce := Nonce(0xdeadbeef)
	payload := []byte("dead beed")

	sig, err := Sign(payload, nonce, testSec)
	require.NoError(t, err)

	auth := &Auth{
		Key:   testPubKey,
		Nonce: nonce,
		Sig:   sig,
	}

	assert.NoError(t, auth.Verify(payload))
	assert.Error(t, auth.Verify([]byte("other payload")), "Validate should return an error for this payload")
}

func TestSignatureVerification(t *testing.T) {
	pub, sec := cipher.GenerateKeyPair()
	payload := []byte("payload to sign")
	nonce := Nonce(0xff)

	sig, err := Sign(payload, nonce, sec)
	require.NoError(t, err)
	require.NoError(t, Verify(payload, nonce, pub, sig))
	require.Error(t, Verify(payload, nonce+1, pub, sig))
}
