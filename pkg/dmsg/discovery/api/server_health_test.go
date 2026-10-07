// Package api pkg/dmsg/discovery/api/server_health_test.go c2-dmsg-core
package api

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httputil"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

// Each answering server gives its sessions now and what it relayed since the
// last sample; a restart counts from zero; a silent server is marked.
func TestServerHealthCollect(t *testing.T) {
	a, _ := cipher.GenerateKeyPair()
	b, _ := cipher.GenerateKeyPair()
	health := &httputil.DmsgServerHealth{ClientSessions: 5, PeerSessions: 2, ActiveStreams: 3, StreamsRelayed: 10, BytesUp: 1000, BytesDown: 3000}
	client := &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		rec := httptest.NewRecorder()
		if !strings.HasPrefix(r.URL.Host, a.Hex()) {
			rec.WriteHeader(http.StatusBadGateway)
			return rec.Result(), nil
		}
		b, _ := json.Marshal(httputil.HealthCheckResponse{DmsgServer: health}) //nolint:errcheck
		_, _ = rec.Write(b)                                                    //nolint:errcheck
		return rec.Result(), nil
	})}
	api := &API{}
	api.SetServerHealthClient(client)
	h := api.srvHealth.Load()

	v := map[string]float64{}
	h.collect(context.Background(), []cipher.PubKey{a, b}, v)
	require.Equal(t, 5.0, v[chartSrvConns+a.Hex()])
	require.Equal(t, 2.0, v[chartSrvPeers+a.Hex()])
	require.NotContains(t, v, chartSrvBytes+a.Hex(), "the first sample has nothing to compare with")
	require.Equal(t, 1.0, v[chartSrvOK+a.Hex()])
	require.Equal(t, 0.0, v[chartSrvOK+b.Hex()])

	health.StreamsRelayed, health.BytesUp, health.BytesDown = 14, 1500, 3500
	v = map[string]float64{}
	h.collect(context.Background(), []cipher.PubKey{a, b}, v)
	require.Equal(t, 4.0, v[chartSrvStreams+a.Hex()])
	require.Equal(t, 1000.0, v[chartSrvBytes+a.Hex()])

	health.StreamsRelayed, health.BytesUp, health.BytesDown = 2, 100, 200 // restarted
	v = map[string]float64{}
	h.collect(context.Background(), []cipher.PubKey{a}, v)
	require.Equal(t, 2.0, v[chartSrvStreams+a.Hex()])
	require.Equal(t, 300.0, v[chartSrvBytes+a.Hex()])
}
