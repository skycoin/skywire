// Package commands cmd/dmsg/dmsg-discovery/commands/dmsg-discovery_test.go
//
// These tests cover the package's pure/deterministic logic: the comma-list
// splitter, the partial-config merge, the flag+file config assembly
// (buildConfig), and the help-text example generator. They do NOT cover the
// RootCmd.Run handler (it starts the discovery service against redis/dmsg and
// Fatals on error) or Execute (process entrypoint).
package commands

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/services"
	"github.com/skycoin/skywire/pkg/services/dmsgdisc"
)

// resetGlobals clears the package-level flag vars that buildConfig reads, so a
// test starts from a known state and does not leak into later tests.
func resetGlobals(t *testing.T) {
	t.Helper()
	addr, redisURL, whitelistKeys, officialServers = "", "", "", ""
	dmsgServerType, mode, authPassphrase, pprofMode, pprofAddr = "", "", "", "", ""
	configPath, keyFile = "", ""
	entryTimeout, dmsgPort = 0, 0
	testMode, enableLoadTesting, testEnvironment = false, false, false
	sk = cipher.SecKey{}
	t.Cleanup(func() {
		configPath, keyFile = "", ""
		sk = cipher.SecKey{}
	})
}

// TestCommaSplit covers the comma-list splitter, including trimming and the
// dropping of empty segments.
func TestCommaSplit(t *testing.T) {
	assert.Nil(t, commaSplit(""))
	assert.Equal(t, []string{"a", "b", "c"}, commaSplit("a,b,c"))
	assert.Equal(t, []string{"a", "b"}, commaSplit("  a , , b  "))
	assert.Equal(t, []string{"solo"}, commaSplit("solo"))
	// Only the empty string returns nil; an all-whitespace input returns a
	// non-nil but empty slice.
	assert.Empty(t, commaSplit("  ,  , "))
	assert.Nil(t, commaSplit(""))
}

// TestMergeFile verifies non-zero src fields override dst and zero fields are
// left untouched.
func TestMergeFile(t *testing.T) {
	t.Run("all fields override", func(t *testing.T) {
		_, srcSK := cipher.GenerateKeyPair()
		dst := &dmsgdisc.Config{Addr: "flag-addr", Redis: "flag-redis"}
		src := &dmsgdisc.Config{
			SecKey:            srcSK,
			Addr:              "file-addr",
			Redis:             "file-redis",
			DmsgPort:          81,
			EntryTimeout:      services.Duration(time.Minute),
			Mode:              "dual",
			AuthPassphrase:    "secret",
			OfficialServers:   []string{"pk1"},
			DmsgServerType:    "type",
			TestMode:          true,
			EnableLoadTesting: true,
			TestEnvironment:   true,
			Whitelist:         []string{"wl1"},
		}

		mergeFile(dst, src)

		assert.Equal(t, srcSK, dst.SecKey)
		assert.Equal(t, "file-addr", dst.Addr)
		assert.Equal(t, "file-redis", dst.Redis)
		assert.Equal(t, uint16(81), dst.DmsgPort)
		assert.Equal(t, services.Duration(time.Minute), dst.EntryTimeout)
		assert.Equal(t, "dual", dst.Mode)
		assert.Equal(t, "secret", dst.AuthPassphrase)
		assert.Equal(t, []string{"pk1"}, dst.OfficialServers)
		assert.Equal(t, "type", dst.DmsgServerType)
		assert.True(t, dst.TestMode)
		assert.True(t, dst.EnableLoadTesting)
		assert.True(t, dst.TestEnvironment)
		assert.Equal(t, []string{"wl1"}, dst.Whitelist)
	})

	t.Run("zero src leaves dst untouched", func(t *testing.T) {
		dst := &dmsgdisc.Config{Addr: "keep", Redis: "keep-redis", DmsgPort: 99}
		mergeFile(dst, &dmsgdisc.Config{})
		assert.Equal(t, "keep", dst.Addr)
		assert.Equal(t, "keep-redis", dst.Redis)
		assert.Equal(t, uint16(99), dst.DmsgPort)
	})
}

// TestBuildConfigFromFlags verifies buildConfig maps the flag globals into the
// resulting config.
func TestBuildConfigFromFlags(t *testing.T) {
	resetGlobals(t)
	addr = ":9090"
	redisURL = "redis://localhost:6379"
	dmsgPort = 80
	entryTimeout = time.Hour
	mode = "http"
	officialServers = "pkA, pkB"
	whitelistKeys = "wl1,wl2"
	testMode = true

	cfg, err := buildConfig()
	require.NoError(t, err)

	assert.Equal(t, ":9090", cfg.Addr)
	assert.Equal(t, "redis://localhost:6379", cfg.Redis)
	assert.Equal(t, uint16(80), cfg.DmsgPort)
	assert.Equal(t, services.Duration(time.Hour), cfg.EntryTimeout)
	assert.Equal(t, "http", cfg.Mode)
	assert.Equal(t, []string{"pkA", "pkB"}, cfg.OfficialServers)
	assert.Equal(t, []string{"wl1", "wl2"}, cfg.Whitelist)
	assert.True(t, cfg.TestMode)
}

// TestBuildConfigWithSecKey verifies an explicitly set secret key is carried
// into the config.
func TestBuildConfigWithSecKey(t *testing.T) {
	resetGlobals(t)
	_, secret := cipher.GenerateKeyPair()
	sk = secret

	cfg, err := buildConfig()
	require.NoError(t, err)
	assert.Equal(t, secret, cfg.SecKey)
}

// TestBuildConfigFileWins verifies that values from a --config file override the
// flag-derived values.
func TestBuildConfigFileWins(t *testing.T) {
	resetGlobals(t)
	addr = ":9090"
	redisURL = "redis://flag:6379"

	path := filepath.Join(t.TempDir(), "cfg.json")
	require.NoError(t, os.WriteFile(path, []byte(`{"addr":":7777","redis":"redis://file:6379","mode":"dual"}`), 0600))
	configPath = path

	cfg, err := buildConfig()
	require.NoError(t, err)
	assert.Equal(t, ":7777", cfg.Addr, "file addr should win")
	assert.Equal(t, "redis://file:6379", cfg.Redis, "file redis should win")
	assert.Equal(t, "dual", cfg.Mode)
}

// TestBuildConfigFileErrors verifies missing and malformed config files surface
// errors.
func TestBuildConfigFileErrors(t *testing.T) {
	t.Run("missing file", func(t *testing.T) {
		resetGlobals(t)
		configPath = filepath.Join(t.TempDir(), "nope.json")
		_, err := buildConfig()
		assert.Error(t, err)
	})

	t.Run("malformed file", func(t *testing.T) {
		resetGlobals(t)
		path := filepath.Join(t.TempDir(), "bad.json")
		require.NoError(t, os.WriteFile(path, []byte("{not json"), 0600))
		configPath = path
		_, err := buildConfig()
		assert.Error(t, err)
	})
}

// TestBuildConfigGeneratesKey verifies that pointing --keyfile at a missing path
// generates a key, writes it, and carries it into the config.
func TestBuildConfigGeneratesKey(t *testing.T) {
	resetGlobals(t)
	keyFile = filepath.Join(t.TempDir(), "key.txt")

	cfg, err := buildConfig()
	require.NoError(t, err)

	assert.False(t, cfg.SecKey.Null(), "a key should have been generated")
	assert.FileExists(t, keyFile, "the generated key should be persisted")
}

// TestGenerateExamples verifies the help-text generator produces a non-empty
// string covering the documented endpoints.
func TestGenerateExamples(t *testing.T) {
	out := generateExamples()
	require.NotEmpty(t, out)
	assert.Contains(t, out, "GET /health")
	assert.Contains(t, out, "GET /dmsg-discovery/all_servers")
	assert.Contains(t, out, "POST /dmsg-discovery/entry/")
}
