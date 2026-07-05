// Package geo pkg/geo/geo_test.go: unit tests for the IP geolocation
// closure built by MakeIPDetails and the roundTwoDigits helper.
package geo

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// publicIP is a well-known public address so netutil.IsPublicIP passes.
var publicIP = net.ParseIP("8.8.8.8")

func TestRoundTwoDigits(t *testing.T) {
	require.Equal(t, 1.23, roundTwoDigits(1.234))
	require.Equal(t, 1.24, roundTwoDigits(1.235))
	require.Equal(t, -1.24, roundTwoDigits(-1.235))
	require.Equal(t, 0.0, roundTwoDigits(0))
}

func TestMakeIPDetails_NonPublicIP(t *testing.T) {
	fn := MakeIPDetails(logging.MustGetLogger("geo-test"), "http://unused.local")
	_, err := fn(net.ParseIP("192.168.1.10"))
	require.ErrorIs(t, err, ErrIPIsNotPublic)
}

func TestMakeIPDetails_NilLogger(t *testing.T) {
	// A nil logger must be replaced with a default one, not panic.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"country_code":"US","region_code":"CA","latitude":37.4056,"longitude":-122.0775}`)) //nolint
	}))
	defer srv.Close()

	fn := MakeIPDetails(nil, srv.URL)
	got, err := fn(publicIP)
	require.NoError(t, err)
	require.Equal(t, "US", got.Country)
}

func TestMakeIPDetails_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "8.8.8.8", r.URL.Query().Get("ip"))
		_, _ = w.Write([]byte(`{"country_code":"US","region_code":"CA","latitude":37.4056,"longitude":-122.0775}`)) //nolint
	}))
	defer srv.Close()

	fn := MakeIPDetails(logging.MustGetLogger("geo-test"), srv.URL)
	got, err := fn(publicIP)
	require.NoError(t, err)
	require.Equal(t, &LocationData{Lat: 37.41, Lon: -122.08, Country: "US", Region: "CA"}, got)
}

func TestMakeIPDetails_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{}`)) //nolint
	}))
	defer srv.Close()

	fn := MakeIPDetails(logging.MustGetLogger("geo-test"), srv.URL)
	_, err := fn(publicIP)
	require.Error(t, err)
	require.ErrorContains(t, err, ErrCannotObtainLocFromIP.Error())
}

func TestMakeIPDetails_InvalidJSON(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`not json`)) //nolint
	}))
	defer srv.Close()

	fn := MakeIPDetails(logging.MustGetLogger("geo-test"), srv.URL)
	_, err := fn(publicIP)
	require.Error(t, err)
}

func TestMakeIPDetails_RequestError(t *testing.T) {
	// Unroutable/closed address makes client.Get fail.
	fn := MakeIPDetails(logging.MustGetLogger("geo-test"), "http://127.0.0.1:1")
	_, err := fn(publicIP)
	require.Error(t, err)
}
