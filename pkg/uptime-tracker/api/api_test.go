package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/geo"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/storeconfig"

	"github.com/skycoin/skywire/internal/utmetrics"
	"github.com/skycoin/skywire/pkg/uptime-tracker/store"
)

var testPubKey, testSec = cipher.GenerateKeyPair()

var geoFunc = func(ip net.IP) (*geo.LocationData, error) {
	wantIP := net.IPv4(127, 0, 0, 1)
	if wantIP.Equal(ip) {
		return &geo.LocationData{
			Lat: 1,
			Lon: 1,
		}, nil
	}

	return nil, errors.New("unexpected ip")
}

func TestHandleUptimes(t *testing.T) {
	mock := store.NewMemoryStore()
	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	pk, _ := cipher.GenerateKeyPair()

	const iterations = 15
	for i := 0; i < iterations; i++ {
		require.NoError(t, mock.UpdateUptime(pk.String(), "127.0.0.1", ""))
	}

	w := httptest.NewRecorder()

	body := bytes.NewBuffer(make([]byte, 0))
	r := httptest.NewRequest(http.MethodGet, "/uptimes", body)
	r.Header = validHeaders(t, body.Bytes())
	api.ServeHTTP(w, r)

	assert.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp store.UptimeResponse
	require.NoError(t, json.NewDecoder(bytes.NewBuffer(w.Body.Bytes())).Decode(&resp))

	require.Len(t, resp, 1)
	assert.Equal(t, pk.String(), resp[0].Key)

	assert.True(t, resp[0].Online)
}

func TestAPI_handleUpdate(t *testing.T) {
	mock := store.NewMemoryStore()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	t.Run("StatusOK", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusOK, w.Code)
	})

	t.Run("StatusUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "{\"error\":{\"message\":\"SW-Public missing\",\"code\":401}}\n", w.Body.String())
	})
}

func TestApi_UpdateRemovedMethod(t *testing.T) {
	mock := store.NewMemoryStore()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, storeconfig.Config{Type: storeconfig.Memory}, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, geoFunc, false, false,
		utmetrics.NewEmpty(), 0, "", "")

	t.Run("StatusGone", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("StatusGonev2", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v2/update", body)
		r.Header = validHeaders(t, body.Bytes())
		r.Header.Add("X-Forwarded-For", "127.0.0.1")
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusGone, w.Code)
	})

	t.Run("StatusUnauthorized", func(t *testing.T) {
		w := httptest.NewRecorder()
		body := bytes.NewBuffer(make([]byte, 0))
		r := httptest.NewRequest(http.MethodGet, "/v4/update", body)
		api.ServeHTTP(w, r)

		assert.Equal(t, http.StatusUnauthorized, w.Code)
		assert.Equal(t, "{\"error\":{\"message\":\"SW-Public missing\",\"code\":401}}\n", w.Body.String())
	})
}

// validHeaders returns a valid set of headers
func validHeaders(t *testing.T, payload []byte) http.Header {
	nonce := httpauth.Nonce(0)
	sig, err := httpauth.Sign(payload, nonce, testSec)
	require.NoError(t, err)

	hdr := http.Header{}
	hdr.Set("SW-Public", testPubKey.Hex())
	hdr.Set("SW-Sig", sig.Hex())
	hdr.Set("SW-Nonce", nonce.String())

	return hdr
}
