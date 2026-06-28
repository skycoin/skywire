// Package sd — pkg/services/sd/sd_test.go: covers the config plumbing
// (LoadFile / ParseBlock / dmsgDiscEntries), the registration init,
// factory / New, and the validation branches of Run that return before
// the service touches a live store. Run's redis-, store-, and dmsg-
// dependent body (svcmode listeners, CXO publisher) needs a real redis +
// dmsg deployment and is not unit-tested here.
package sd

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

func testLog() *logging.Logger { return logging.MustGetLogger("sd-test") }

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
		raw := []byte(`{"type":"service-discovery","name":"sd1","addr":":9098","redis":"redis://localhost:6379","mode":"http"}`)
		cfg, err := ParseBlock(raw)
		if err != nil {
			t.Fatalf("ParseBlock: %v", err)
		}
		if cfg.Addr != ":9098" || cfg.Redis != "redis://localhost:6379" || cfg.Mode != "http" {
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
		writeFile(t, path, `{"addr":":9098","entry_timeout":"5m"}`)
		cfg, err := LoadFile(path)
		if err != nil {
			t.Fatalf("LoadFile: %v", err)
		}
		if cfg.Addr != ":9098" {
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
		// LoadFile uses DisallowUnknownFields (unlike the tolerant
		// ParseBlock), so a stray key is a hard error.
		path := filepath.Join(dir, "unknown.json")
		writeFile(t, path, `{"addr":":9098","bogus_field":true}`)
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
		svc, err := factory(json.RawMessage(`{"addr":":9098"}`), testLog())
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

// --- Run validation branches (no live store) -------------------------

func TestRunInvalidRedisURL(t *testing.T) {
	// A non-redis scheme makes redis.ParseURL fail, so Run returns before
	// any connection attempt. PubKey set → the sk-derivation branch is
	// skipped.
	pk, _ := cipher.GenerateKeyPair()
	svc := New(&Config{PubKey: pk, Redis: "ftp://localhost"}, testLog()).(*service)
	err := svc.Run(context.Background())
	if err == nil || !strings.Contains(err.Error(), "redis URL") {
		t.Fatalf("expected redis URL parse error, got %v", err)
	}
}

func TestRunRedisPingFails(t *testing.T) {
	// Valid URL pointing at a closed port: ParseURL succeeds, the ping
	// fails fast with connection-refused. SecKey-only config exercises
	// the pk = sk.PubKey() derivation branch.
	_, sk := cipher.GenerateKeyPair()
	addr := closedAddr(t)
	svc := New(&Config{SecKey: sk, Redis: "redis://" + addr}, testLog()).(*service)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	err := svc.Run(ctx)
	if err == nil || !strings.Contains(err.Error(), "redis ping failed") {
		t.Fatalf("expected redis ping error, got %v", err)
	}
}

// closedAddr returns a "host:port" on loopback with nothing listening.
func closedAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close() //nolint
	return addr
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
