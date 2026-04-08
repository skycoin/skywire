package dmsgserver_test

import (
	"encoding/json"
	"os"
	"path/filepath"
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
