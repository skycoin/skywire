package geoip

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

func build(t *testing.T, block string) *service {
	t.Helper()
	s, err := factory(json.RawMessage(block), logging.MustGetLogger("geoip-test"))
	require.NoError(t, err)
	return s.(*service)
}

func TestEmbedLooksUpIP(t *testing.T) {
	s := build(t, `{"type":"geoip"}`)
	var _ services.Embeddable = s
	h, err := s.Embed(context.Background(), services.Host{})
	require.NoError(t, err)

	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/?ip=77.175.156.151", nil))
	require.Equal(t, http.StatusOK, w.Code)
	var res struct {
		CountryCode string `json:"country_code"`
	}
	require.NoError(t, json.Unmarshal(w.Body.Bytes(), &res))
	require.Equal(t, "NL", res.CountryCode)

	// Without ?ip= the caller's own address is looked up.
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	r.Header.Set("X-Forwarded-For", "77.175.156.151, 10.0.0.1")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	require.Equal(t, http.StatusOK, w.Code)
	require.Contains(t, w.Body.String(), `"NL"`)
}

func TestRunServesAddr(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := lis.Addr().String()
	require.NoError(t, lis.Close())

	s := build(t, `{"type":"geoip","addr":"`+addr+`"}`)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Run(ctx) }()

	var resp *http.Response
	require.Eventually(t, func() bool {
		resp, err = http.Get("http://" + addr + "/?ip=77.175.156.151") //nolint:noctx
		return err == nil
	}, 10*time.Second, 50*time.Millisecond)
	require.Equal(t, http.StatusOK, resp.StatusCode)
	require.NoError(t, resp.Body.Close())

	cancel()
	require.NoError(t, <-done)
}
