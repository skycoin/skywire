// Package servicedisc pkg/servicedisc/client_test.go: unit tests for the
// HTTPClient query-building / Services path and the Configured/NewClient
// helpers.
package servicedisc

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

func testClient(disc string, hc *http.Client) *HTTPClient {
	pk, sk := cipher.GenerateKeyPair()
	mLog := logging.NewMasterLogger()
	return NewClient(mLog.PackageLogger("test"), mLog, Config{
		Type:     ServiceTypeVisor,
		PK:       pk,
		SK:       sk,
		Port:     5505,
		DiscAddr: disc,
	}, hc, "")
}

func TestNewClient_InitializesEntry(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	mLog := logging.NewMasterLogger()
	c := NewClient(mLog.PackageLogger("test"), mLog, Config{
		Type:     ServiceTypeVPN,
		PK:       pk,
		SK:       sk,
		Port:     44,
		DiscAddr: "http://disc.local",
	}, &http.Client{}, "1.2.3.4")

	require.Equal(t, ServiceTypeVPN, c.entry.Type)
	require.Equal(t, NewSWAddr(pk, 44), c.entry.Addr)
	require.Equal(t, "1.2.3.4", c.clientPublicIP)
}

func TestConfigured(t *testing.T) {
	var nilClient *HTTPClient
	require.False(t, nilClient.Configured())

	require.False(t, testClient("http://disc.local", nil).Configured())
	require.False(t, testClient("", &http.Client{}).Configured())
	require.True(t, testClient("http://disc.local", &http.Client{}).Configured())
}

func TestAddr(t *testing.T) {
	c := testClient("http://disc.local", &http.Client{})

	t.Run("all params", func(t *testing.T) {
		got, err := c.addr("/api/services", "vpn", "v1.0", "US", 5)
		require.NoError(t, err)
		require.Contains(t, got, "http://disc.local/api/services?")
		require.Contains(t, got, "type=vpn")
		require.Contains(t, got, "quantity=5")
		require.Contains(t, got, "version=v1.0")
		require.Contains(t, got, "country=US")
	})

	t.Run("quantity of 1 is omitted", func(t *testing.T) {
		got, err := c.addr("/api/services", "vpn", "", "", 1)
		require.NoError(t, err)
		require.NotContains(t, got, "quantity")
		require.NotContains(t, got, "version")
		require.NotContains(t, got, "country")
	})

	t.Run("invalid disc addr", func(t *testing.T) {
		bad := testClient("http://[::1]:namedport", &http.Client{})
		_, err := bad.addr("/api/services", "", "", "", 1)
		require.Error(t, err)
		require.Contains(t, err.Error(), "invalid service discovery address")
	})
}

func TestServices_NoClient(t *testing.T) {
	c := testClient("http://disc.local", nil)
	_, err := c.Services(context.Background(), 1, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no HTTP client configured")
}

func TestServices_Success(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	want := []Service{{Addr: NewSWAddr(pk, 80), Type: ServiceTypeVisor}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/services", r.URL.Path)
		require.Equal(t, ServiceTypeVisor, r.URL.Query().Get("type"))
		w.Header().Set("Content-Type", "application/json")
		require.NoError(t, json.NewEncoder(w).Encode(want))
	}))
	defer srv.Close()

	c := testClient(srv.URL, srv.Client())
	got, err := c.Services(context.Background(), 1, "", "")
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, ServiceTypeVisor, got[0].Type)
}

func TestServices_Empty(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer srv.Close()

	c := testClient(srv.URL, srv.Client())
	_, err := c.Services(context.Background(), 1, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "no service of type")
}

func TestServices_HTTPErrorJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"code":500,"error":"boom"}`))
	}))
	defer srv.Close()

	c := testClient(srv.URL, srv.Client())
	_, err := c.Services(context.Background(), 1, "", "")
	require.Error(t, err)

	var hErr *HTTPError
	require.ErrorAs(t, err, &hErr)
	require.Equal(t, "boom", hErr.Err)
}

func TestServices_HTTPErrorNonJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	c := testClient(srv.URL, srv.Client())
	_, err := c.Services(context.Background(), 1, "", "")
	require.Error(t, err)
	require.Contains(t, err.Error(), "service discovery error")
}

func TestServices_InvalidDiscAddr(t *testing.T) {
	c := testClient("http://[::1]:namedport", &http.Client{})
	_, err := c.Services(context.Background(), 1, "", "")
	require.Error(t, err)
}
