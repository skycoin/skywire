// Package api bind_quic_wt_test.go: unit tests for the QUIC and WebTransport
// bind handlers, which share bindForType with STCPR but store under their own
// transport type. Driven directly with injected auth (bypassing the httpauth
// signing middleware), mirroring TestBind.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/deployment/ar/store"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func TestBindQUICAndWT(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		tpType   types.Type
		handler  func(*API) http.HandlerFunc
	}{
		{"quic", "/bind/quic", types.QUIC, func(a *API) http.HandlerFunc { return a.bindQUIC }},
		{"wt", "/bind/wt", types.WT, func(a *API) http.HandlerFunc { return a.bindWT }},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pk, _ := cipher.GenerateKeyPair()

			t.Run("unauthorized", func(t *testing.T) {
				a := newTestAPI(t)
				rec := httptest.NewRecorder()
				tc.handler(a)(rec, authReq(t, http.MethodPost, tc.endpoint, []byte("{}"), nil, nil))
				require.Equal(t, http.StatusUnauthorized, rec.Code)
			})

			t.Run("remote addr not in local addresses -> 400", func(t *testing.T) {
				a := newTestAPI(t)
				body, _ := json.Marshal(addrresolver.LocalAddresses{Addresses: []string{"8.8.8.8"}}) //nolint:errcheck
				rec := httptest.NewRecorder()
				tc.handler(a)(rec, authReq(t, http.MethodPost, tc.endpoint, body, &pk, nil))
				require.Equal(t, http.StatusBadRequest, rec.Code)
			})

			t.Run("success binds under its own transport type", func(t *testing.T) {
				a := newTestAPI(t)
				body, _ := json.Marshal(addrresolver.LocalAddresses{ //nolint:errcheck
					Port:      "30000",
					Addresses: []string{"203.0.113.5"},
				})
				rec := httptest.NewRecorder()
				tc.handler(a)(rec, authReq(t, http.MethodPost, tc.endpoint, body, &pk, nil))
				require.Equal(t, http.StatusOK, rec.Code)

				// Stored and resolvable under the handler's transport type.
				vd, err := a.store.Resolve(context.Background(), tc.tpType, pk)
				require.NoError(t, err)
				require.Equal(t, "203.0.113.5", vd.RemoteAddr)
				require.Equal(t, "30000", vd.LocalAddresses.Port)

				// Type isolation: it must NOT leak into the STCPR bucket.
				_, err = a.store.Resolve(context.Background(), types.STCPR, pk)
				require.True(t, errors.Is(err, store.ErrNoEntry))
			})
		})
	}
}
