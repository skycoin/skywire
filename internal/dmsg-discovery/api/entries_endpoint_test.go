// Package api internal/dmsg-discovery/api/entries_endpoint_test.go
package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/internal/discmetrics"
	store2 "github.com/skycoin/skywire/internal/dmsg-discovery/store"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/disc"
	"github.com/skycoin/skywire/pkg/logging"
)

func TestEntriesEndpoint(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	baseEntry := disc.Entry{
		Static:    pk,
		Timestamp: time.Now().UnixNano(),
		Client:    &disc.Client{},
		Server: &disc.Server{
			Address:           "localhost:8080",
			AvailableSessions: 3,
		},
		Version:  "0",
		Sequence: 0,
	}

	cases := []struct {
		name            string
		method          string
		endpoint        string
		status          int
		contentType     string
		httpBody        string
		httpResponse    *disc.HTTPMessage
		responseIsEntry bool
		entry           disc.Entry
		entryPreHook    func(*testing.T, *disc.Entry, *string)
		storerPreHook   func(*testing.T, store2.Storer, *disc.Entry)
	}{
		{
			name:            "get entry",
			method:          http.MethodGet,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusOK,
			contentType:     "application/json",
			responseIsEntry: true,
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry, body *string) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
			storerPreHook: func(t *testing.T, s store2.Storer, e *disc.Entry) {
				err := s.SetEntry(context.Background(), e, time.Duration(0))
				require.NoError(t, err)
			},
		},
		{
			name:            "get not valid entry",
			method:          http.MethodGet,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusNotFound,
			contentType:     "application/json",
			responseIsEntry: false,
			httpResponse:    &disc.HTTPMessage{Code: http.StatusNotFound, Message: "entry of public key is not found"},
			entry:           baseEntry,
		},
		{
			name:            "set entry right",
			method:          http.MethodPost,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusOK,
			contentType:     "application/json",
			responseIsEntry: false,
			httpResponse:    &disc.HTTPMessage{Code: http.StatusOK, Message: "wrote a new entry"},
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry, body *string) {
				err := e.Sign(sk)
				require.NoError(t, err)
				*body = toJSON(t, e)
			},
		},
		{
			name:            "set entry iteration",
			method:          http.MethodPost,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusOK,
			contentType:     "application/json",
			responseIsEntry: false,
			httpResponse:    &disc.MsgEntryUpdated,
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry, body *string) {
				err := e.Sign(sk)
				require.NoError(t, err)
				newEntry := *e
				newEntry.Sequence = 1
				newEntry.Timestamp += 3
				err = newEntry.Sign(sk)
				require.NoError(t, err)
				*body = toJSON(t, &newEntry)
			},
			storerPreHook: func(t *testing.T, s store2.Storer, e *disc.Entry) {
				e.Sequence = 0
				err := e.Sign(sk)
				require.NoError(t, err)
				err = s.SetEntry(context.Background(), e, time.Duration(0))
				require.NoError(t, err)
			},
		},
		{
			name:            "set entry iteration wrong sequence",
			method:          http.MethodPost,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusUnprocessableEntity,
			contentType:     "application/json",
			responseIsEntry: false,
			httpResponse:    &disc.HTTPMessage{Code: http.StatusUnprocessableEntity, Message: disc.ErrValidationWrongSequence.Error()},
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry, body *string) {
				newEntry := *e
				newEntry.Sequence = 0
				newEntry.Timestamp += 3
				err := newEntry.Sign(sk)
				require.NoError(t, err)
				*body = toJSON(t, &newEntry)
			},
			storerPreHook: func(t *testing.T, s store2.Storer, e *disc.Entry) {
				e.Sequence = 1
				err := e.Sign(sk)
				require.NoError(t, err)
				err = s.SetEntry(context.Background(), e, time.Duration(0))
				require.NoError(t, err)
			},
		},
		{
			name:            "set entry iteration unauthorized",
			method:          http.MethodPost,
			endpoint:        fmt.Sprintf("/dmsg-discovery/entry/%s", pk),
			status:          http.StatusUnauthorized,
			contentType:     "application/json",
			responseIsEntry: false,
			httpResponse:    &disc.HTTPMessage{Code: http.StatusUnauthorized, Message: "invalid signature"},
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry, body *string) {
				err := e.Sign(sk)
				require.NoError(t, err)
				newEntry := *e
				err = newEntry.Sign(sk)
				require.NoError(t, err)
				newEntry.Timestamp += 3
				*body = toJSON(t, &newEntry)
			},
			storerPreHook: func(t *testing.T, s store2.Storer, e *disc.Entry) {
				e.Sequence = 0
				err := s.SetEntry(context.Background(), e, time.Duration(0))
				require.NoError(t, err)
			},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			if tc.entryPreHook != nil {
				tc.entryPreHook(t, &tc.entry, &tc.httpBody)
			}

			ctx := context.TODO()
			log := logging.MustGetLogger("test")
			dbMock, err := store2.NewStore(ctx, "mock", nil, log)
			require.NoError(t, err)

			if tc.storerPreHook != nil {
				tc.storerPreHook(t, dbMock, &tc.entry)
			}

			api := New(nil, dbMock, discmetrics.NewEmpty(), true, false, true, "")
			req, err := http.NewRequest(tc.method, tc.endpoint, bytes.NewBufferString(tc.httpBody))
			require.NoError(t, err)

			contentType := tc.contentType
			if contentType == "" {
				contentType = "application/json"
			}

			req.Header.Set("Content-Type", contentType)

			rr := httptest.NewRecorder()
			api.Handler.ServeHTTP(rr, req)

			status := rr.Code
			require.Equal(t, tc.status, status, "case: %s, handler for %s %s returned wrong status code: got `%v` want `%v`",
				tc.name, tc.method, tc.endpoint, status, tc.status)

			if tc.responseIsEntry {
				var resEntry disc.Entry
				err = json.NewDecoder(rr.Body).Decode(&resEntry)
				require.NoError(t, err)

				require.Equal(t, tc.entry, resEntry)
			} else {
				var resMessage disc.HTTPMessage
				err = json.NewDecoder(rr.Body).Decode(&resMessage)
				require.NoError(t, err)

				require.Equal(t, tc.httpResponse, &resMessage)
			}
		})
	}
}

func toJSON(t *testing.T, e *disc.Entry) string {
	b, err := json.Marshal(e)
	require.NoError(t, err)
	return string(b)
}
