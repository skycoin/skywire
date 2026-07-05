// Package tps — tps_test.go: covers the config plumbing (LoadFile /
// ParseBlock / factory / New), the registration init, and the Run loop.
// Run is driven to completion with a valid keypair on an ephemeral port
// (Port=0) and torn down via context cancel — api.New only builds a lazy
// dmsg client, so no network is reached. The required-keys validation
// and the config-file error path are covered by dedicated tests.
package tps

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/transport-setup/config"
)

func testLog() *logging.Logger { return logging.MustGetLogger("tps-test") }

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
		raw := []byte(`{"type":"transport-setup","name":"ts1","port":9080,"tag":"ts","log_level":"info"}`)
		cfg, err := ParseBlock(raw)
		require.NoError(t, err)
		require.EqualValues(t, 9080, cfg.Port)
		require.Equal(t, "ts", cfg.Tag)
	})

	t.Run("invalid JSON", func(t *testing.T) {
		if _, err := ParseBlock([]byte(`{not json`)); err == nil {
			t.Fatal("expected error for malformed JSON")
		}
	})
}

func TestLoadFile(t *testing.T) {
	dir := t.TempDir()

	t.Run("valid file", func(t *testing.T) {
		path := filepath.Join(dir, "ok.json")
		writeFile(t, path, `{"port":9080}`)
		cfg, err := LoadFile(path)
		require.NoError(t, err)
		require.EqualValues(t, 9080, cfg.Port)
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

func TestFactory(t *testing.T) {
	t.Run("valid raw block", func(t *testing.T) {
		svc, err := factory(json.RawMessage(`{"port":9080}`), testLog())
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

func TestRunRequiresKeys(t *testing.T) {
	// No PK/SK (inline or via file) → the required-keys validation fails
	// before any networking. Also exercises the default-tag path.
	err := New(&Config{}, testLog()).(*service).Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "required")
}

func TestRunConfigPathError(t *testing.T) {
	cfg := &Config{ConfigPath: "/no/such/transport-setup-config.json"}
	err := New(cfg, testLog()).(*service).Run(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "read config")
}

func TestRunLifecycle(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	cfg := &Config{
		Config:   config.Config{PK: pk, SK: sk, Port: 0}, // ephemeral port
		Tag:      "ts-run",
		LogLevel: "info", // exercises the log-level branch
	}
	svc := New(cfg, testLog()).(*service)

	ctx, cancel := context.WithCancel(context.Background())
	errCh := make(chan error, 1)
	go func() { errCh <- svc.Run(ctx) }()

	// Give the HTTP + dmsg listeners a moment to come up, then cancel.
	time.Sleep(300 * time.Millisecond)
	cancel()

	select {
	case err := <-errCh:
		require.NoError(t, err)
	case <-time.After(10 * time.Second):
		t.Fatal("Run did not return after context cancel")
	}
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
