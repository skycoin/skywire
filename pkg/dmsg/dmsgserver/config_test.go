package dmsgserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/dmsg/dmsgserver"
)

func TestGenerateDefaultConfig(t *testing.T) {
	var c dmsgserver.Config
	dmsgserver.GenerateDefaultConfig(&c)

	assert.Equal(t, dmsgserver.DefaultConfigPath, c.Path)
	assert.False(t, c.PubKey.Null(), "PubKey should be populated")
	assert.False(t, c.SecKey.Null(), "SecKey should be populated")
	assert.NotEmpty(t, c.Discovery, "Discovery URL should be set")
	assert.Equal(t, "127.0.0.1:8081", c.PublicAddress)
	assert.Equal(t, ":8081", c.LocalAddress)
	assert.Equal(t, ":8082", c.HTTPAddress)
	assert.Equal(t, "info", c.LogLevel)
	assert.Equal(t, 2048, c.MaxSessions)
}

func TestConfigFlush(t *testing.T) {
	var c dmsgserver.Config
	dmsgserver.GenerateDefaultConfig(&c)

	tmpDir := t.TempDir()
	c.Path = filepath.Join(tmpDir, "test_config.json")

	log := logging.MustGetLogger("test")
	err := c.Flush(log)
	require.NoError(t, err)

	data, err := os.ReadFile(c.Path)
	require.NoError(t, err)

	var loaded dmsgserver.Config
	err = json.Unmarshal(data, &loaded)
	require.NoError(t, err)

	assert.Equal(t, c.PubKey, loaded.PubKey)
	assert.Equal(t, c.SecKey, loaded.SecKey)
	assert.Equal(t, c.Discovery, loaded.Discovery)
	assert.Equal(t, c.PublicAddress, loaded.PublicAddress)
	assert.Equal(t, c.LocalAddress, loaded.LocalAddress)
	assert.Equal(t, c.HTTPAddress, loaded.HTTPAddress)
	assert.Equal(t, c.LogLevel, loaded.LogLevel)
	assert.Equal(t, c.MaxSessions, loaded.MaxSessions)
}

func TestDefaultConfigPath(t *testing.T) {
	assert.Equal(t, "config.json", dmsgserver.DefaultConfigPath)
}

// TestWSTLS_OffByDefault locks the safety contract for the built-in autocert wss
// listener: it is opt-in (ws_tls_address empty unless explicitly set), so every
// existing dmsg server is unaffected, and the JSON tags round-trip.
func TestWSTLS_OffByDefault(t *testing.T) {
	var c dmsgserver.Config
	dmsgserver.GenerateDefaultConfig(&c)
	if c.WSTLSAddress != "" {
		t.Fatalf("ws_tls_address must default to empty (off); got %q", c.WSTLSAddress)
	}

	// Set → emitted under the documented JSON keys.
	set, err := json.Marshal(dmsgserver.Config{WSTLSAddress: ":443", WSTLSCacheDir: "/var/lib/dmsg-autocert"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(set), `"ws_tls_address":":443"`) ||
		!strings.Contains(string(set), `"ws_tls_cache_dir":"/var/lib/dmsg-autocert"`) {
		t.Fatalf("ws_tls fields not emitted under expected keys: %s", set)
	}

	// Unset → omitted (omitempty), so existing configs gain no new keys.
	zb, err := json.Marshal(dmsgserver.Config{})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(zb), "ws_tls_address") {
		t.Fatalf("ws_tls_address must be omitempty; got %s", zb)
	}
}
