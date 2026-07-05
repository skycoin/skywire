// Package servicedisc pkg/servicedisc/auth_test.go: unit tests for the
// authenticated paths (RegisterEntry/postEntry, DeleteEntry, Register) using
// a fake service-discovery server that speaks the httpauthclient nonce
// handshake.
package servicedisc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// resetAuthClient clears the package-level singleton so each test builds a
// fresh auth client pointed at its own test server.
func resetAuthClient() {
	authClientMu.Lock()
	authClient = nil
	authClientMu.Unlock()
}

// authClientFor returns an HTTPClient (non-visor type, so RegisterEntry skips
// the local-IP lookup) wired to the given discovery address.
func authClientFor(disc string, hc *http.Client) *HTTPClient {
	resetAuthClient()
	pk, sk := cipher.GenerateKeyPair()
	mLog := logging.NewMasterLogger()
	return NewClient(mLog.PackageLogger("test"), mLog, Config{
		Type:     ServiceTypeVPN,
		PK:       pk,
		SK:       sk,
		Port:     9999,
		DiscAddr: disc,
	}, hc, "")
}

// nonceHandler answers the auth handshake; the caller supplies the handler for
// everything else (the actual /api/services traffic).
func nonceHandler(rest http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/security/nonces/") {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"next_nonce":0}`)) //nolint
			return
		}
		rest(w, r)
	}
}

func TestRegisterEntry_Success(t *testing.T) {
	var posted bool
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodPost, r.Method)
		require.Equal(t, "/api/services", r.URL.Path)
		posted = true

		pk, _ := cipher.GenerateKeyPair()
		w.Header().Set("Content-Type", "application/json")
		// Encode via pointer so SWAddr's pointer-receiver MarshalText runs
		// (a value would serialize the address as a raw byte array).
		require.NoError(t, json.NewEncoder(w).Encode(&Service{
			Addr: NewSWAddr(pk, 9999), Type: ServiceTypeVPN, Version: "v1",
		}))
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	require.NoError(t, c.RegisterEntry(context.Background()))
	require.True(t, posted)
	require.Equal(t, "v1", c.entry.Version)
}

func TestRegisterEntry_AuthFails(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		// Fail the nonce handshake.
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	require.Error(t, c.RegisterEntry(context.Background()))
}

func TestPostEntry_ServerError(t *testing.T) {
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"rejected"}`)) //nolint
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	err := c.RegisterEntry(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "rejected")
}

func TestDeleteEntry_Success(t *testing.T) {
	var deleted bool
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, http.MethodDelete, r.Method)
		require.True(t, strings.HasPrefix(r.URL.Path, "/api/services/"))
		deleted = true
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	require.NoError(t, c.DeleteEntry(context.Background()))
	require.True(t, deleted)
}

func TestDeleteEntry_ServerError(t *testing.T) {
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"code":404,"error":"gone"}`)) //nolint
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	err := c.DeleteEntry(context.Background())
	require.Error(t, err)

	var hErr *HTTPError
	require.ErrorAs(t, err, &hErr)
	require.Equal(t, "gone", hErr.Err)
}

func TestRegister_Success(t *testing.T) {
	srv := httptest.NewServer(nonceHandler(func(w http.ResponseWriter, _ *http.Request) {
		pk, _ := cipher.GenerateKeyPair()
		require.NoError(t, json.NewEncoder(w).Encode(&Service{Addr: NewSWAddr(pk, 9999), Type: ServiceTypeVPN}))
	}))
	defer srv.Close()

	c := authClientFor(srv.URL, srv.Client())
	require.NoError(t, c.Register(context.Background()))
}
