// Package api internal/dmsg-discovery/api/get_available_servers_test.go
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/discmetrics"
	store2 "github.com/skycoin/skywire/internal/dmsg-discovery/store"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestGetAvailableServers(t *testing.T) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair([]byte(`test`))
	require.NoError(t, err)

	ephemeralPk1, ephemeralSk1, err := cipher.GenerateDeterministicKeyPair([]byte(`test ephemeral 1`))
	require.NoError(t, err)

	ephemeralPk2, ephemeralSk2, err := cipher.GenerateDeterministicKeyPair([]byte(`test ephemeral 2`))
	require.NoError(t, err)

	baseEntry := disc.Entry{
		Static:    pk,
		Timestamp: time.Now().Unix(),
		Client:    &disc.Client{},
		Server: &disc.Server{
			Address:           "localhost:8080",
			AvailableSessions: 3,
		},
		Version:  "0",
		Sequence: 1,
	}

	cases := []struct {
		name               string
		endpoint           string
		method             string
		status             int
		databaseAndEntries func(*testing.T) (store2.Storer, []*disc.Entry)
		responseIsError    bool
		errorMessage       disc.HTTPMessage
	}{
		{
			name:            "get 3 server entries",
			endpoint:        "/dmsg-discovery/available_servers",
			method:          http.MethodGet,
			status:          http.StatusOK,
			responseIsError: false,
			databaseAndEntries: func(t *testing.T) (store2.Storer, []*disc.Entry) {
				// deep copy of Keys, if not they will overwrite the same underlying key struct
				var entry1, entry2, entry3 disc.Entry
				disc.Copy(&entry1, &baseEntry)
				disc.Copy(&entry2, &baseEntry)
				disc.Copy(&entry3, &baseEntry)

				err := entry1.Sign(sk)
				require.NoError(t, err)

				entry2.Static = ephemeralPk1
				err = entry2.Sign(ephemeralSk1)
				require.NoError(t, err)

				entry3.Static = ephemeralPk2
				err = entry3.Sign(ephemeralSk2)
				require.NoError(t, err)

				ctx := context.TODO()
				log := logging.MustGetLogger("test")
				db, err := store2.NewStore(ctx, "mock", nil, log)
				require.NoError(t, err)

				err = db.SetEntry(context.Background(), &entry1, time.Duration(0))
				require.NoError(t, err)

				err = db.SetEntry(context.Background(), &entry2, time.Duration(0))
				require.NoError(t, err)

				err = db.SetEntry(context.Background(), &entry3, time.Duration(0))
				require.NoError(t, err)

				return db, []*disc.Entry{&entry1, &entry2, &entry3}
			},
		},
		{
			name:            "get no entries",
			endpoint:        "/dmsg-discovery/available_servers",
			method:          http.MethodGet,
			status:          http.StatusNotFound,
			responseIsError: true,
			databaseAndEntries: func(t *testing.T) (store2.Storer, []*disc.Entry) {

				ctx := context.TODO()
				log := logging.MustGetLogger("test")
				db, err := store2.NewStore(ctx, "mock", nil, log)
				require.NoError(t, err)

				return db, []*disc.Entry{}
			},
			errorMessage: disc.HTTPMessage{
				Message: disc.ErrNoAvailableServers.Error(),
				Code:    http.StatusNotFound,
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			db, entries := tc.databaseAndEntries(t)

			api := New(nil, db, discmetrics.NewEmpty(), true, false, true, "")
			req, err := http.NewRequest(tc.method, tc.endpoint, nil)
			require.NoError(t, err)

			rr := httptest.NewRecorder()
			api.Handler.ServeHTTP(rr, req)

			status := rr.Code
			require.Equal(t, tc.status, status, "case: %s, handler returned wrong status code: got `%v` want `%v`",
				tc.name, status, tc.status)

			if !tc.responseIsError {
				var resEntries []*disc.Entry
				err = json.NewDecoder(rr.Body).Decode(&resEntries)
				require.NoError(t, err)

				sort.Slice(entries, func(i, j int) bool { return entries[i].Static.String() > entries[j].Static.String() })
				sort.Slice(resEntries, func(i, j int) bool { return resEntries[i].Static.String() > resEntries[j].Static.String() })
				require.EqualValues(t, entries, resEntries)
			} else {
				var resMessage disc.HTTPMessage
				err = json.NewDecoder(rr.Body).Decode(&resMessage)
				require.NoError(t, err)

				require.Equal(t, tc.errorMessage, resMessage)
			}
		})
	}
}
