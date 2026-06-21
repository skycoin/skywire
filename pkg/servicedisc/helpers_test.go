// Package servicedisc pkg/servicedisc/helpers_test.go: unit tests for the
// query parsers (query.go), the HTTPError helpers (error.go) and the SWAddr
// / Service type methods (types.go) not already covered by types_test.go.
package servicedisc

import (
	"net/http"
	"net/url"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// ---- query.go --------------------------------------------------------------

func TestGeoQuery_Fill(t *testing.T) {
	t.Run("defaults and valid values", func(t *testing.T) {
		q := DefaultGeoQuery()
		require.NoError(t, q.Fill(url.Values{
			"lat":     {"12.5"},
			"lon":     {"-3.2"},
			"rad":     {"500"},
			"radUnit": {"mi"},
			"count":   {"42"},
		}))
		require.Equal(t, 12.5, q.Lat)
		require.Equal(t, -3.2, q.Lon)
		require.Equal(t, 500.0, q.Radius)
		require.Equal(t, "mi", q.RadiusUnit)
		require.Equal(t, int64(42), q.Count)
	})

	t.Run("negative count clamps to 0", func(t *testing.T) {
		var q GeoQuery
		require.NoError(t, q.Fill(url.Values{"count": {"-5"}}))
		require.Equal(t, int64(0), q.Count)
	})

	t.Run("invalid values error", func(t *testing.T) {
		for _, v := range []url.Values{
			{"lat": {"x"}},
			{"lon": {"x"}},
			{"rad": {"x"}},
			{"radUnit": {"parsecs"}},
			{"count": {"x"}},
		} {
			var q GeoQuery
			require.Error(t, q.Fill(v))
		}
	})
}

func TestServicesQuery_Fill(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		var q ServicesQuery
		require.NoError(t, q.Fill(url.Values{"count": {"10"}, "cursor": {"3"}}))
		require.Equal(t, int64(10), q.Count)
		require.Equal(t, uint64(3), q.Cursor)
	})

	t.Run("negative count clamps to 0", func(t *testing.T) {
		var q ServicesQuery
		require.NoError(t, q.Fill(url.Values{"count": {"-1"}}))
		require.Equal(t, int64(0), q.Count)
	})

	t.Run("invalid", func(t *testing.T) {
		var q ServicesQuery
		require.Error(t, q.Fill(url.Values{"count": {"x"}}))
		require.Error(t, q.Fill(url.Values{"cursor": {"x"}}))
	})
}

// ---- error.go --------------------------------------------------------------

func TestHTTPError_Error(t *testing.T) {
	e := &HTTPError{HTTPStatus: http.StatusNotFound, Err: "missing"}
	require.Equal(t, "Not Found: missing", e.Error())
}

func TestHTTPError_Timeout(t *testing.T) {
	require.True(t, (&HTTPError{HTTPStatus: http.StatusGatewayTimeout}).Timeout())
	require.True(t, (&HTTPError{HTTPStatus: http.StatusRequestTimeout}).Timeout())
	require.False(t, (&HTTPError{HTTPStatus: http.StatusOK}).Timeout())
}

func TestHTTPError_Temporary(t *testing.T) {
	require.True(t, (&HTTPError{HTTPStatus: http.StatusGatewayTimeout}).Temporary())
	require.True(t, (&HTTPError{HTTPStatus: http.StatusServiceUnavailable}).Temporary())
	require.True(t, (&HTTPError{HTTPStatus: http.StatusTooManyRequests}).Temporary())
	require.False(t, (&HTTPError{HTTPStatus: http.StatusBadRequest}).Temporary())
}

func TestHTTPError_Log(t *testing.T) {
	// Just exercise the code path; nothing to assert.
	(&HTTPError{HTTPStatus: http.StatusBadGateway, Err: "x"}).Log(logging.NewMasterLogger().PackageLogger("test"))
}

// ---- types.go: SWAddr ------------------------------------------------------

func TestSWAddr_RoundTrip(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	a := NewSWAddr(pk, 9090)

	require.Equal(t, pk, a.PubKey())
	require.Equal(t, uint16(9090), a.Port())
	require.Equal(t, pk.String()+":9090", a.String())

	text, err := a.MarshalText()
	require.NoError(t, err)

	var b SWAddr
	require.NoError(t, b.UnmarshalText(text))
	require.Equal(t, a, b)

	bin, err := a.MarshalBinary()
	require.NoError(t, err)
	var c SWAddr
	require.NoError(t, c.UnmarshalBinary(bin))
	require.Equal(t, a, c)
}

func TestSWAddr_UnmarshalText_NoPortDefaultsZero(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	var a SWAddr
	require.NoError(t, a.UnmarshalText([]byte(pk.String())))
	require.Equal(t, uint16(0), a.Port())
}

func TestSWAddr_UnmarshalText_Errors(t *testing.T) {
	var a SWAddr
	require.Error(t, a.UnmarshalText([]byte("not-a-pubkey:80")))

	pk, _ := cipher.GenerateKeyPair()
	require.Error(t, a.UnmarshalText([]byte(pk.String()+":notaport")))
}

func TestSWAddr_ScanAndValue(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	a := NewSWAddr(pk, 7)

	v, err := a.Value()
	require.NoError(t, err)
	require.Equal(t, a.String(), v)

	var b SWAddr
	require.NoError(t, b.Scan(a.String()))
	require.Equal(t, a, b)

	require.Error(t, b.Scan(12345))            // non-string
	require.Error(t, b.Scan("bad:addr:value")) // unparsable
}

// ---- types.go: Service -----------------------------------------------------

func TestService_Check(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	require.NoError(t, Service{Addr: NewSWAddr(pk, 80)}.Check())

	require.Error(t, Service{Addr: NewSWAddr(cipher.PubKey{}, 80)}.Check()) // null pk
	require.Error(t, Service{Addr: NewSWAddr(pk, 0)}.Check())               // port 0
}

func TestService_StringAndBinary(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()
	s := Service{Addr: NewSWAddr(pk, 80), Type: ServiceTypeVPN}

	require.Contains(t, s.String(), pk.String()+":80")

	data, err := s.MarshalBinary()
	require.NoError(t, err)

	var got Service
	require.NoError(t, got.UnmarshalBinary(data))
	require.Equal(t, s.Type, got.Type)
	require.Equal(t, s.Addr, got.Addr)
}
