// Package dmsgsrv — pkg/services/dmsgsrv/dmsgsrv_test.go: covers the
// config plumbing (ParseBlock / LoadFile / factory / New), the
// registration init, the validation branches of Run that return before
// any networking starts, and direct exercises of the dmsg-side helpers
// (buildTransitDmsg / newDmsgOnly / serveDmsgSurfaces / serveRouteSetup)
// driven with short-lived contexts so the real dmsg primitives spin up
// and tear down without reaching the network.
package dmsgsrv

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/skyenv"
)

func testLog() *logging.Logger { return logging.MustGetLogger("dmsgsrv-test") }

// --- registration ----------------------------------------------------

func TestInitRegistersFactory(t *testing.T) {
	f, ok := services.Lookup(Type)
	if !ok {
		t.Fatalf("service type %q not registered", Type)
	}
	if f == nil {
		t.Fatal("registered factory is nil")
	}
}

// --- ParseBlock ------------------------------------------------------

func TestParseBlock(t *testing.T) {
	t.Run("valid block with framing keys", func(t *testing.T) {
		raw := []byte(`{"type":"dmsg-server","name":"srv1","local_address":"//127.0.0.1:8081","auth_passphrase":"secret","metrics_addr":":9090"}`)
		cfg, err := ParseBlock(raw)
		if err != nil {
			t.Fatalf("ParseBlock: %v", err)
		}
		if cfg.LocalAddress != "//127.0.0.1:8081" {
			t.Errorf("LocalAddress = %q", cfg.LocalAddress)
		}
		if cfg.AuthPassphrase != "secret" {
			t.Errorf("AuthPassphrase = %q", cfg.AuthPassphrase)
		}
		if cfg.MetricsAddr != ":9090" {
			t.Errorf("MetricsAddr = %q", cfg.MetricsAddr)
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := ParseBlock([]byte(`{not json`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

// --- LoadFile --------------------------------------------------------

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(dir, "ok.json")
		writeFile(t, path, `{"local_address":"//127.0.0.1:8085","max_sessions":42}`)
		cfg, err := LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		if cfg.MaxSessions != 42 || cfg.LocalAddress != "//127.0.0.1:8085" {
			t.Errorf("unexpected config: %+v", cfg)
		}
	})

	t.Run("missing file", func(t *testing.T) {
		if _, err := LoadFile(filepath.Join(dir, "nope.json")); err == nil {
			t.Fatal("expected read error for missing file")
		}
	})

	t.Run("invalid JSON", func(t *testing.T) {
		path := filepath.Join(dir, "bad.json")
		writeFile(t, path, `{broken`)
		if _, err := LoadFile(path); err == nil {
			t.Fatal("expected parse error for malformed file")
		}
	})
}

// --- factory + New ---------------------------------------------------

func TestFactory(t *testing.T) {
	t.Run("valid raw block", func(t *testing.T) {
		svc, err := factory(json.RawMessage(`{"local_address":"//127.0.0.1:8081"}`), testLog())
		if err != nil {
			t.Fatalf("factory: %v", err)
		}
		if svc == nil {
			t.Fatal("factory returned nil service")
		}
	})

	t.Run("invalid raw block", func(t *testing.T) {
		if _, err := factory(json.RawMessage(`{bad`), testLog()); err == nil {
			t.Fatal("expected factory error for malformed block")
		}
	})
}

func TestNew(t *testing.T) {
	svc := New(&Config{}, testLog())
	if svc == nil {
		t.Fatal("New returned nil")
	}
	if _, ok := svc.(*service); !ok {
		t.Fatalf("New returned %T, want *service", svc)
	}
}

// --- Run validation branches (no networking) -------------------------

func TestRunConfigPathError(t *testing.T) {
	svc := New(&Config{ConfigPath: "/no/such/dmsg-server-config.json"}, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil {
		t.Fatal("expected error when ConfigPath cannot be read")
	}
}

func TestRunLocalAddressParseError(t *testing.T) {
	// ":8081" has no scheme → url.Parse fails → Run returns the
	// parse-local_address error before any networking.
	svc := New(&Config{Config: dmsgserver.Config{LocalAddress: ":8081"}}, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "local_address") {
		t.Fatalf("expected local_address parse error, got %v", err)
	}
}

func TestRunLocalAddressPortError(t *testing.T) {
	// "//127.0.0.1" parses but has no port → strconv.Atoi fails.
	svc := New(&Config{Config: dmsgserver.Config{LocalAddress: "//127.0.0.1"}}, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "port") {
		t.Fatalf("expected local_address port error, got %v", err)
	}
}

func TestRunNoDiscoveryDmsg(t *testing.T) {
	// A legacy Discovery without a discovery_dmsg PK exercises the full
	// early Run path (MaxSessions default, HTTPAddress derivation,
	// metrics, router, ServerAPI, peer + deployment folding) and then
	// fails the strict dmsg-only check — all without opening a socket.
	svc := New(&Config{Config: dmsgserver.Config{
		Discovery:    "http://disc.example.com",
		LocalAddress: "//127.0.0.1:8081",
	}}, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "discovery_dmsg") {
		t.Fatalf("expected discovery_dmsg error, got %v", err)
	}
}

// --- dmsg-side helpers (direct, short-lived) -------------------------

// newTestService builds a service with a fresh keypair and a single
// dmsg deployment whose discovery PK is valid (so PKFromDmsgURL works).
func newTestService(t *testing.T) (*service, []dmsgserver.Deployment, []cipher.PubKey) {
	t.Helper()
	pk, sk := cipher.GenerateKeyPair()
	discPK, _ := cipher.GenerateKeyPair()
	cfg := &Config{Config: dmsgserver.Config{
		PubKey:        pk,
		SecKey:        sk,
		PublicAddress: "127.0.0.1:8081",
		MaxSessions:   100,
		Discovery:     "http://disc.example.com",
		DiscoveryDmsg: "dmsg://" + discPK.Hex() + ":80",
	}}
	svc := &service{cfg: cfg, log: testLog()}
	deployments := []dmsgserver.Deployment{{
		Discovery:     cfg.Discovery,
		DiscoveryDmsg: cfg.DiscoveryDmsg,
	}}
	return svc, deployments, []cipher.PubKey{discPK}
}

func TestBuildTransitDmsg(t *testing.T) {
	svc, deployments, discPKs := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	dmsgC, dClient, closeFn := svc.buildTransitDmsg(ctx, deployments, discPKs)
	defer closeFn()
	if dmsgC == nil {
		t.Error("nil dmsg client")
	}
	if dClient == nil {
		t.Error("nil disc client")
	}
}

func TestNewDmsgOnly(t *testing.T) {
	svc, deployments, discPKs := newTestService(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	dmsgC, _, closeFn := svc.buildTransitDmsg(ctx, deployments, discPKs)
	defer closeFn()

	client := newDmsgOnly(dmsgC, discPKs[0], testLog())
	if client == nil {
		t.Fatal("newDmsgOnly returned nil disc.APIClient")
	}
}

func TestServeDmsgSurfaces(t *testing.T) {
	svc, deployments, discPKs := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	dmsgC, dClient, closeFn := svc.buildTransitDmsg(ctx, deployments, discPKs)
	defer closeFn()

	done := make(chan struct{})
	go func() {
		defer close(done)
		// serveDmsgSurfaces blocks on <-ctx.Done(); canceling below
		// makes it return after wiring up the health/debug surfaces.
		svc.serveDmsgSurfaces(ctx, cancel, dmsgC, dClient)
	}()

	// Give it a moment to set up the health mux + background listener,
	// then tear it down.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveDmsgSurfaces did not return after cancel")
	}
}

func TestServeRouteSetup(t *testing.T) {
	svc, deployments, discPKs := newTestService(t)
	ctx, cancel := context.WithCancel(t.Context())
	dmsgC, _, closeFn := svc.buildTransitDmsg(ctx, deployments, discPKs)
	done := make(chan struct{})
	go func() {
		defer close(done)
		// Listens on the dmsg setup port and accepts streams until the
		// client is closed (AcceptStream then errors with ErrEntityClosed).
		svc.serveRouteSetup(ctx, dmsgC)
	}()

	time.Sleep(100 * time.Millisecond)
	cancel()
	closeFn() // closing the client unblocks AcceptStream → handler returns

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("serveRouteSetup did not return after client close")
	}
	_ = skyenv.DmsgSetupPort // referenced for clarity of intent
	_ = dmsg.DefaultDmsgHTTPPort
}

// NOTE: Run's full happy path (lines past the strict dmsg-only check —
// dmsg server start, HTTP API listener, health/debug surfaces) is
// intentionally NOT covered by a live test. Run blocks on
// <-runCtx.Done() while serving, and its deferred shutdown calls
// (*dmsg.Server).Close → delEntry(context.Background()) with NO timeout,
// which deregisters from the discovery over dmsg. Offline (no real
// discovery/peer) that DELETE never resolves and Close hangs forever, so
// the server cannot tear down in a unit-test environment. Covering it
// would require a full live dmsg harness (real discovery + reachable
// peers) — high-cost and brittle. The validation branches above plus the
// direct buildTransitDmsg / serveDmsgSurfaces / serveRouteSetup tests
// exercise everything reachable without that infrastructure.

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
