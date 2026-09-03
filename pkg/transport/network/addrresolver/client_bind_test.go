// Package addrresolver — pkg/transport/network/addrresolver/client_bind_test.go:
// exercises the QUIC / WT bind POST paths and the public-IP readiness signal
// (SetPublicIP unblocking awaitPublicIP), all driven against an httptest AR
// server. The live UDP SUDPH bind path needs a real AR and is not covered here.
package addrresolver

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

func TestBindQUIC(t *testing.T) {
	t.Run("success posts to quic bind path", func(t *testing.T) {
		var got LocalAddresses
		gotCh := make(chan struct{}, 1)
		mux := chi.NewRouter()
		mux.Post("/bind/quic", func(w http.ResponseWriter, r *http.Request) {
			_ = json.NewDecoder(r.Body).Decode(&got) //nolint
			gotCh <- struct{}{}
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		// Public IP already signaled → bind does not wait the full timeout.
		c.SetPublicIP("", "")
		require.NoError(t, c.BindQUIC(context.Background(), "30300"))
		<-gotCh
		assert.Equal(t, "30300", got.Port)
	})

	t.Run("rate limited returns error", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Post("/bind/quic", func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusTooManyRequests)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		c.SetPublicIP("", "")
		err := c.BindQUIC(context.Background(), "30300")
		require.Error(t, err)
		assert.Contains(t, err.Error(), "429")
	})

	t.Run("non-OK status returns error", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Post("/bind/quic", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", http.StatusInternalServerError)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		c.SetPublicIP("", "")
		require.Error(t, c.BindQUIC(context.Background(), "30300"))
	})
}

func TestBindWT(t *testing.T) {
	var got LocalAddresses
	gotCh := make(chan struct{}, 1)
	mux := chi.NewRouter()
	mux.Post("/bind/wt", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&got) //nolint
		gotCh <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)
	// A declared public IP is injected into the advertised address set.
	c.SetPublicIP("9.9.9.9:1", "")
	require.NoError(t, c.BindWT(context.Background(), "30301", "deadbeefcert"))
	<-gotCh
	assert.Equal(t, "30301", got.Port)
	assert.Equal(t, "deadbeefcert", got.CertHash)
	assert.Contains(t, got.Addresses, "9.9.9.9")
}

func TestSetPublicIPUnblocksAwait(t *testing.T) {
	c := &httpClient{
		log:     logging.MustGetLogger("ar-setpub"),
		closed:  make(chan struct{}),
		ipReady: make(chan struct{}),
	}

	// Before SetPublicIP, awaitPublicIP blocks until the timeout.
	start := time.Now()
	c.awaitPublicIP(50 * time.Millisecond)
	assert.GreaterOrEqual(t, time.Since(start), 50*time.Millisecond)

	// SetPublicIP records the IPs and closes ipReady (idempotently).
	c.SetPublicIP("1.2.3.4:5", "[2001:db8::1]:6")
	assert.Equal(t, "1.2.3.4", c.LocalPublicIP())
	assert.Equal(t, "1.2.3.4:5", c.localPublicIPRaw())
	assert.Equal(t, "[2001:db8::1]:6", c.localPublicIPv6Raw())

	// After the ready signal, awaitPublicIP returns immediately.
	start = time.Now()
	c.awaitPublicIP(5 * time.Second)
	assert.Less(t, time.Since(start), time.Second)

	// Second call is a safe no-op (ipReadyOnce guards the close).
	c.SetPublicIP("9.9.9.9", "")
}
