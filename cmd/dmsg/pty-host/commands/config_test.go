// Package commands cmd/dmsg/pty-host/commands/config_test.go
//
// Covers the config-assembly helpers: env, flag, JSON-file and visor-config-SK
// sourcing. These read environment/flag globals/files with no network, so they
// are testable as-is. The RunE host-serving path and Execute are not.
package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/pty"
)

// TestFillConfigFromENV covers each env override plus the two parse-error paths.
func TestFillConfigFromENV(t *testing.T) {
	// Use a dedicated prefix so we never collide with the real DMSGPTY_* vars.
	old := envPrefix
	envPrefix = "TESTPTYHOST"
	t.Cleanup(func() { envPrefix = old })

	t.Run("all set", func(t *testing.T) {
		t.Setenv("TESTPTYHOST_DMSGDISC", "http://disc.example")
		t.Setenv("TESTPTYHOST_DMSGSESSIONS", "4")
		t.Setenv("TESTPTYHOST_DMSGPORT", "2222")
		t.Setenv("TESTPTYHOST_CLINET", "tcp")
		t.Setenv("TESTPTYHOST_CLIADDR", "127.0.0.1:9999")

		got, err := fillConfigFromENV(pty.Config{})
		require.NoError(t, err)
		assert.Equal(t, "http://disc.example", got.DmsgDisc)
		assert.Equal(t, 4, got.DmsgSessions)
		assert.Equal(t, uint16(2222), got.DmsgPort)
		assert.Equal(t, "tcp", got.CLINet)
		assert.Equal(t, "127.0.0.1:9999", got.CLIAddr)
	})

	t.Run("none set leaves config untouched", func(t *testing.T) {
		in := pty.Config{DmsgDisc: "keep", DmsgSessions: 9}
		got, err := fillConfigFromENV(in)
		require.NoError(t, err)
		assert.Equal(t, in, got)
	})

	t.Run("bad sessions", func(t *testing.T) {
		t.Setenv("TESTPTYHOST_DMSGSESSIONS", "notanumber")
		_, err := fillConfigFromENV(pty.Config{})
		assert.Error(t, err)
	})

	t.Run("bad port", func(t *testing.T) {
		t.Setenv("TESTPTYHOST_DMSGPORT", "70000")
		_, err := fillConfigFromENV(pty.Config{})
		assert.Error(t, err)
	})
}

// TestFillConfigFromFlags verifies only non-default flag values override the
// config.
func TestFillConfigFromFlags(t *testing.T) {
	// Snapshot and restore the package-level flag vars.
	od, os_, op, on, oa := dmsgDisc, dmsgSessions, dmsgPort, cliNet, cliAddr
	t.Cleanup(func() { dmsgDisc, dmsgSessions, dmsgPort, cliNet, cliAddr = od, os_, op, on, oa })

	t.Run("defaults leave config untouched", func(t *testing.T) {
		in := pty.Config{DmsgDisc: "orig", CLINet: "orig-net"}
		got := fillConfigFromFlags(in)
		assert.Equal(t, in, got)
	})

	t.Run("non-default flags override", func(t *testing.T) {
		dmsgDisc = "http://flagdisc"
		dmsgSessions = 11
		dmsgPort = 2300
		cliNet = "tcp"
		cliAddr = "127.0.0.1:1234"

		got := fillConfigFromFlags(pty.Config{})
		assert.Equal(t, "http://flagdisc", got.DmsgDisc)
		assert.Equal(t, 11, got.DmsgSessions)
		assert.Equal(t, uint16(2300), got.DmsgPort)
		assert.Equal(t, "tcp", got.CLINet)
		assert.Equal(t, "127.0.0.1:1234", got.CLIAddr)
	})
}

// TestLoadSKFromVisorConfig covers the happy path and every failure mode.
func TestLoadSKFromVisorConfig(t *testing.T) {
	oldSK := sk
	t.Cleanup(func() { sk = oldSK })

	write := func(t *testing.T, content string) string {
		t.Helper()
		p := filepath.Join(t.TempDir(), "visor.json")
		require.NoError(t, os.WriteFile(p, []byte(content), 0600))
		return p
	}

	t.Run("valid", func(t *testing.T) {
		sk = cipher.SecKey{}
		_, secret := cipher.GenerateKeyPair()
		require.NoError(t, loadSKFromVisorConfig(write(t, `{"sk":"`+secret.Hex()+`"}`)))
		assert.Equal(t, secret, sk)
	})

	t.Run("missing file", func(t *testing.T) {
		assert.Error(t, loadSKFromVisorConfig(filepath.Join(t.TempDir(), "nope.json")))
	})

	t.Run("malformed json", func(t *testing.T) {
		assert.Error(t, loadSKFromVisorConfig(write(t, `{not json`)))
	})

	t.Run("missing sk field", func(t *testing.T) {
		err := loadSKFromVisorConfig(write(t, `{"pk":"x"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "missing top-level 'sk'")
	})

	t.Run("invalid sk", func(t *testing.T) {
		err := loadSKFromVisorConfig(write(t, `{"sk":"not-hex"}`))
		require.Error(t, err)
		assert.Contains(t, err.Error(), "invalid sk")
	})
}

// TestConfigFromJSON covers the no-source default, a config-file merge, and the
// file error paths. Globals touched by the function are reset around each case.
func TestConfigFromJSON(t *testing.T) {
	oldStdin, oldPath, oldSK, oldPK, oldWL := confStdin, confPath, sk, pk, wl
	t.Cleanup(func() { confStdin, confPath, sk, pk, wl = oldStdin, oldPath, oldSK, oldPK, oldWL })

	reset := func() { confStdin, confPath, sk, pk, wl = false, "", cipher.SecKey{}, cipher.PubKey{}, nil }

	t.Run("no source applies default CLIAddr", func(t *testing.T) {
		reset()
		got, err := configFromJSON(pty.Config{})
		require.NoError(t, err)
		assert.Equal(t, pty.DefaultCLIAddr(), got.CLIAddr)
	})

	t.Run("file SK merges into config", func(t *testing.T) {
		reset()
		_, secret := cipher.GenerateKeyPair()
		p := filepath.Join(t.TempDir(), "c.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"sk":"`+secret.Hex()+`"}`), 0600))
		confPath = p

		got, err := configFromJSON(pty.Config{})
		require.NoError(t, err)
		assert.Equal(t, secret.Hex(), got.SK)
	})

	t.Run("missing file", func(t *testing.T) {
		reset()
		confPath = filepath.Join(t.TempDir(), "nope.json")
		_, err := configFromJSON(pty.Config{})
		assert.Error(t, err)
	})

	t.Run("invalid sk in file", func(t *testing.T) {
		reset()
		p := filepath.Join(t.TempDir(), "c.json")
		require.NoError(t, os.WriteFile(p, []byte(`{"sk":"not-hex"}`), 0600))
		confPath = p
		_, err := configFromJSON(pty.Config{})
		assert.Error(t, err)
	})
}
