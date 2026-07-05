// Package stun — pkg/services/stun/stun_test.go: covers the config
// plumbing (ParseBlock / factory / New), the registration init, and the
// Run loop. Run's setup (default ports/tag, log level, server build) is
// driven to completion by using an unresolvable IP so srv.Start fails
// fast at address resolution — exercising every line up to the error
// return without binding real UDP sockets. The clean-shutdown return
// needs two distinct bindable IPs and is not unit-tested.
package stun

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
)

func testLog() *logging.Logger { return logging.MustGetLogger("stun-test") }

func TestInitRegistersFactory(t *testing.T) {
	f, ok := services.Lookup(Type)
	if !ok {
		t.Fatalf("service type %q not registered", Type)
	}
	if f == nil {
		t.Fatal("registered factory is nil")
	}
}

func TestParseBlock(t *testing.T) {
	t.Run("valid block with framing keys", func(t *testing.T) {
		raw := []byte(`{"type":"stun-server","name":"s1","primary_ip":"1.2.3.4","secondary_ip":"1.2.3.5","port":3478,"alt_port":3479}`)
		cfg, err := ParseBlock(raw)
		require.NoError(t, err)
		require.Equal(t, "1.2.3.4", cfg.PrimaryIP)
		require.Equal(t, "1.2.3.5", cfg.SecondaryIP)
		require.Equal(t, 3478, cfg.Port)
		require.Equal(t, 3479, cfg.AltPort)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := ParseBlock([]byte(`{not json`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestFactory(t *testing.T) {
	t.Run("valid raw block", func(t *testing.T) {
		svc, err := factory(json.RawMessage(`{"primary_ip":"1.2.3.4","secondary_ip":"1.2.3.5"}`), testLog())
		require.NoError(t, err)
		require.NotNil(t, svc)
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

func TestRunRequiresBothIPs(t *testing.T) {
	t.Run("missing secondary", func(t *testing.T) {
		err := New(&Config{PrimaryIP: "1.2.3.4"}, testLog()).(*service).Run(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "required")
	})
	t.Run("missing primary", func(t *testing.T) {
		err := New(&Config{SecondaryIP: "1.2.3.5"}, testLog()).(*service).Run(context.Background())
		require.Error(t, err)
		require.Contains(t, err.Error(), "required")
	})
}

func TestRunStartFailsWithDefaults(t *testing.T) {
	// An unresolvable IP makes srv.Start fail at address resolution, so
	// Run executes all of its setup (default port/alt_port/tag, no log
	// level) before returning the wrapped start error. No sockets bind.
	cfg := &Config{PrimaryIP: "256.256.256.256", SecondaryIP: "256.256.256.256"}
	err := New(cfg, testLog()).(*service).Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "stun-server: start")
}

func TestRunStartFailsWithExplicitConfig(t *testing.T) {
	// Same unresolvable-IP path, but with explicit port/alt_port/tag and
	// a log level set — exercises the log-level branch and the
	// explicit-value (non-default) paths.
	cfg := &Config{
		PrimaryIP:   "256.256.256.256",
		SecondaryIP: "256.256.256.256",
		Port:        15000,
		AltPort:     15001,
		Tag:         "custom-stun",
		LogLevel:    "debug",
	}
	err := New(cfg, testLog()).(*service).Run(context.Background())
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "stun-server: start"))
}

func TestRunInvalidLogLevelIgnored(t *testing.T) {
	// A bad log level is swallowed (LevelFromString errors → no SetLevel);
	// Run still proceeds and fails at start.
	cfg := &Config{PrimaryIP: "256.256.256.256", SecondaryIP: "256.256.256.256", LogLevel: "not-a-level"}
	err := New(cfg, testLog()).(*service).Run(context.Background())
	require.Error(t, err)
}
