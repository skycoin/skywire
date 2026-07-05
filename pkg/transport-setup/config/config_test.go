// Package config pkg/transport-setup/config/config_test.go: unit tests for the
// transport-setup config reader.
package config

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// nonExitingLogger returns a *logging.Logger whose Fatalf does not terminate the
// process, so the error branches of MustReadConfig can be exercised.
func nonExitingLogger() *logging.Logger {
	lr := logrus.New()
	lr.SetOutput(io.Discard)
	lr.ExitFunc = func(int) {}
	return &logging.Logger{FieldLogger: lr}
}

// TestMustReadConfig verifies a well-formed config file is decoded into a Config.
func TestMustReadConfig(t *testing.T) {
	pk, sk := cipher.GenerateKeyPair()
	want := Config{PK: pk, SK: sk, Port: 7070}

	raw, err := json.Marshal(want)
	require.NoError(t, err)

	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, raw, 0600))

	got := MustReadConfig(path, logging.MustGetLogger("config-test"))
	assert.Equal(t, want.PK, got.PK)
	assert.Equal(t, want.SK, got.SK)
	assert.Equal(t, uint16(7070), got.Port)
}

// TestMustReadConfigMissingFile verifies a missing file flows through the
// Fatalf guards (neutralized) and yields a zero-valued Config rather than
// terminating the process.
func TestMustReadConfigMissingFile(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "does-not-exist.json")
	got := MustReadConfig(missing, nonExitingLogger())
	assert.Equal(t, Config{}, got)
}

// TestMustReadConfigBadJSON verifies invalid JSON is reported via Fatalf
// (neutralized) and returns a zero-valued Config.
func TestMustReadConfigBadJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	require.NoError(t, os.WriteFile(path, []byte("{not valid json"), 0600))

	got := MustReadConfig(path, nonExitingLogger())
	assert.Equal(t, Config{}, got)
}
