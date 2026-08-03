// Package addrresolver — pkg/transport/network/addrresolver/client_extra_test.go:
// exercises the HTTP-backed surface of the AR client (Resolve /
// Transports / TransportsType / Delete / Close / fetchPublicUDPAddr) via
// an httptest server, plus the pure helpers (Addresses / LocalPublicIP /
// isReady / isClosed) and the SUDPH housekeeping loops driven against an
// in-memory writer. The live UDP/KCP SUDPH path needs a real AR server
// and is not covered here.
package addrresolver

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauthclient"
	"github.com/skycoin/skywire/pkg/logging"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

// arRouter builds a chi router that satisfies the auth handshake
// (/security/nonces/{pk}) and dispatches everything else to next.
func arRouter(next http.Handler) http.Handler {
	r := chi.NewRouter()
	r.Handle("/security/nonces/{pk}", http.HandlerFunc(func(w http.ResponseWriter, rq *http.Request) {
		pk := chi.URLParam(rq, "pk")
		var edge cipher.PubKey
		_ = edge.Set(pk)                                                                           //nolint
		_ = json.NewEncoder(w).Encode(&httpauthclient.NextNonceResponse{Edge: edge, NextNonce: 1}) //nolint
	}))
	r.Handle("/*", next)
	return r
}

// newReadyClient stands up an AR client against srv and waits until its
// background auth handshake has completed (c.ready closed).
func newReadyClient(t *testing.T, srv *httptest.Server) *httpClient {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	log := logging.MustGetLogger("ar-extra-test")
	ml := logging.NewMasterLogger()
	api, err := NewHTTP(srv.URL, pk, sk, &http.Client{}, nil, "", "", log, ml)
	require.NoError(t, err)
	c := api.(*httpClient)
	select {
	case <-c.ready:
	case <-time.After(5 * time.Second):
		t.Fatal("AR client never became ready")
	}
	return c
}

func TestResolve(t *testing.T) {
	target, _ := cipher.GenerateKeyPair()

	mux := chi.NewRouter()
	mux.Get("/resolve/stcpr/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(VisorData{RemoteAddr: "1.2.3.4:5000"}) //nolint
	})
	mux.Get("/resolve/sudph/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusNotFound)
	})
	mux.Get("/resolve/stcp/{pk}", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "boom", http.StatusInternalServerError)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)

	t.Run("success", func(t *testing.T) {
		vd, err := c.Resolve(context.Background(), "stcpr", target)
		require.NoError(t, err)
		assert.Equal(t, "1.2.3.4:5000", vd.RemoteAddr)
	})
	t.Run("not found", func(t *testing.T) {
		_, err := c.Resolve(context.Background(), "sudph", target)
		require.ErrorIs(t, err, ErrNoEntry)
	})
	t.Run("server error", func(t *testing.T) {
		_, err := c.Resolve(context.Background(), "stcp", target)
		require.Error(t, err)
		require.NotErrorIs(t, err, ErrNoEntry)
	})
}

func TestResolveNotReady(t *testing.T) {
	// A hand-built client whose ready channel is still open reports
	// ErrNotReady without touching the network.
	c := &httpClient{
		log:    logging.MustGetLogger("ar-notready"),
		ready:  make(chan struct{}),
		closed: make(chan struct{}),
	}
	target, _ := cipher.GenerateKeyPair()
	_, err := c.Resolve(context.Background(), "stcpr", target)
	require.ErrorIs(t, err, ErrNotReady)
	require.False(t, c.isReady())
}

func TestTransports(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	t.Run("success", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Get("/transports", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string][]string{ //nolint
				"stcpr": {pk1.Hex(), pk2.Hex()},
				"sudph": {pk1.Hex(), "not-a-key"},
			})
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		res, err := c.Transports(context.Background())
		require.NoError(t, err)
		// pk1 appears under both stcpr and sudph.
		assert.Len(t, res[pk1], 2)
		assert.Len(t, res[pk2], 1)
	})

	t.Run("non-OK status", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Get("/transports", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "down", http.StatusServiceUnavailable)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		_, err := c.Transports(context.Background())
		require.ErrorIs(t, err, ErrNoTransportsFound)
	})
}

func TestTransportsType(t *testing.T) {
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()

	mux := chi.NewRouter()
	mux.Get("/transports", func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string][]string{ //nolint
			"sudph": {pk1.Hex()},
			"stcpr": {pk2.Hex(), "bad-key"},
		})
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()
	c := newReadyClient(t, srv)

	sudph, err := c.TransportsType(context.Background(), types.SUDPH)
	require.NoError(t, err)
	_, ok := sudph[pk1]
	assert.True(t, ok)

	stcpr, err := c.TransportsType(context.Background(), types.STCPR)
	require.NoError(t, err)
	_, ok = stcpr[pk2]
	assert.True(t, ok)

	if _, err := c.TransportsType(context.Background(), types.DMSG); err == nil {
		t.Error("unsupported network type should error")
	}
}

func TestDelete(t *testing.T) {
	gotMethod := make(chan string, 1)
	mux := chi.NewRouter()
	mux.Delete("/some/path", func(_ http.ResponseWriter, r *http.Request) {
		gotMethod <- r.Method
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	c := newReadyClient(t, srv)
	resp, err := c.Delete(context.Background(), "/some/path")
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	assert.Equal(t, http.MethodDelete, <-gotMethod)
}

func TestClose(t *testing.T) {
	srv := httptest.NewServer(arRouter(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})))
	defer srv.Close()

	c := newReadyClient(t, srv)
	require.False(t, c.isClosed())
	require.NoError(t, c.Close())
	require.True(t, c.isClosed())
	// Idempotent.
	require.NoError(t, c.Close())
}

func TestFetchPublicUDPAddr(t *testing.T) {
	t.Run("advertised udp_address", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"udp_address": "5.6.7.8:30178"}) //nolint
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c := &httpClient{log: logging.MustGetLogger("ar-udp"), remoteHTTPAddr: srv.URL}
		assert.Equal(t, "5.6.7.8:30178", c.fetchPublicUDPAddr(&http.Client{}))
	})

	t.Run("host without port gets default port", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{"udp_address": "5.6.7.8"}) //nolint
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c := &httpClient{log: logging.MustGetLogger("ar-udp"), remoteHTTPAddr: srv.URL}
		assert.Equal(t, net.JoinHostPort("5.6.7.8", defaultUDPPort), c.fetchPublicUDPAddr(&http.Client{}))
	})

	t.Run("empty udp_address yields empty", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Get("/health", func(w http.ResponseWriter, _ *http.Request) {
			_ = json.NewEncoder(w).Encode(map[string]string{}) //nolint
		})
		srv := httptest.NewServer(mux)
		defer srv.Close()

		c := &httpClient{log: logging.MustGetLogger("ar-udp"), remoteHTTPAddr: srv.URL}
		assert.Empty(t, c.fetchPublicUDPAddr(&http.Client{}))
	})

	t.Run("transient transport error is retried", func(t *testing.T) {
		attempts := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			attempts++
			if attempts == 1 {
				hijacker, ok := w.(http.Hijacker)
				if !ok {
					t.Error("response writer does not support hijacking")
					return
				}
				conn, _, err := hijacker.Hijack()
				if err != nil {
					t.Errorf("hijack connection: %v", err)
					return
				}
				_ = conn.Close() //nolint
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]string{"udp_address": "5.6.7.8:30178"}) //nolint
		}))
		defer srv.Close()

		c := &httpClient{log: logging.MustGetLogger("ar-udp"), remoteHTTPAddr: srv.URL}
		assert.Equal(t, "5.6.7.8:30178", c.fetchPublicUDPAddr(&http.Client{}))
		assert.GreaterOrEqual(t, attempts, 2)
	})

	t.Run("non-OK status yields empty", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", http.StatusInternalServerError)
		}))
		defer srv.Close()
		c := &httpClient{log: logging.MustGetLogger("ar-udp"), remoteHTTPAddr: srv.URL}
		assert.Empty(t, c.fetchPublicUDPAddr(&http.Client{}))
	})
}

// --- bind paths ------------------------------------------------------

func TestBindSTCPRWithV6AndPublicIP(t *testing.T) {
	var v4Binds, v6Binds int
	bindCh := make(chan struct{}, 4)
	mux := chi.NewRouter()
	mux.Post("/bind/stcpr", func(w http.ResponseWriter, _ *http.Request) {
		v4Binds++ // both v4 and v6 clients hit the same loopback endpoint
		bindCh <- struct{}{}
		w.WriteHeader(http.StatusOK)
	})
	srv := httptest.NewServer(arRouter(mux))
	defer srv.Close()

	pk, sk := cipher.GenerateKeyPair()
	// A non-nil v6 client makes initHTTPClient set httpClientV6, so
	// BindSTCPR fires the secondary postV6BindSTCPR. clientPublicIP
	// exercises the NAT public-IP injection branch.
	api, err := NewHTTP(srv.URL, pk, sk, &http.Client{}, &http.Client{}, "9.9.9.9:30178", "", logging.MustGetLogger("bind"), logging.NewMasterLogger())
	require.NoError(t, err)
	c := api.(*httpClient)
	<-c.ready

	require.NoError(t, c.BindSTCPR(context.Background(), "1234"))
	// Expect both the v4 POST and the v6 POST.
	<-bindCh
	<-bindCh
	_ = v6Binds
	assert.GreaterOrEqual(t, v4Binds, 2)
}

func TestDelBindSTCPR(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		done := make(chan struct{}, 1)
		mux := chi.NewRouter()
		mux.Delete("/bind/stcpr", func(w http.ResponseWriter, _ *http.Request) {
			done <- struct{}{}
			w.WriteHeader(http.StatusOK)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		require.NoError(t, c.delBindSTCPR(context.Background()))
		<-done
	})

	t.Run("non-OK status errors", func(t *testing.T) {
		mux := chi.NewRouter()
		mux.Delete("/bind/stcpr", func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, "no", http.StatusInternalServerError)
		})
		srv := httptest.NewServer(arRouter(mux))
		defer srv.Close()

		c := newReadyClient(t, srv)
		require.Error(t, c.delBindSTCPR(context.Background()))
	})
}

// --- pure helpers ----------------------------------------------------

func TestAddressesAndLocalPublicIP(t *testing.T) {
	// No SUDPH connection → Addresses returns "".
	c := &httpClient{log: logging.MustGetLogger("ar-helpers")}
	assert.Equal(t, "", c.Addresses(context.Background()))

	// host:port → host only.
	c.clientPublicIP = "9.9.9.9:30178"
	assert.Equal(t, "9.9.9.9", c.LocalPublicIP())

	// bare host (no port) → returned verbatim.
	c.clientPublicIP = "bare-host"
	assert.Equal(t, "bare-host", c.LocalPublicIP())

	// empty → "".
	c.clientPublicIP = ""
	assert.Equal(t, "", c.LocalPublicIP())
}

// --- SUDPH housekeeping loops ----------------------------------------

type errWriter struct{}

func (errWriter) Write([]byte) (int, error) { return 0, errors.New("write failed") }

func TestKeepSudphHeartbeatLoop(t *testing.T) {
	// Write error → loop returns the error.
	c := &httpClient{log: logging.MustGetLogger("ar-hb"), closed: make(chan struct{})}
	require.Error(t, c.keepSudphHeartbeatLoop(errWriter{}))

	// Already-closed client → returns nil before writing.
	c2 := &httpClient{log: logging.MustGetLogger("ar-hb"), closed: make(chan struct{})}
	close(c2.closed)
	require.NoError(t, c2.keepSudphHeartbeatLoop(errWriter{}))
}

func TestSudphReRegisterLoopStops(t *testing.T) {
	// Already-closed client → loop returns nil immediately.
	c := &httpClient{log: logging.MustGetLogger("ar-rereg"), closed: make(chan struct{})}
	close(c.closed)
	require.NoError(t, c.sudphReRegisterLoop(errWriter{}))
}

func TestDelBindSUDPHLoop(t *testing.T) {
	t.Run("writes unbind on close", func(t *testing.T) {
		serverConn, clientConn := net.Pipe()
		c := &httpClient{
			log:         logging.MustGetLogger("ar-delbind"),
			closed:      make(chan struct{}),
			sudphArConn: clientConn,
		}
		c.delBindSudphWg.Add(1)

		got := make(chan string, 1)
		go func() {
			buf := make([]byte, len(UDPDelBindMessage))
			n, _ := serverConn.Read(buf) //nolint
			got <- string(buf[:n])
		}()

		go c.delBindSUDPHLoop()
		close(c.closed)

		select {
		case msg := <-got:
			assert.Equal(t, UDPDelBindMessage, msg)
		case <-time.After(3 * time.Second):
			t.Fatal("unbind message not sent")
		}
		c.delBindSudphWg.Wait()
	})

	t.Run("nil arConn is a no-op", func(t *testing.T) {
		c := &httpClient{log: logging.MustGetLogger("ar-delbind"), closed: make(chan struct{})}
		c.delBindSudphWg.Add(1)
		close(c.closed)
		c.delBindSUDPHLoop()
		c.delBindSudphWg.Wait()
	})
}

// --- generated mock --------------------------------------------------

func TestMockAPIClient(t *testing.T) {
	m := NewMockAPIClient(t)
	ctx := context.Background()
	pk, _ := cipher.GenerateKeyPair()

	m.On("BindSTCPR", ctx, "1234").Return(nil)
	m.On("Resolve", ctx, "stcpr", pk).Return(VisorData{RemoteAddr: "1.2.3.4:5"}, nil)
	m.On("Transports", ctx).Return(map[cipher.PubKey][]string{pk: {"stcpr"}}, nil)
	m.On("TransportsType", ctx, types.STCPR).Return(map[cipher.PubKey][]string{pk: nil}, nil)
	m.On("Addresses", ctx).Return("addr")
	m.On("LocalPublicIP").Return("1.2.3.4")
	m.On("Close").Return(nil)

	require.NoError(t, m.BindSTCPR(ctx, "1234"))
	vd, err := m.Resolve(ctx, "stcpr", pk)
	require.NoError(t, err)
	assert.Equal(t, "1.2.3.4:5", vd.RemoteAddr)
	tps, err := m.Transports(ctx)
	require.NoError(t, err)
	assert.Len(t, tps, 1)
	tt, err := m.TransportsType(ctx, types.STCPR)
	require.NoError(t, err)
	assert.Len(t, tt, 1)
	assert.Equal(t, "addr", m.Addresses(ctx))
	assert.Equal(t, "1.2.3.4", m.LocalPublicIP())
	require.NoError(t, m.Close())
}
