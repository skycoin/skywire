// Package rf — pkg/services/rf/rf_test.go: covers the config plumbing
// (LoadFile / ParseBlock / dmsgDiscEntries), the registration init,
// factory / New, and the Run loop. Run is driven in its in-memory,
// http-only configuration (cfg.Testing=true, no SecKey) so the transport
// store stays in-process and svcmode.Start brings up only the HTTP
// listener — no redis and no dmsg networking — letting the full
// happy-path execute and return cleanly on context cancel. The redis
// store path and the mode-validation error are covered by dedicated
// error-branch tests.
package rf

import (
	"context"
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

func testLog() *logging.Logger { return logging.MustGetLogger("rf-test") }

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
		raw := []byte(`{"type":"route-finder","name":"rf1","addr":":9092","redis":"redis://localhost:6379","mode":"http"}`)
		cfg, err := ParseBlock(raw)
		if err != nil {
			t.Fatalf("ParseBlock: %v", err)
		}
		if cfg.Addr != ":9092" || cfg.Redis != "redis://localhost:6379" || cfg.Mode != "http" {
			t.Errorf("unexpected config: %+v", cfg)
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

	t.Run("valid file sets Path", func(t *testing.T) {
		path := filepath.Join(dir, "ok.json")
		writeFile(t, path, `{"addr":":9092","tag":"rf"}`)
		cfg, err := LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		if cfg.Addr != ":9092" {
			t.Errorf("Addr = %q", cfg.Addr)
		}
		if cfg.Path != path {
			t.Errorf("Path = %q, want %q", cfg.Path, path)
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

	t.Run("unknown field rejected", func(t *testing.T) {
		path := filepath.Join(dir, "unknown.json")
		writeFile(t, path, `{"addr":":9092","bogus_field":true}`)
		if _, err := LoadFile(path); err == nil {
			t.Fatal("expected error for unknown field")
		}
	})
}

// --- dmsgDiscEntries -------------------------------------------------

func TestDmsgDiscEntries(t *testing.T) {
	t.Run("empty falls back to embedded prod servers", func(t *testing.T) {
		got := dmsgDiscEntries(nil)
		if len(got) != len(dmsg.Prod.DmsgServers) {
			t.Errorf("empty input → %d entries, want prod set (%d)", len(got), len(dmsg.Prod.DmsgServers))
		}
	})

	t.Run("non-empty copies entries and skips nils", func(t *testing.T) {
		e1 := &disc.Entry{Static: cipher.PubKey{0x02}}
		e2 := &disc.Entry{Static: cipher.PubKey{0x03}}
		got := dmsgDiscEntries([]*disc.Entry{e1, nil, e2})
		if len(got) != 2 {
			t.Fatalf("got %d entries, want 2 (nil skipped)", len(got))
		}
		if got[0].Static != e1.Static || got[1].Static != e2.Static {
			t.Errorf("entries not copied in order: %+v", got)
		}
	})
}

// --- factory + New ---------------------------------------------------

func TestFactory(t *testing.T) {
	t.Run("valid raw block", func(t *testing.T) {
		svc, err := factory(json.RawMessage(`{"addr":":9092"}`), testLog())
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

// --- Run happy path (in-memory, http-only) ---------------------------

func TestRunInMemoryHTTPLifecycle(t *testing.T) {
	pk, _ := cipher.GenerateKeyPair()   // PubKey set (no SecKey) → dmsgAddr computed, http-only mode
	wlPK, _ := cipher.GenerateKeyPair() // survey-whitelist entry

	cfg := &Config{
		PubKey:          pk,
		Addr:            freeTCPAddr(t),
		Testing:         true, // in-memory transport store
		LogLevel:        "info",
		TestEnvironment: true,                  // survey-whitelist test branch
		SurveyWhitelist: []cipher.PubKey{wlPK}, // survey-whitelist override branch
	}
	svc := New(cfg, testLog()).(*service)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	// Give the listener a moment to come up, then cancel.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Run returned error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

// --- Run error branches ----------------------------------------------

func TestRunInvalidMode(t *testing.T) {
	// Testing=true keeps the store in-memory so Run reaches the mode
	// resolution, where an unknown mode string is rejected.
	cfg := &Config{Testing: true, Mode: "bogus-mode"}
	svc := New(cfg, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "invalid mode") {
		t.Fatalf("expected invalid mode error, got %v", err)
	}
}

func TestRunRedisStoreFails(t *testing.T) {
	// Non-Testing config uses the redis store; pointing at a closed port
	// makes store.New fail, exercising the redis path (incl. the
	// scheme-prefix branch, since the value has no redis:// prefix) and
	// the init-store error branch. The redis store retries until the
	// context deadline, so bound it tightly.
	cfg := &Config{Redis: closedHostPort(t)}
	svc := New(cfg, testLog()).(*service)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := svc.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "init store") {
		t.Fatalf("expected init store error, got %v", err)
	}
}

// --- helpers ---------------------------------------------------------

func freeTCPAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve tcp port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func closedHostPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
