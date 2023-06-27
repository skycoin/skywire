package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/internal/tpdiscmetrics"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

type errorSetter interface {
	SetError(error)
}

var testPubKey, testSec = cipher.GenerateKeyPair()

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

func newTestEntry() *transport.Entry {
	pk1, _ := cipher.GenerateKeyPair()
	return &transport.Entry{
		ID:    uuid.New(),
		Edges: transport.SortEdges(testPubKey, pk1),
		Type:  "dmsg",
	}
}

func TestBadRequest(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	ctx := context.TODO()

	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	w := httptest.NewRecorder()
	r := httptest.NewRequest("POST", "/transports/", bytes.NewBufferString("not-a-json"))
	r.Header = validHeaders(t, []byte("not-a-json"))

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")
	api.ServeHTTP(w, r)

	resp := w.Result()
	assert.Equal(t, http.StatusBadRequest, w.Code, resp.Status)

	assert.NoError(t, resp.Body.Close())
}

func TestRegisterTransport(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	sEntry := &transport.SignedEntry{Entry: newTestEntry(), Signatures: [2]cipher.Sig{}}
	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")
	w := httptest.NewRecorder()

	body := bytes.NewBuffer(nil)
	require.NoError(t, json.NewEncoder(body).Encode([]*transport.SignedEntry{sEntry}))
	r := httptest.NewRequest("POST", "/transports/", body)
	r.Header = validHeaders(t, body.Bytes())
	api.ServeHTTP(w, r)

	assert.Equal(t, http.StatusCreated, w.Code, w.Body.String())

	var resp []*transport.SignedEntry
	require.NoError(t, json.NewDecoder(bytes.NewBuffer(w.Body.Bytes())).Decode(&resp))

	require.Len(t, resp, 1)
	assert.Equal(t, sEntry.Entry, resp[0].Entry)
}

func TestRegisterTimeout(t *testing.T) {
	const timeout = 10 * time.Millisecond

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	sEntry := &transport.SignedEntry{Entry: newTestEntry(), Signatures: [2]cipher.Sig{}}
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	// after this ctx's deadline will be exceeded
	time.Sleep(timeout * 2)

	mock.(errorSetter).SetError(ctx.Err())

	w := httptest.NewRecorder()
	body := bytes.NewBuffer(nil)
	require.NoError(t, json.NewEncoder(body).Encode([]*transport.SignedEntry{sEntry}))
	r := httptest.NewRequest("POST", "/transports/", body)
	r.Header = validHeaders(t, body.Bytes())

	api.ServeHTTP(w, r.WithContext(ctx))

	require.Equal(t, http.StatusRequestTimeout, w.Code, w.Body.String())
}

func TestGETTransportByID(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, sEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/transports/id:%s", entry.ID), nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp *transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	assert.Equal(t, entry, resp)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetTransportByID(ctx, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, found, entry)
	})
}

func TestDELETETransportByID(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)
	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}

	t.Run("can delete own transport", func(t *testing.T) {
		require.NoError(t, mock.RegisterTransport(ctx, sEntry))
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/transports/id:%s", entry.ID), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		_, err := mock.GetTransportByID(context.TODO(), entry.ID)
		require.Equal(t, store.ErrTransportNotFound, err)
	})

	t.Run("cannot delete transport of unauthorized visor", func(t *testing.T) {
		ctx := context.TODO()
		nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
		require.NoError(t, err)

		api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")
		pk1, _ := cipher.GenerateKeyPair()
		pk2, _ := cipher.GenerateKeyPair()
		otherVisorEntry := &transport.Entry{
			ID:    uuid.New(),
			Edges: transport.SortEdges(pk1, pk2),
			Type:  "dmsg",
		}
		sEntry := &transport.SignedEntry{Entry: otherVisorEntry, Signatures: [2]cipher.Sig{}}
		require.NoError(t, mock.RegisterTransport(ctx, sEntry))

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodDelete, fmt.Sprintf("/transports/id:%s", otherVisorEntry.ID), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())

		e, err := mock.GetTransportByID(context.TODO(), otherVisorEntry.ID)
		require.NoError(t, err)
		require.Equal(t, otherVisorEntry, e)
	})
}

func TestGETTransportByEdge(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	entry := newTestEntry()
	sEntry := &transport.SignedEntry{Entry: entry, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, sEntry))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/transports/edge:%s", entry.Edges[0]), nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp []*transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp, 1)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetTransportByID(ctx, entry.ID)
		require.NoError(t, err)
		assert.Equal(t, found, entry)
	})
}

func TestGETAllTransports(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	ctx := context.Background()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)

	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	entry1 := newTestEntry()
	sEntry1 := &transport.SignedEntry{Entry: entry1, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, sEntry1))

	entry2 := newTestEntry()
	sEntry2 := &transport.SignedEntry{Entry: entry2, Signatures: [2]cipher.Sig{}}
	require.NoError(t, mock.RegisterTransport(ctx, sEntry2))

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/all-transports", nil)
	r.Header = validHeaders(t, nil)
	api.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code, w.Body.String())

	var resp []*transport.Entry
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &resp))

	require.Len(t, resp, 2)

	t.Run("Persistence", func(t *testing.T) {
		found, err := mock.GetAllTransports(ctx)
		require.NoError(t, err)
		for i, f := range found {
			if f.ID == resp[i].ID {
				assert.EqualValues(t, *f, *resp[i])
			} else {
				j := (i + 1) % 2
				assert.EqualValues(t, *f, *resp[j])
			}
		}
	})
}

func TestGETIncrementingNonces(t *testing.T) {
	logger := &logging.Logger{}
	gormDB := &gorm.DB{}
	memoryStore := true
	nonceStoreConfig := storeconfig.Config{Type: storeconfig.Memory}

	mock, err := store.New(logger, gormDB, memoryStore)
	require.NoError(t, err)

	pubKey, _ := cipher.GenerateKeyPair()

	ctx := context.TODO()
	nonceMock, err := httpauth.NewNonceStore(ctx, nonceStoreConfig, "")
	require.NoError(t, err)
	api := New(nil, mock, nonceMock, false, tpdiscmetrics.NewEmpty(), "")

	t.Run("ValidRequest", func(t *testing.T) {
		const iterations = 0xFF

		for i := 0; i < iterations; i++ {
			_, err := nonceMock.IncrementNonce(context.Background(), pubKey)
			require.NoError(t, err)
		}

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/security/nonces/%s", pubKey), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r.WithContext(context.Background()))
		require.Equal(t, http.StatusOK, w.Code, w.Body.String())

		var resp httpauth.NextNonceResponse
		err := json.Unmarshal(w.Body.Bytes(), &resp)
		require.NoError(t, err)

		assert.Equal(t, pubKey, resp.Edge)
		assert.Equal(t, httpauth.Nonce(iterations), resp.NextNonce)
	})

	t.Run("StoreError", func(t *testing.T) {
		boom := errors.New("boom")
		nonceMock.(errorSetter).SetError(boom)
		defer mock.(errorSetter).SetError(nil)

		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/security/nonces/%s", pubKey), nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusInternalServerError, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), boom.Error())
	})

	t.Run("InvalidKey", func(t *testing.T) {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodGet, "/security/nonces/foo-bar", nil)
		r.Header = validHeaders(t, nil)
		api.ServeHTTP(w, r)
		require.Equal(t, http.StatusBadRequest, w.Code, w.Body.String())
		assert.Contains(t, w.Body.String(), "Invalid public key")
	})
}
