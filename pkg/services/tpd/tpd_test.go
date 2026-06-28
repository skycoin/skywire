// Package tpd tpd_test.go: unit tests for config loading (ParseBlock /
// LoadFile), the service factory + New, the pure dmsgDiscEntries helper,
// the aggregatorSink delegation wrappers, and the early-return / error
// paths of Run (invalid mode, listener start failure, and the canceled-ctx
// Testing path) that don't require a live redis or dmsg network.
package tpd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/httpauth"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/storeconfig"
	tpapi "github.com/skycoin/skywire/pkg/transport-discovery/api"
	tpdiscmetrics "github.com/skycoin/skywire/pkg/transport-discovery/metrics"
	"github.com/skycoin/skywire/pkg/transport-discovery/store"
)

func testLog() *logging.Logger { return logging.MustGetLogger("tpd_test") }

func canceledCtx() context.Context {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	return ctx
}

// runWithin runs fn in a goroutine and fails the test if it does not return
// within d. Used for Run paths that bring up background goroutines but should
// unwind promptly on a canceled context.
func runWithin(t *testing.T, d time.Duration, fn func() error) error {
	t.Helper()
	errCh := make(chan error, 1)
	go func() { errCh <- fn() }()
	select {
	case err := <-errCh:
		return err
	case <-time.After(d):
		t.Fatalf("Run did not return within %v", d)
		return nil
	}
}

// ---- config: ParseBlock ----------------------------------------------------

func TestParseBlock(t *testing.T) {
	// Unknown framing fields (type/name) are tolerated by ParseBlock.
	raw := []byte(`{"type":"transport-discovery","name":"x","addr":":9091","redis":"redis://localhost:6379","mode":"http","whitelist_keys":["abc"]}`)
	cfg, err := ParseBlock(raw)
	require.NoError(t, err)
	require.Equal(t, ":9091", cfg.Addr)
	require.Equal(t, "redis://localhost:6379", cfg.Redis)
	require.Equal(t, "http", cfg.Mode)
	require.Equal(t, []string{"abc"}, cfg.Whitelist)

	_, err = ParseBlock([]byte("{bad json"))
	require.Error(t, err)
}

// ---- config: LoadFile ------------------------------------------------------

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid", func(t *testing.T) {
		p := filepath.Join(dir, "ok.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"addr":":8888","mode":"dual","entry_timeout":"2m"}`), 0o600))
		cfg, err := LoadFile(p)
		require.NoError(t, err)
		require.Equal(t, ":8888", cfg.Addr)
		require.Equal(t, "dual", cfg.Mode)
		require.Equal(t, 2*time.Minute, cfg.EntryTimeout.Std())
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

	t.Run("malformed json", func(t *testing.T) {
		p := filepath.Join(dir, "malformed.json")
		require.NoError(t, os.WriteFile(p, []byte(`{not json`), 0o600))
		_, err := LoadFile(p)
		require.Error(t, err)
	})
}

// ---- factory / New ---------------------------------------------------------

func TestFactoryAndNew(t *testing.T) {
	svc, err := factory(json.RawMessage(`{"addr":":9091","mode":"http"}`), testLog())
	require.NoError(t, err)
	require.NotNil(t, svc)

	_, err = factory(json.RawMessage(`{bad`), testLog())
	require.Error(t, err)

	require.NotNil(t, New(&Config{}, testLog()))
}

// ---- pure helper: dmsgDiscEntries -----------------------------------------

func TestDmsgDiscEntries(t *testing.T) {
	// Empty/nil config falls back to the embedded prod keyring.
	out := dmsgDiscEntries(nil)
	require.Equal(t, dmsg.Prod.DmsgServers, out)

	// Non-empty config is copied through (value type), with nil entries
	// skipped.
	srvPK, _ := cipher.GenerateKeyPair()
	e := &disc.Entry{Static: srvPK, Server: &disc.Server{Address: "127.0.0.1:8080", ServerType: "public"}}
	got := dmsgDiscEntries([]*disc.Entry{e, nil})
	require.Len(t, got, 1)
	require.Equal(t, srvPK, got[0].Static)
}

// ---- aggregatorSink delegation --------------------------------------------

func newTestAPI(t *testing.T) *tpapi.API {
	t.Helper()
	st, err := store.New(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, time.Minute, testLog())
	require.NoError(t, err)
	nonce, err := httpauth.NewNonceStore(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, redisPrefix)
	require.NoError(t, err)
	return tpapi.New(testLog(), st, nonce, false, tpdiscmetrics.NewEmpty(), "", t.TempDir())
}

func TestAggregatorSink_Delegation(t *testing.T) {
	st, err := store.New(context.Background(), storeconfig.Config{Type: storeconfig.Memory}, time.Minute, testLog())
	require.NoError(t, err)
	sink := &aggregatorSink{Store: st, api: newTestAPI(t)}

	// RegisterTransportFromCXO rejects a nil entry via the API's guard.
	err = sink.RegisterTransportFromCXO(context.Background(), nil, cipher.PubKey{}, "v1")
	require.Error(t, err)

	// DeregisterTransportFromCXO of an unknown ID is an idempotent no-op.
	reporterPK, _ := cipher.GenerateKeyPair()
	require.NoError(t, sink.DeregisterTransportFromCXO(context.Background(), uuid.New(), reporterPK))
}

// ---- Run: error / early-return paths ---------------------------------------

func TestRun_InvalidMode(t *testing.T) {
	// An unrecognized mode makes svcmode.ResolveMode fail before any
	// listener is started.
	svc := New(&Config{Testing: true, Mode: "bogus", Addr: "127.0.0.1:0"}, testLog())
	err := runWithin(t, 10*time.Second, func() error { return svc.Run(context.Background()) })
	require.Error(t, err)
	require.Contains(t, err.Error(), "invalid mode")
}

func TestRun_DmsgModeNoSecKey(t *testing.T) {
	// mode=dmsg with a null secret key: svcmode.Start refuses to bring up
	// the dmsghttp listener, so Run returns the wrapped start error.
	svc := New(&Config{Testing: true, Mode: "dmsg"}, testLog())
	err := runWithin(t, 10*time.Second, func() error { return svc.Run(context.Background()) })
	require.Error(t, err)
	require.Contains(t, err.Error(), "start listeners")
}

func TestRun_TestingHTTPCanceledCtx(t *testing.T) {
	// Testing=true uses in-memory stores (no redis), mode=http with a null
	// SK means no dmsg bootstrap, and a canceled context makes the run loop
	// unwind immediately with nil.
	svc := New(&Config{Testing: true, Mode: "http", Addr: "127.0.0.1:0"}, testLog())
	err := runWithin(t, 10*time.Second, func() error { return svc.Run(canceledCtx()) })
	require.NoError(t, err)
}

func TestRun_WithUptimeDBAndMetrics(t *testing.T) {
	// Exercises the uptime-recorder bring-up branch (UptimeDB set) and the
	// VictoriaMetrics branch (MetricsAddr set), then unwinds on the canceled
	// context. Defaults Tag/Addr/StoreDataPath are also taken here.
	dir := t.TempDir()
	svc := New(&Config{
		Testing:       true,
		Mode:          "http",
		UptimeDB:      filepath.Join(dir, "uptime.db"),
		MetricsAddr:   "127.0.0.1:0",
		StoreDataPath: filepath.Join(dir, "bandwidth"),
		Whitelist:     []string{"  pk1  ", ""},
		LogLevel:      "info",
	}, testLog())
	err := runWithin(t, 10*time.Second, func() error { return svc.Run(canceledCtx()) })
	require.NoError(t, err)
}

func TestRun_WithSecKeyDerivesPubKey(t *testing.T) {
	// Only a secret key is supplied: Run derives the public key from it and
	// brings up dmsg, which on a canceled context unwinds promptly. We don't
	// assert on the error value (bootstrap may or may not surface one); we
	// only require that Run returns rather than hangs.
	_, sk := cipher.GenerateKeyPair()
	svc := New(&Config{Testing: true, Mode: "http", SecKey: sk, Addr: "127.0.0.1:0"}, testLog())
	_ = runWithin(t, 15*time.Second, func() error { return svc.Run(canceledCtx()) }) //nolint
}
