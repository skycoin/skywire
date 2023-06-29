// Package disc pkg/disc/client_mock_test.go
package disc_test

import (
	"context"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/disc"
)

func TestNewMockGetAvailableServers(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
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
		name                      string
		databaseAndEntriesPrehook func(*testing.T, disc.APIClient, *[]*disc.Entry)
		responseIsError           bool
		errorMessage              disc.HTTPMessage
	}{
		{
			name:            "get 3 server entries",
			responseIsError: false,
			databaseAndEntriesPrehook: func(t *testing.T, mockClient disc.APIClient, entries *[]*disc.Entry) {
				entry1 := baseEntry
				entry2 := baseEntry
				entry3 := baseEntry

				err := entry1.Sign(sk)
				require.NoError(t, err)
				err = mockClient.PostEntry(context.TODO(), &entry1)
				require.NoError(t, err)

				pk1, sk1 := cipher.GenerateKeyPair()
				entry2.Static = pk1
				err = entry2.Sign(sk1)
				require.NoError(t, err)
				err = mockClient.PostEntry(context.TODO(), &entry2)
				require.NoError(t, err)

				pk2, sk2 := cipher.GenerateKeyPair()
				entry3.Static = pk2
				err = entry3.Sign(sk2)
				require.NoError(t, err)
				err = mockClient.PostEntry(context.TODO(), &entry3)
				require.NoError(t, err)

				*entries = append(*entries, &entry1, &entry2, &entry3)
			},
		},
		{
			name:            "get no entries",
			responseIsError: false,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientServer := disc.NewMock(0)
			expectedEntries := make([]*disc.Entry, 0)

			if tc.databaseAndEntriesPrehook != nil {
				tc.databaseAndEntriesPrehook(t, clientServer, &expectedEntries)
			}

			entries, err := clientServer.AvailableServers(context.TODO())

			if !tc.responseIsError {
				assert.NoError(t, err)
				checkEqualEntries(t, entries, expectedEntries)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.errorMessage.String(), err.Error())
			}
		})
	}
}

func checkEqualEntries(t *testing.T, entries, expected []*disc.Entry) {
	require.Len(t, entries, len(expected))

	expectedMap := make(map[cipher.PubKey]*disc.Entry, len(expected))
	for _, expEntry := range expected {
		expectedMap[expEntry.Static] = expEntry
	}

	for _, entry := range entries {
		assert.Equal(t, expectedMap[entry.Static], entry)
	}
}

func TestNewMockEntriesEndpoint(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	baseEntry := newTestEntry(pk)

	cases := []struct {
		name            string
		httpResponse    disc.HTTPMessage
		publicKey       cipher.PubKey
		responseIsEntry bool
		entry           disc.Entry
		entryPreHook    func(*testing.T, *disc.Entry)
		storerPreHook   func(*testing.T, disc.APIClient, *disc.Entry)
	}{
		{
			name:            "get entry",
			publicKey:       pk,
			responseIsEntry: true,
			entry:           baseEntry,
			entryPreHook: func(t *testing.T, e *disc.Entry) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
			storerPreHook: func(t *testing.T, apiClient disc.APIClient, e *disc.Entry) {
				err := apiClient.PostEntry(context.TODO(), e)
				require.NoError(t, err)
			},
		},
		{
			name:            "get not valid entry",
			publicKey:       pk,
			responseIsEntry: false,
			httpResponse:    disc.HTTPMessage{Code: http.StatusNotFound, Message: disc.ErrKeyNotFound.Error()},
			entry:           baseEntry,
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientServer := disc.NewMock(0)

			if tc.entryPreHook != nil {
				tc.entryPreHook(t, &tc.entry)
			}

			if tc.storerPreHook != nil {
				tc.storerPreHook(t, clientServer, &tc.entry)
			}

			entry, err := clientServer.Entry(context.TODO(), tc.publicKey)
			if tc.responseIsEntry {
				assert.NoError(t, err)
				assert.Equal(t, &tc.entry, entry)
			} else {
				require.Error(t, err)
				assert.Equal(t, tc.httpResponse.String(), err.Error())
			}

		})
	}
}

func TestNewMockSetEntriesEndpoint(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	_, ephemeralSk1 := cipher.GenerateKeyPair()
	baseEntry := newTestEntry(pk)

	cases := []struct {
		name                string
		httpResponse        disc.HTTPMessage
		responseShouldError bool
		entryPreHook        func(t *testing.T, entry *disc.Entry)
		storerPreHook       func(*testing.T, disc.APIClient, *disc.Entry)
	}{

		{
			name:                "set entry right",
			responseShouldError: false,
			entryPreHook: func(t *testing.T, e *disc.Entry) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
		},
		{
			name:                "set entry iteration",
			responseShouldError: false,
			entryPreHook: func(t *testing.T, e *disc.Entry) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
			storerPreHook: func(t *testing.T, s disc.APIClient, e *disc.Entry) {
				var oldEntry disc.Entry
				disc.Copy(&oldEntry, e)
				fmt.Println(oldEntry.Static)
				oldEntry.Sequence = 0
				err := oldEntry.Sign(sk)
				require.NoError(t, err)
				err = s.PostEntry(context.TODO(), &oldEntry)
				require.NoError(t, err)
				e.Sequence = 1
				e.Timestamp += 3
				err = e.Sign(sk)
				require.NoError(t, err)
			},
		},
		{
			name:                "set entry iteration wrong sequence",
			responseShouldError: true,
			httpResponse:        disc.HTTPMessage{Code: http.StatusUnprocessableEntity, Message: disc.ErrValidationWrongSequence.Error()},
			entryPreHook: func(t *testing.T, e *disc.Entry) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
			storerPreHook: func(t *testing.T, s disc.APIClient, e *disc.Entry) {
				e.Sequence = 2
				err := s.PostEntry(context.TODO(), e)
				require.NoError(t, err)
			},
		},
		{
			name:                "set entry iteration unauthorized",
			responseShouldError: true,
			httpResponse:        disc.HTTPMessage{Code: http.StatusUnauthorized, Message: disc.ErrUnauthorized.Error()},
			entryPreHook: func(t *testing.T, e *disc.Entry) {
				err := e.Sign(sk)
				require.NoError(t, err)
			},
			storerPreHook: func(t *testing.T, s disc.APIClient, e *disc.Entry) {
				e.Sequence = 0
				err := s.PostEntry(context.TODO(), e)
				require.NoError(t, err)
				e.Signature = ""
				e.Sequence = 1
				e.Timestamp += 3
				err = e.Sign(ephemeralSk1)
				require.NoError(t, err)
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientServer := disc.NewMock(0)
			var entry disc.Entry
			disc.Copy(&entry, &baseEntry)

			if tc.entryPreHook != nil {
				tc.entryPreHook(t, &entry)
			}

			if tc.storerPreHook != nil {
				tc.storerPreHook(t, clientServer, &entry)
			}

			fmt.Println("key in: ", entry.Static)
			err := clientServer.PostEntry(context.TODO(), &entry)

			if tc.responseShouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func TestNewMockUpdateEntriesEndpoint(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	_, ephemeralSk1 := cipher.GenerateKeyPair()
	baseEntry := newTestEntry(pk)
	err := baseEntry.Sign(sk)
	require.NoError(t, err)

	cases := []struct {
		name                string
		secretKey           cipher.SecKey
		responseShouldError bool
		entryPreHook        func(entry *disc.Entry)
		storerPreHook       func(disc.APIClient, *disc.Entry)
	}{

		{
			name:                "update entry iteration",
			responseShouldError: false,
			secretKey:           sk,
			storerPreHook: func(apiClient disc.APIClient, e *disc.Entry) {
				e.Server.Address = "different one"
			},
		},
		{
			name:                "update entry unauthorized",
			responseShouldError: true,
			secretKey:           ephemeralSk1,
			storerPreHook: func(apiClient disc.APIClient, e *disc.Entry) {
				e.Server.Address = "different one"
			},
		},
		{
			name:                "update retries on wrong sequence",
			responseShouldError: false,
			secretKey:           sk,
			entryPreHook: func(entry *disc.Entry) {
				entry.Sequence = 3
			},
		},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			clientServer := disc.NewMock(0)
			err := clientServer.PostEntry(context.TODO(), &baseEntry)
			require.NoError(t, err)

			entry := baseEntry

			if tc.entryPreHook != nil {
				tc.entryPreHook(&entry)
			}

			if tc.storerPreHook != nil {
				tc.storerPreHook(clientServer, &entry)
			}

			err = clientServer.PutEntry(context.TODO(), tc.secretKey, &entry)

			if tc.responseShouldError {
				assert.Error(t, err)
			} else {
				assert.NoError(t, err)
			}

		})
	}
}

func TestNewMockUpdateEntrySequence(t *testing.T) {
	clientServer := disc.NewMock(0)
	pk, sk := cipher.GenerateKeyPair()
	entry := &disc.Entry{
		Sequence: 0,
		Static:   pk,
	}

	err := clientServer.PutEntry(context.TODO(), sk, entry)
	require.NoError(t, err)

	v1Entry, err := clientServer.Entry(context.TODO(), pk)
	require.NoError(t, err)

	err = clientServer.PutEntry(context.TODO(), sk, entry)
	require.NoError(t, err)

	v2Entry, err := clientServer.Entry(context.TODO(), pk)
	require.NoError(t, err)

	assert.NotEqual(t, v1Entry.Sequence, v2Entry.Sequence)
}

func newTestEntry(pk cipher.PubKey) disc.Entry {
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
	return baseEntry
}
