// Package api pkg/dmsg/discovery/api/body_limit_test.go
package api

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
)

func TestBodyLimits(t *testing.T) {
	api := newAPI(newTestStore(t))
	oversize := bytes.Repeat([]byte(" "), maxBodyBytes+1)

	keys := func(n int) []byte {
		pks := make([]string, n)
		for i := range pks {
			pk, _ := cipher.GenerateKeyPair()
			pks[i] = pk.Hex()
		}
		b, err := json.Marshal(pks)
		require.NoError(t, err)
		return b
	}

	tests := []struct {
		name    string
		target  string
		body    []byte
		chunked bool
		want    int
	}{
		{"batch within limits", "/dmsg-discovery/entries/batch", keys(2), false, http.StatusOK},
		{"batch too many keys", "/dmsg-discovery/entries/batch", keys(maxBatchEntriesKeys + 1), false, http.StatusBadRequest},
		{"batch declared oversize", "/dmsg-discovery/entries/batch", oversize, false, http.StatusRequestEntityTooLarge},
		{"batch chunked oversize", "/dmsg-discovery/entries/batch", oversize, true, http.StatusRequestEntityTooLarge},
		{"entry chunked oversize", "/dmsg-discovery/entry/", oversize, true, http.StatusRequestEntityTooLarge},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, tc.target, bytes.NewReader(tc.body))
			if tc.chunked {
				r.ContentLength = -1
			}
			rr := httptest.NewRecorder()
			api.Handler.ServeHTTP(rr, r)
			require.Equal(t, tc.want, rr.Code, rr.Body.String())
		})
	}
}
