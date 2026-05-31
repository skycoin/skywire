// Package dmsgdisc dmsgdisc_test.go: unit tests for config loading, the
// service factory, the pure helpers, and the bounded/early-return paths of
// the run loop (pollServersUntilFound, updateServers, openStore, Run's
// store-open failure) that don't require redis or a live dmsg network.
package dmsgdisc

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	"github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/logging"
)

func testLog() *logging.Logger { return logging.MustGetLogger("dmsgdisc_test") }

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// ---- config ----------------------------------------------------------------

func TestParseBlock(t *testing.T) {
	// Unknown framing fields (type/name) are tolerated by ParseBlock.
	raw := []byte(`{"type":"dmsg-discovery","name":"x","addr":":9090","redis":"redis://localhost:6379","mode":"http","official_servers":["abc"]}`)
	cfg, err := ParseBlock(raw)
	require.NoError(t, err)
	require.Equal(t, ":9090", cfg.Addr)
	require.Equal(t, "redis://localhost:6379", cfg.Redis)
	require.Equal(t, "http", cfg.Mode)
	require.Equal(t, []string{"abc"}, cfg.OfficialServers)

	_, err = ParseBlock([]byte("{bad json"))
	require.Error(t, err)
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid", func(t *testing.T) {
		p := filepath.Join(dir, "ok.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"addr":":8888","mode":"dual"}`), 0o600))
		cfg, err := LoadFile(p)
		require.NoError(t, err)
		require.Equal(t, ":8888", cfg.Addr)
		require.Equal(t, "dual", cfg.Mode)
		require.Equal(t, p, cfg.Path) // Path is stamped from the filename
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadFile(filepath.Join(dir, "nope.json"))
		require.Error(t, err)
	})

	t.Run("unknown field rejected (strict)", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"totally_unknown":true}`), 0o600))
		_, err := LoadFile(p)
		require.Error(t, err)
	})
}

// ---- factory / New ---------------------------------------------------------

func TestFactoryAndNew(t *testing.T) {
	svc, err := factory(json.RawMessage(`{"addr":":9090","mode":"http"}`), testLog())
	require.NoError(t, err)
	require.NotNil(t, svc)

	_, err = factory(json.RawMessage(`{bad`), testLog())
	require.Error(t, err)

	require.NotNil(t, New(&Config{}, testLog()))
}

// ---- pure helpers ----------------------------------------------------------

func TestOfficialServersMap(t *testing.T) {
	require.Empty(t, officialServersMap(nil))

	m := officialServersMap([]string{"  pk1  ", "pk2", "", "   "})
	require.True(t, m["pk1"]) // trimmed
	require.True(t, m["pk2"])
	require.Len(t, m, 2) // empty/whitespace entries dropped
}

func TestFilterServersByType(t *testing.T) {
	in := []*disc.Entry{
		{Server: &disc.Server{ServerType: "public"}},
		{Server: &disc.Server{ServerType: "private"}},
		{Server: nil}, // no server -> filtered out
		{Server: &disc.Server{ServerType: "public"}},
	}
	out := filterServersByType(in, "public")
	require.Len(t, out, 2)
	for _, e := range out {
		require.Equal(t, "public", e.Server.ServerType)
	}
}

// ---- openStore -------------------------------------------------------------

func TestOpenStore_DefaultURLNoRedis(t *testing.T) {
	// No redis running: newRedis pings with a retrier that bails on the
	// canceled context, so this returns an error promptly (and exercises
	// the default-URL branch).
	_, err := openStore(canceledCtx(), &Config{Redis: ""}, testLog())
	require.Error(t, err)
}

// ---- pollServersUntilFound / updateServers (bounded by canceled ctx) -------

func newMockAPI(t *testing.T) *api.API {
	t.Helper()
	return api.New(testLog(), store.NewMock(), metrics.NewEmpty(), true, false, false, "", "", 0)
}

func TestPollServersUntilFound_CtxCanceled(t *testing.T) {
	a := newMockAPI(t)
	// Mock store has no servers; with a canceled ctx the poll loop returns
	// nil after the first (empty) lookup instead of waiting a minute.
	out := pollServersUntilFound(canceledCtx(), a, "", testLog())
	require.Nil(t, out)
}

func TestPollServersUntilFound_Found(t *testing.T) {
	db := store.NewMock()
	srvPK, _ := cipher.GenerateKeyPair()
	serverEntry := &disc.Entry{
		Version: "0.0.1",
		Static:  srvPK,
		Server:  &disc.Server{Address: "127.0.0.1:8080", ServerType: "public", AvailableSessions: 5},
	}
	require.NoError(t, db.SetEntry(context.Background(), serverEntry, 0))

	a := api.New(testLog(), db, metrics.NewEmpty(), true, false, false, "", "", 0)

	// A server is present, so the poll returns immediately (no waiting on
	// the minute ticker), and the type filter keeps the matching server.
	out := pollServersUntilFound(context.Background(), a, "public", testLog())
	require.Len(t, out, 1)
	require.Equal(t, srvPK, out[0].Static)
}

func TestUpdateServers_CtxCanceled(t *testing.T) {
	a := newMockAPI(t)
	// Returns immediately on a canceled ctx (the 10-minute ticker never
	// fires). Just must not block or panic.
	done := make(chan struct{})
	go func() {
		updateServers(canceledCtx(), a, nil, nil, "", testLog())
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("updateServers did not return on canceled ctx")
	}
}

// ---- listenAndServe --------------------------------------------------------

func TestListenAndServe_BindError(t *testing.T) {
	// An unbindable address makes net.Listen fail, so listenAndServe returns
	// the error instead of blocking on Serve.
	err := listenAndServe("256.256.256.256:0", api.New(testLog(), store.NewMock(), metrics.NewEmpty(), true, false, false, "", "", 0))
	require.Error(t, err)
}

func TestListenAndServe_Serves(t *testing.T) {
	// Grab a free port, then hand it to listenAndServe.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := ln.Addr().(*net.TCPAddr).Port
	require.NoError(t, ln.Close())

	a := api.New(testLog(), store.NewMock(), metrics.NewEmpty(), true, false, false, "", "", 0)
	// listenAndServe blocks on Serve; it has no shutdown hook, so the
	// goroutine is intentionally left running until the test binary exits.
	go func() { _ = listenAndServe(fmt.Sprintf("127.0.0.1:%d", port), a) }()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	require.Eventually(t, func() bool {
		resp, e := http.Get(url) //nolint:gosec
		if e != nil {
			return false
		}
		_ = resp.Body.Close()
		return resp.StatusCode == http.StatusOK
	}, 5*time.Second, 25*time.Millisecond)
}

// ---- Run (store-open failure path) -----------------------------------------

func TestRun_StoreOpenFails(t *testing.T) {
	svc := New(&Config{Mode: "http"}, testLog())
	// Canceled ctx => openStore's redis ping bails fast => Run returns the
	// wrapped store-open error before reaching the listener/dmsg surfaces.
	err := svc.Run(canceledCtx())
	require.Error(t, err)
	require.Contains(t, err.Error(), "open store")
}
