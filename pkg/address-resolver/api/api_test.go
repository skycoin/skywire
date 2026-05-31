// Package api api_test.go: unit tests for the address-resolver HTTP API —
// pure helpers, the JSON/health/transports handlers, and the bind / delBind
// / resolve / deregister handlers driven directly with injected auth + chi
// route context (bypassing the httpauth signing middleware).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	armetrics "github.com/skycoin/skywire/pkg/address-resolver/metrics"
	"github.com/skycoin/skywire/pkg/address-resolver/store"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

func testLog() *logging.Logger { return logging.MustGetLogger("ar_api_test") }

func newTestAPI(t *testing.T) *API {
	t.Helper()
	s, err := store.New(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, time.Minute, testLog())
	require.NoError(t, err)
	a := New(testLog(), s, nil, false, armetrics.NewEmpty(), "dmsg://srv:80", "203.0.113.9:30178")
	t.Cleanup(a.Close)
	return a
}

// authReq builds a request carrying an authenticated PK in its context and,
// optionally, chi URL params.
func authReq(t *testing.T, method, target string, body []byte, pk *cipher.PubKey, params map[string]string) *http.Request {
	t.Helper()
	var r *http.Request
	if body != nil {
		r = httptest.NewRequest(method, target, bytes.NewReader(body))
	} else {
		r = httptest.NewRequest(method, target, nil)
	}
	r.RemoteAddr = "203.0.113.5:40000"

	ctx := r.Context()
	if pk != nil {
		ctx = context.WithValue(ctx, httpauth.ContextAuthKey, *pk)
	}
	if params != nil {
		rctx := chi.NewRouteContext()
		for k, v := range params {
			rctx.URLParams.Add(k, v)
		}
		ctx = context.WithValue(ctx, chi.RouteCtxKey, rctx)
	}
	return r.WithContext(ctx)
}

// ---- pure helpers ----------------------------------------------------------
// (isPublicIPv6 and splitFamilyAddr are covered in the ipv6_* test files.)

func TestSameIP(t *testing.T) {
	require.True(t, sameIP("1.2.3.4:10", "1.2.3.4:20"))
	require.False(t, sameIP("1.2.3.4:10", "5.6.7.8:20"))
	require.False(t, sameIP("no-port", "1.2.3.4:20")) // first unparsable
	require.False(t, sameIP("1.2.3.4:10", "no-port")) // second unparsable
}

func TestHasAddress(t *testing.T) {
	a := newTestAPI(t)
	local := addrresolver.LocalAddresses{Addresses: []string{"203.0.113.5", "10.0.0.1"}}
	require.True(t, a.hasAddress("203.0.113.5", local))
	require.False(t, a.hasAddress("8.8.8.8", local))
}

func TestUDPConnHelpers(t *testing.T) {
	a := newTestAPI(t)
	pk, _ := cipher.GenerateKeyPair()

	_, ok := a.udpConn(pk)
	require.False(t, ok)

	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck

	a.setUDPConn(pk, c1)
	got, ok := a.udpConn(pk)
	require.True(t, ok)
	require.Equal(t, c1, got)

	a.deleteUDPConn(pk)
	_, ok = a.udpConn(pk)
	require.False(t, ok)
}

func TestMirrorsAndBackfill_NoMirror(t *testing.T) {
	a := newTestAPI(t)
	pk, _ := cipher.GenerateKeyPair()
	// No mirrors configured -> all of these are safe no-ops.
	a.mirrorSTCPR(pk, &addrresolver.VisorData{})
	a.mirrorSUDPH(pk, &addrresolver.VisorData{})
	a.BackfillDHTMirror(context.Background(), testLog())
	a.SetDHTMirrors(nil, nil)
	a.updateMetrics() // exercise the metrics path
}

func TestWriteJSON_MarshalError(t *testing.T) {
	a := newTestAPI(t)
	rec := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)
	// channels can't be JSON-marshaled -> 500.
	a.writeJSON(rec, r, http.StatusOK, make(chan int))
	require.Equal(t, http.StatusInternalServerError, rec.Code)
}

// ---- health / transports (no auth) ----------------------------------------

func TestHealth(t *testing.T) {
	a := newTestAPI(t)
	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/health", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp HealthCheckResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	require.Equal(t, "address-resolver", resp.ServiceName)
	require.Equal(t, "dmsg://srv:80", resp.DmsgAddr)
	require.Equal(t, "203.0.113.9:30178", resp.UDPAddr)
}

func TestTransports(t *testing.T) {
	a := newTestAPI(t)
	ctx := context.Background()
	pk, _ := cipher.GenerateKeyPair()
	require.NoError(t, a.store.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "203.0.113.5"}))

	rec := httptest.NewRecorder()
	a.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/transports", nil))
	require.Equal(t, http.StatusOK, rec.Code)

	var data ArData
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &data))
	require.Contains(t, data.Stcpr, pk.Hex())
}

// ---- bind ------------------------------------------------------------------

func TestBind(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("unauthorized", func(t *testing.T) {
		a := newTestAPI(t)
		rec := httptest.NewRecorder()
		a.bind(rec, authReq(t, http.MethodPost, "/bind/stcpr", []byte("{}"), nil, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("remote addr not in local addresses -> 400", func(t *testing.T) {
		a := newTestAPI(t)
		body, _ := json.Marshal(addrresolver.LocalAddresses{Addresses: []string{"8.8.8.8"}})
		rec := httptest.NewRecorder()
		a.bind(rec, authReq(t, http.MethodPost, "/bind/stcpr", body, &pk, nil))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("success binds and stores", func(t *testing.T) {
		a := newTestAPI(t)
		body, _ := json.Marshal(addrresolver.LocalAddresses{
			Port:      "30000",
			Addresses: []string{"203.0.113.5"},
		})
		rec := httptest.NewRecorder()
		a.bind(rec, authReq(t, http.MethodPost, "/bind/stcpr", body, &pk, nil))
		require.Equal(t, http.StatusOK, rec.Code)

		vd, err := a.store.Resolve(context.Background(), types.STCPR, pk)
		require.NoError(t, err)
		require.Equal(t, "203.0.113.5", vd.RemoteAddr)
	})

	t.Run("declared public IPv6 populates RemoteAddrV6", func(t *testing.T) {
		a := newTestAPI(t)
		body, _ := json.Marshal(addrresolver.LocalAddresses{
			Port:       "30000",
			Addresses:  []string{"203.0.113.5"},
			PublicIPv6: "2606:4700:4700::1111",
		})
		rec := httptest.NewRecorder()
		a.bind(rec, authReq(t, http.MethodPost, "/bind/stcpr", body, &pk, nil))
		require.Equal(t, http.StatusOK, rec.Code)

		vd, err := a.store.Resolve(context.Background(), types.STCPR, pk)
		require.NoError(t, err)
		require.Equal(t, "2606:4700:4700::1111", vd.RemoteAddrV6)
	})
}

// ---- delBind ---------------------------------------------------------------

func TestDelBind(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()

	t.Run("unauthorized", func(t *testing.T) {
		a := newTestAPI(t)
		rec := httptest.NewRecorder()
		a.delBind(rec, authReq(t, http.MethodDelete, "/bind/stcpr", nil, nil, nil))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("success", func(t *testing.T) {
		a := newTestAPI(t)
		ctx := context.Background()
		require.NoError(t, a.store.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "203.0.113.5"}))

		rec := httptest.NewRecorder()
		a.delBind(rec, authReq(t, http.MethodDelete, "/bind/stcpr", nil, &pk, nil))
		require.Equal(t, http.StatusOK, rec.Code)

		_, err := a.store.Resolve(ctx, types.STCPR, pk)
		require.ErrorIs(t, err, store.ErrNoEntry)
	})
}

// ---- resolve ---------------------------------------------------------------

func TestResolve(t *testing.T) {
	sender, _ := cipher.GenerateKeyPair()
	receiver, _ := cipher.GenerateKeyPair()

	t.Run("unauthorized", func(t *testing.T) {
		a := newTestAPI(t)
		rec := httptest.NewRecorder()
		a.resolve(rec, authReq(t, http.MethodGet, "/resolve/stcpr/x", nil, nil,
			map[string]string{"type": "stcpr", "pk": receiver.Hex()}))
		require.Equal(t, http.StatusUnauthorized, rec.Code)
	})

	t.Run("bad receiver pk", func(t *testing.T) {
		a := newTestAPI(t)
		rec := httptest.NewRecorder()
		a.resolve(rec, authReq(t, http.MethodGet, "/resolve/stcpr/x", nil, &sender,
			map[string]string{"type": "stcpr", "pk": "not-a-pk"}))
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("no entry -> 404", func(t *testing.T) {
		a := newTestAPI(t)
		rec := httptest.NewRecorder()
		a.resolve(rec, authReq(t, http.MethodGet, "/resolve/stcpr/x", nil, &sender,
			map[string]string{"type": "stcpr", "pk": receiver.Hex()}))
		require.Equal(t, http.StatusNotFound, rec.Code)
	})

	t.Run("found -> 200 with visor data", func(t *testing.T) {
		a := newTestAPI(t)
		ctx := context.Background()
		require.NoError(t, a.store.Bind(ctx, types.STCPR, receiver, addrresolver.VisorData{RemoteAddr: "198.51.100.7"}))

		rec := httptest.NewRecorder()
		a.resolve(rec, authReq(t, http.MethodGet, "/resolve/stcpr/x", nil, &sender,
			map[string]string{"type": "stcpr", "pk": receiver.Hex()}))
		require.Equal(t, http.StatusOK, rec.Code)

		var vd addrresolver.VisorData
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &vd))
		require.Equal(t, "198.51.100.7", vd.RemoteAddr)
		require.False(t, vd.IsLocal) // sender remote (203.0.113.5) != receiver (198.51.100.7)
	})
}

// ---- deregister ------------------------------------------------------------

func TestDeregister(t *testing.T) {
	t.Run("non-whitelisted NM -> 403", func(t *testing.T) {
		a := newTestAPI(t)
		nmPK, _ := cipher.GenerateKeyPair()
		req := httptest.NewRequest(http.MethodDelete, "/deregister/stcpr", nil)
		req.Header.Set("NM-PK", nmPK.Hex())
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		require.Equal(t, http.StatusForbidden, rec.Code)
	})

	t.Run("whitelisted bad network type -> 400", func(t *testing.T) {
		a := newTestAPI(t)
		nmPK, nmSK := cipher.GenerateKeyPair()
		WhitelistPKs.Set(nmPK.Hex())
		defer delete(WhitelistPKs, nmPK.Hex())

		sig, err := cipher.SignPayload([]byte(nmPK.Hex()), nmSK)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodDelete, "/deregister/bogus", nil)
		req.Header.Set("NM-PK", nmPK.Hex())
		req.Header.Set("NM-Sign", sig.Hex())
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		require.Equal(t, http.StatusBadRequest, rec.Code)
	})

	t.Run("whitelisted success deletes binds", func(t *testing.T) {
		a := newTestAPI(t)
		ctx := context.Background()
		target, _ := cipher.GenerateKeyPair()
		require.NoError(t, a.store.Bind(ctx, types.STCPR, target, addrresolver.VisorData{RemoteAddr: "203.0.113.5"}))

		nmPK, nmSK := cipher.GenerateKeyPair()
		WhitelistPKs.Set(nmPK.Hex())
		defer delete(WhitelistPKs, nmPK.Hex())
		sig, err := cipher.SignPayload([]byte(nmPK.Hex()), nmSK)
		require.NoError(t, err)

		body, _ := json.Marshal([]string{target.Hex()})
		req := httptest.NewRequest(http.MethodDelete, "/deregister/stcpr", bytes.NewReader(body))
		req.Header.Set("NM-PK", nmPK.Hex())
		req.Header.Set("NM-Sign", sig.Hex())
		rec := httptest.NewRecorder()
		a.ServeHTTP(rec, req)
		require.Equal(t, http.StatusOK, rec.Code)

		_, err = a.store.Resolve(ctx, types.STCPR, target)
		require.ErrorIs(t, err, store.ErrNoEntry)
	})
}

// ---- DHT mirrors -----------------------------------------------------------

type fakeMirror struct {
	mirrored int
	deleted  int
}

func (m *fakeMirror) Mirror(_ cipher.PubKey, _ any, _ uint64) { m.mirrored++ }
func (m *fakeMirror) Delete(_ cipher.PubKey)                  { m.deleted++ }

func TestMirrors_WithMirror(t *testing.T) {
	a := newTestAPI(t)
	stcpr, sudph := &fakeMirror{}, &fakeMirror{}
	a.SetDHTMirrors(stcpr, sudph)
	pk, _ := cipher.GenerateKeyPair()

	a.mirrorSTCPR(pk, &addrresolver.VisorData{})
	a.mirrorSUDPH(pk, &addrresolver.VisorData{})
	require.Equal(t, 1, stcpr.mirrored)
	require.Equal(t, 1, sudph.mirrored)

	// delBind triggers a Delete on the STCPR mirror.
	ctx := context.Background()
	require.NoError(t, a.store.Bind(ctx, types.STCPR, pk, addrresolver.VisorData{RemoteAddr: "203.0.113.5"}))
	rec := httptest.NewRecorder()
	a.delBind(rec, authReq(t, http.MethodDelete, "/bind/stcpr", nil, &pk, nil))
	require.Equal(t, http.StatusOK, rec.Code)
	require.Equal(t, 1, stcpr.deleted)
}

func TestBackfillDHTMirror_WithMirror(t *testing.T) {
	a := newTestAPI(t)
	stcpr, sudph := &fakeMirror{}, &fakeMirror{}
	a.SetDHTMirrors(stcpr, sudph)

	ctx := context.Background()
	pk1, _ := cipher.GenerateKeyPair()
	pk2, _ := cipher.GenerateKeyPair()
	require.NoError(t, a.store.Bind(ctx, types.STCPR, pk1, addrresolver.VisorData{RemoteAddr: "203.0.113.5"}))
	require.NoError(t, a.store.Bind(ctx, types.SUDPH, pk2, addrresolver.VisorData{RemoteAddr: "203.0.113.6:30000"}))

	a.BackfillDHTMirror(ctx, testLog())
	require.GreaterOrEqual(t, stcpr.mirrored, 1)
	require.GreaterOrEqual(t, sudph.mirrored, 1)
}

// ---- UDP helpers -----------------------------------------------------------

func TestAskToDialUDP(t *testing.T) {
	a := newTestAPI(t)
	dialer, _ := cipher.GenerateKeyPair()
	dialee, _ := cipher.GenerateKeyPair()
	r := httptest.NewRequest(http.MethodGet, "/x", nil)

	// No conn for dialer -> ErrNotConnected.
	err := a.askToDialUDP(dialer, dialee, r, addrresolver.VisorData{RemoteAddr: "1.2.3.4:5"})
	require.ErrorIs(t, err, ErrNotConnected)

	// With a conn, the dialee address is written to it.
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck
	a.setUDPConn(dialer, c1)

	readDone := make(chan []byte, 1)
	go func() {
		buf := make([]byte, 256)
		n, _ := c2.Read(buf)
		readDone <- buf[:n]
	}()

	require.NoError(t, a.askToDialUDP(dialer, dialee, r, addrresolver.VisorData{RemoteAddr: "1.2.3.4:5"}))
	got := <-readDone
	var rv addrresolver.RemoteVisor
	require.NoError(t, json.Unmarshal(got, &rv))
	require.Equal(t, dialee, rv.PK)
	require.Equal(t, "1.2.3.4:5", rv.Addr)
}

func TestCleanupUDPConn(t *testing.T) {
	a := newTestAPI(t)
	pk, _ := cipher.GenerateKeyPair()
	c1, c2 := net.Pipe()
	defer c2.Close() //nolint:errcheck

	a.setUDPConn(pk, c1)
	a.cleanupUDPConn(pk, c1)

	_, ok := a.udpConn(pk)
	require.False(t, ok)
	// conn was closed: a write should fail.
	_, err := c1.Write([]byte("x"))
	require.Error(t, err)
}

// ---- resolve SUDPH branch (asks receiver to dial sender) -------------------

func TestResolve_SUDPH(t *testing.T) {
	a := newTestAPI(t)
	ctx := context.Background()
	sender, _ := cipher.GenerateKeyPair()
	receiver, _ := cipher.GenerateKeyPair()

	require.NoError(t, a.store.Bind(ctx, types.SUDPH, receiver, addrresolver.VisorData{RemoteAddr: "198.51.100.7:30000"}))
	require.NoError(t, a.store.Bind(ctx, types.SUDPH, sender, addrresolver.VisorData{RemoteAddr: "203.0.113.5:30000"}))

	// receiver has a live UDP conn, so it can be asked to dial the sender.
	c1, c2 := net.Pipe()
	defer c1.Close() //nolint:errcheck
	defer c2.Close() //nolint:errcheck
	a.setUDPConn(receiver, c1)
	go func() {
		buf := make([]byte, 256)
		_, _ = c2.Read(buf) // drain the ask-to-dial write so the handler doesn't block
	}()

	rec := httptest.NewRecorder()
	a.resolve(rec, authReq(t, http.MethodGet, "/resolve/sudph/x", nil, &sender,
		map[string]string{"type": "sudph", "pk": receiver.Hex()}))
	require.Equal(t, http.StatusOK, rec.Code)
}

func TestNewAndClose(t *testing.T) {
	a := newTestAPI(t)
	require.NotNil(t, a.Handler)
	// Close is idempotent.
	a.Close()
	a.Close()
}
