// Package sn sn_test.go: unit tests for config loading (ParseBlock /
// LoadFile), the service factory + New, the getHTTPClient / plainHTTPClient
// helpers, and the early error-return paths of Run (missing config_path
// file, missing keys) that don't require a live dmsg network.
package sn

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	discmetrics "github.com/skycoin/skywire/pkg/dmsg/disc/metrics"
	discapi "github.com/skycoin/skywire/pkg/dmsg/discovery/api"
	discstore "github.com/skycoin/skywire/pkg/dmsg/discovery/store"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router"
)

func testLog() *logging.Logger { return logging.MustGetLogger("sn_test") }

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

func validKeys(t *testing.T) (cipher.PubKey, cipher.SecKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	return pk, sk
}

// ---- config: ParseBlock ----------------------------------------------------

func TestParseBlock(t *testing.T) {
	raw := []byte(`{"type":"setup-node","name":"x","metrics_addr":":2121","pprof_mode":"http","tag":"sn1","transport_discovery":"http://tpd"}`)
	cfg, err := ParseBlock(raw)
	require.NoError(t, err)
	require.Equal(t, ":2121", cfg.MetricsAddr)
	require.Equal(t, "http", cfg.PProfMode)
	require.Equal(t, "sn1", cfg.Tag)
	require.Equal(t, "http://tpd", cfg.TransportDiscovery)

	_, err = ParseBlock([]byte("{bad json"))
	require.Error(t, err)
}

// ---- config: LoadFile ------------------------------------------------------

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()
	pk, sk := validKeys(t)

	t.Run("valid", func(t *testing.T) {
		p := filepath.Join(dir, "ok.json")
		body, err := json.Marshal(router.SetupConfig{PK: pk, SK: sk, TransportDiscovery: "http://tpd"})
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(p, body, 0o600))

		sc, err := LoadFile(p)
		require.NoError(t, err)
		require.Equal(t, pk, sc.PK)
		require.Equal(t, "http://tpd", sc.TransportDiscovery)
	})

	t.Run("missing file", func(t *testing.T) {
		_, err := LoadFile(filepath.Join(dir, "nope.json"))
		require.Error(t, err)
	})

	t.Run("malformed json", func(t *testing.T) {
		p := filepath.Join(dir, "bad.json")
		require.NoError(t, os.WriteFile(p, []byte(`{not json`), 0o600))
		_, err := LoadFile(p)
		require.Error(t, err)
	})
}

// ---- factory / New ---------------------------------------------------------

func TestFactoryAndNew(t *testing.T) {
	svc, err := factory(json.RawMessage(`{"tag":"sn"}`), testLog())
	require.NoError(t, err)
	require.NotNil(t, svc)

	_, err = factory(json.RawMessage(`{bad`), testLog())
	require.Error(t, err)

	require.NotNil(t, New(&Config{}, testLog()))
}

// ---- getHTTPClient / plainHTTPClient ---------------------------------------

func TestGetHTTPClient(t *testing.T) {
	t.Run("empty service URL errors", func(t *testing.T) {
		_, err := getHTTPClient(nil, "")
		require.Error(t, err)
	})

	t.Run("plain http URL yields plain client", func(t *testing.T) {
		c, err := getHTTPClient(nil, "http://localhost:8080")
		require.NoError(t, err)
		require.NotNil(t, c)
	})

	t.Run("unparseable URL falls back to plain client", func(t *testing.T) {
		// Fill fails (missing scheme) -> plainHTTPClient, not an error.
		c, err := getHTTPClient(nil, "localhost-no-scheme")
		require.NoError(t, err)
		require.NotNil(t, c)
	})

	t.Run("dmsg URL without client errors", func(t *testing.T) {
		pk, _ := validKeys(t)
		_, err := getHTTPClient(nil, "dmsg://"+pk.Hex()+":80")
		require.Error(t, err)
	})
}

func TestPlainHTTPClient(t *testing.T) {
	c := plainHTTPClient()
	require.NotNil(t, c)
	tr, ok := c.Transport.(*http.Transport)
	require.True(t, ok)
	require.True(t, tr.DisableKeepAlives)
}

// ---- Run: early error paths ------------------------------------------------

func TestRun_ConfigPathMissing(t *testing.T) {
	// A non-empty config_path that doesn't exist makes LoadFile fail before
	// any node is created.
	svc := New(&Config{ConfigPath: filepath.Join(t.TempDir(), "nope.json")}, testLog())
	err := svc.Run(canceledCtx())
	require.Error(t, err)
}

func TestRun_MissingKeys(t *testing.T) {
	// Inline config with null PK/SK is rejected before node creation.
	svc := New(&Config{}, testLog())
	err := svc.Run(canceledCtx())
	require.Error(t, err)
	require.Contains(t, err.Error(), "public_key and secret_key required")
}

// ---- Run: full path over an in-memory dmsg network -------------------------

// newDmsgDiscovery brings up an in-memory dmsg-discovery (served over HTTP via
// httptest) plus a single dmsg server registered against it, so that
// router.NewNode can establish a real session and become Ready without
// reaching the public network. Returns the discovery URL.
func newDmsgDiscovery(t *testing.T) string {
	t.Helper()
	log := testLog()

	// HTTP discovery backed by an in-memory store, in test mode (no auth).
	disco := discapi.New(log, discstore.NewMock(), discmetrics.NewEmpty(),
		true, false, false, "", "", 0)
	httpSrv := httptest.NewServer(disco)
	t.Cleanup(httpSrv.Close)

	// A dmsg server that registers itself into the discovery over HTTP and
	// serves sessions on a local TCP listener.
	srvPK, srvSK := cipher.GenerateKeyPair()
	dc := disc.NewHTTP(httpSrv.URL, &http.Client{}, log)
	srv := dmsg.NewServer(srvPK, srvSK, dc, &dmsg.ServerConfig{MaxSessions: 100}, nil)
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	go func() { _ = srv.Serve(lis, "") }() //nolint:errcheck
	t.Cleanup(func() { _ = srv.Close() })  //nolint:errcheck

	select {
	case <-srv.Ready():
	case <-time.After(10 * time.Second):
		t.Fatal("dmsg server did not become ready")
	}
	return httpSrv.URL
}

func TestRun_FullPathWithCascadeAndHealth(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping dmsg network integration test in -short mode")
	}
	discURL := newDmsgDiscovery(t)

	pk, sk := validKeys(t)
	cfg := &Config{Tag: "sn_it", MetricsAddr: "127.0.0.1:0"}
	cfg.SetupConfig.PK = pk
	cfg.SetupConfig.SK = sk
	cfg.SetupConfig.Dmsg.Discovery = discURL
	cfg.SetupConfig.Dmsg.SessionsCount = 1
	// A TPD URL exercises the discovery-client branch of startCascade; the URL
	// only needs to be constructible (no live TPD is contacted during Run).
	cfg.SetupConfig.TransportDiscovery = discURL
	// Enable both cascade (transport manager) and the DMSG health surface so
	// startCascade and startDMSGHealth both execute. The (unreachable) AR URL
	// keeps the STCPR client's resolver non-nil so it logs bind failures
	// instead of panicking.
	cfg.SetupConfig.Transport = &router.SetupTransportConfig{
		STCPRAddr:       "127.0.0.1:0",
		AddressResolver: "http://127.0.0.1:1",
	}
	cfg.SetupConfig.Cascade = &router.CascadeConfig{}

	svc := New(cfg, testLog())

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	// Give Run time to create the node, start cascade + health goroutines,
	// and reach sn.Serve, then cancel and assert it unwinds cleanly.
	time.Sleep(2 * time.Second)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(15 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}
