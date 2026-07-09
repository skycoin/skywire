// Package api pkg/dmsg/discovery/api/handlers_coverage_test.go
package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	store2 "github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/logging"
)

// newTestStore returns the package's real in-memory mock store.
func newTestStore(t *testing.T) store2.Storer {
	t.Helper()
	db, err := store2.NewStore(context.Background(), "mock", nil, logging.MustGetLogger("test"))
	require.NoError(t, err)
	return db
}

// newAPI wraps store in an API with test mode on and load-testing off (so
// signature checks run, matching production).
func newAPI(db store2.Storer) *API {
	return New(nil, db, metrics.NewEmpty(), true, false, true, "", "", 0)
}

// signedClientEntry builds and signs a client entry delegating to the given
// servers. Returns the entry and its client PK.
func signedClientEntry(t *testing.T, delegated ...cipher.PubKey) (*disc.Entry, cipher.PubKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	e := &disc.Entry{
		Static:    pk,
		Timestamp: time.Now().UnixNano(),
		Client:    &disc.Client{DelegatedServers: delegated},
		Version:   "0",
		Sequence:  0,
	}
	require.NoError(t, e.Sign(sk))
	return e, pk
}

func do(api *API, method, target string, body []byte) *httptest.ResponseRecorder {
	var r *http.Request
	if body == nil {
		r = httptest.NewRequest(method, target, nil)
	} else {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	}
	r.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	api.Handler.ServeHTTP(rr, r)
	return rr
}

func TestDelEntry(t *testing.T) {
	t.Run("deletes a signed entry", func(t *testing.T) {
		db := newTestStore(t)
		entry, pk := signedClientEntry(t)
		require.NoError(t, db.SetEntry(context.Background(), entry, 0))
		api := newAPI(db)

		rr := do(api, http.MethodDelete, "/dmsg-discovery/entry", []byte(toJSON(t, entry)))
		require.Equal(t, http.StatusOK, rr.Code)
		var msg disc.HTTPMessage
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&msg))
		require.Equal(t, disc.MsgEntryDeleted, msg)

		// The entry is really gone.
		_, err := db.Entry(context.Background(), pk)
		require.Equal(t, disc.ErrKeyNotFound, err)
	})

	t.Run("tampered entry is unauthorized", func(t *testing.T) {
		db := newTestStore(t)
		entry, _ := signedClientEntry(t)
		require.NoError(t, db.SetEntry(context.Background(), entry, 0))
		api := newAPI(db)

		entry.Timestamp += 1000 // invalidate the signature after signing
		rr := do(api, http.MethodDelete, "/dmsg-discovery/entry", []byte(toJSON(t, entry)))
		require.Equal(t, http.StatusUnauthorized, rr.Code)
	})
}

func TestAllVisorEntries(t *testing.T) {
	db := newTestStore(t)
	entry, _ := signedClientEntry(t)
	require.NoError(t, db.SetEntry(context.Background(), entry, 0))
	api := newAPI(db)

	rr := do(api, http.MethodGet, "/dmsg-discovery/visorEntries", nil)
	require.Equal(t, http.StatusOK, rr.Code)
	var entries []string
	require.NoError(t, json.NewDecoder(rr.Body).Decode(&entries))
	require.NotEmpty(t, entries)
}

func TestClientsByServerEndpoints(t *testing.T) {
	serverPK, _ := cipher.GenerateKeyPair()
	db := newTestStore(t)
	entry, clientPK := signedClientEntry(t, serverPK)
	require.NoError(t, db.SetEntry(context.Background(), entry, 0))
	api := newAPI(db)

	t.Run("allClientsByServer groups clients under their server", func(t *testing.T) {
		rr := do(api, http.MethodGet, "/dmsg-discovery/servers/clients", nil)
		require.Equal(t, http.StatusOK, rr.Code)
		var byServer map[string][]string
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&byServer))
		require.Contains(t, byServer[serverPK.Hex()], clientPK.Hex())
	})

	t.Run("clientsByServer lists clients for a specific server", func(t *testing.T) {
		rr := do(api, http.MethodGet, "/dmsg-discovery/server/"+serverPK.Hex()+"/clients", nil)
		require.Equal(t, http.StatusOK, rr.Code)
		var clients []string
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&clients))
		require.Equal(t, []string{clientPK.Hex()}, clients)
	})

	t.Run("clientsByServer rejects a malformed server PK", func(t *testing.T) {
		rr := do(api, http.MethodGet, "/dmsg-discovery/server/not-a-pk/clients", nil)
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

// summariesStore wraps the real store, overriding GetAllVisorSummaries so the
// uptimes handlers have data to return and filter (the mock returns none).
type summariesStore struct {
	store2.Storer
	summaries []store2.VisorSummary
}

func (s summariesStore) GetAllVisorSummaries(_ context.Context, _, _ bool) ([]store2.VisorSummary, error) {
	return s.summaries, nil
}

func TestUptimesEndpoints(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	db := summariesStore{
		Storer:    newTestStore(t),
		summaries: []store2.VisorSummary{{PK: pk1, Online: true}, {PK: pk2}},
	}
	api := newAPI(db)

	t.Run("GET returns all summaries", func(t *testing.T) {
		rr := do(api, http.MethodGet, "/uptimes", nil)
		require.Equal(t, http.StatusOK, rr.Code)
		var got []store2.VisorSummary
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
		require.Len(t, got, 2)
	})

	t.Run("GET filters by visors param", func(t *testing.T) {
		rr := do(api, http.MethodGet, "/uptimes?visors="+pk1.Hex(), nil)
		require.Equal(t, http.StatusOK, rr.Code)
		var got []store2.VisorSummary
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
		require.Len(t, got, 1)
		require.Equal(t, pk1.Hex(), got[0].PK.Hex())
	})

	t.Run("POST filters by pks body", func(t *testing.T) {
		body := []byte(`{"pks":["` + pk2.Hex() + `"]}`)
		rr := do(api, http.MethodPost, "/uptimes", body)
		require.Equal(t, http.StatusOK, rr.Code)
		var got []store2.VisorSummary
		require.NoError(t, json.NewDecoder(rr.Body).Decode(&got))
		require.Len(t, got, 1)
		require.Equal(t, pk2.Hex(), got[0].PK.Hex())
	})

	t.Run("POST rejects invalid JSON", func(t *testing.T) {
		rr := do(api, http.MethodPost, "/uptimes", []byte("{not json"))
		require.Equal(t, http.StatusBadRequest, rr.Code)
	})
}

func TestDeregisterEntry(t *testing.T) {
	t.Run("unauthorized without a whitelisted NM key", func(t *testing.T) {
		api := newAPI(newTestStore(t))
		rr := do(api, http.MethodDelete, "/dmsg-discovery/deregister", nil)
		require.Equal(t, http.StatusForbidden, rr.Code)
	})

	t.Run("whitelisted NM key deletes the listed entries", func(t *testing.T) {
		nmPK, nmSK := cipher.GenerateKeyPair()
		WhitelistPKs.Set(nmPK.Hex())
		t.Cleanup(func() { delete(WhitelistPKs, nmPK.Hex()) })
		sig, err := cipher.SignPayload([]byte(nmPK.Hex()), nmSK)
		require.NoError(t, err)

		db := newTestStore(t)
		entry, deadPK := signedClientEntry(t)
		require.NoError(t, db.SetEntry(context.Background(), entry, 0))
		api := newAPI(db)

		body := []byte(`["` + deadPK.Hex() + `"]`)
		r := httptest.NewRequest(http.MethodDelete, "/dmsg-discovery/deregister", bytes.NewReader(body))
		r.Header.Set("NM-PK", nmPK.Hex())
		r.Header.Set("NM-Sign", sig.Hex())
		rr := httptest.NewRecorder()
		api.Handler.ServeHTTP(rr, r)

		require.Equal(t, http.StatusOK, rr.Code)
		_, err = db.Entry(context.Background(), deadPK)
		require.Equal(t, disc.ErrKeyNotFound, err)
	})
}
