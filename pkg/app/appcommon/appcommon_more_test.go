// Package appcommon pkg/app/appcommon/appcommon_more_test.go: unit tests for
// the Hello wire helpers, the ProcKey/ProcConfig helpers (run mode, env
// encoding, flag parsing, in-process conn registry, internal-start sync), and
// the bbolt log store hook surface (Write/Fire/Levels/Flush, NewProcLogger,
// TimestampFromLog).
package appcommon

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	log "github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/logging"
)

// ---- hello.go --------------------------------------------------------------

func TestHello_StringAndAllows(t *testing.T) {
	h := &Hello{ProcKey: RandProcKey(), EventSubs: map[string]bool{"tcp_dial": true}}
	require.Contains(t, h.String(), "proc_key")

	require.True(t, h.AllowsEventType("tcp_dial"))
	require.False(t, h.AllowsEventType("tcp_close"))

	// Nil subscription map → nothing allowed.
	require.False(t, (&Hello{}).AllowsEventType("tcp_dial"))
}

func TestHello_ReadWriteRoundTrip(t *testing.T) {
	in := Hello{
		ProcKey:    RandProcKey(),
		EgressNet:  "tcp",
		EgressAddr: "127.0.0.1:5",
		EventSubs:  map[string]bool{"tcp_dial": true},
	}

	var buf bytes.Buffer
	require.NoError(t, WriteHello(&buf, in))

	got, err := ReadHello(&buf)
	require.NoError(t, err)
	require.Equal(t, in, got)
}

func TestReadHello_Errors(t *testing.T) {
	// Truncated size prefix.
	_, err := ReadHello(bytes.NewReader([]byte{0x00}))
	require.Error(t, err)

	// Size prefix promises 10 bytes but body is short.
	_, err = ReadHello(bytes.NewReader([]byte{0x00, 0x0a, 0x01}))
	require.Error(t, err)

	// Valid size, but body isn't valid JSON.
	bad := []byte{0x00, 0x04, '{', 'x', 'x', 'x'}
	_, err = ReadHello(bytes.NewReader(bad))
	require.Error(t, err)
}

// ---- proc_config.go: ProcKey ----------------------------------------------

func TestProcKey_TextAndNull(t *testing.T) {
	k := RandProcKey()
	require.Len(t, k.String(), 32)
	require.False(t, k.Null())
	require.True(t, ProcKey{}.Null())

	text, err := k.MarshalText()
	require.NoError(t, err)

	var k2 ProcKey
	require.NoError(t, k2.UnmarshalText(text))
	require.Equal(t, k, k2)

	// Empty text → zero key.
	var k3 ProcKey
	require.NoError(t, k3.UnmarshalText(nil))
	require.True(t, k3.Null())

	// Invalid hex and wrong length both error.
	require.Error(t, k3.UnmarshalText([]byte("zzzz")))
	require.Error(t, k3.UnmarshalText([]byte("abcd")))
}

// ---- proc_config.go: ProcConfig helpers ------------------------------------

func dummyAppFunc(_ context.Context, _ []string) error { return nil }

func TestProcConfig_EffectiveRunMode(t *testing.T) {
	// No RunMode + no RunFunc → external (default).
	require.Equal(t, RunModeExternal, (&ProcConfig{}).EffectiveRunMode())
	// RunFunc present → internal (legacy heuristic).
	require.Equal(t, RunModeInternal, (&ProcConfig{RunFunc: dummyAppFunc}).EffectiveRunMode())
	// Explicit RunMode wins over the RunFunc heuristic.
	require.Equal(t, RunModeExternal, (&ProcConfig{RunMode: RunModeExternal, RunFunc: dummyAppFunc}).EffectiveRunMode())
}

func TestProcConfig_EnsureKey(t *testing.T) {
	c := &ProcConfig{}
	c.EnsureKey()
	require.False(t, c.ProcKey.Null())

	existing := c.ProcKey
	c.EnsureKey() // no-op when already set
	require.Equal(t, existing, c.ProcKey)
}

func TestProcConfig_EnvsAndEncode(t *testing.T) {
	c := &ProcConfig{AppName: "app", ProcEnvs: []string{"FOO=bar"}}
	envs := c.Envs()
	require.Contains(t, envs, "FOO=bar")

	var found bool
	for _, e := range envs {
		if len(e) > len(EnvProcConfig) && e[:len(EnvProcConfig)] == EnvProcConfig {
			found = true
		}
	}
	require.True(t, found, "PROC_CONFIG env should be appended")
}

func TestProcConfig_FlagHelpers(t *testing.T) {
	c := &ProcConfig{ProcArgs: []string{"-srv", "abc", "--passcode=xyz", "plain"}}

	require.True(t, c.ContainsFlag("srv"))
	require.True(t, c.ContainsFlag("passcode"))
	require.False(t, c.ContainsFlag("missing"))

	require.Equal(t, "abc", c.ArgVal("srv"))
	// ArgVal returns the following arg regardless of '=' fusion, so a
	// matched "--passcode=xyz" yields the next token ("plain").
	require.Equal(t, "plain", c.ArgVal("passcode"))
	require.Equal(t, "", c.ArgVal("missing"))
}

// ---- proc_config.go: ProcConfigFromEnv + internal-start sync ----------------

func TestProcConfigFromEnv_NotDefined(t *testing.T) {
	require.NoError(t, os.Unsetenv(EnvProcConfig))
	_, err := ProcConfigFromEnv()
	require.ErrorIs(t, err, ErrProcConfigEnvNotDefined)
}

func TestProcConfigFromEnv_InvalidJSON(t *testing.T) {
	t.Setenv(EnvProcConfig, "{not json")
	_, err := ProcConfigFromEnv()
	require.Error(t, err)
}

func TestProcConfigFromEnv_ValidSignalsConfigRead(t *testing.T) {
	key := RandProcKey()

	// Register a done channel as the internal-app launcher would.
	done := AcquireInternalAppStart(key)
	defer ReleaseInternalAppStart(key)

	conf := ProcConfig{AppName: "app", ProcKey: key}
	raw, err := json.Marshal(conf)
	require.NoError(t, err)
	t.Setenv(EnvProcConfig, string(raw))

	got, err := ProcConfigFromEnv()
	require.NoError(t, err)
	require.Equal(t, key, got.ProcKey)

	// ProcConfigFromEnv must have closed the done channel.
	select {
	case <-done:
	default:
		t.Fatal("config-read signal channel was not closed")
	}

	// signalConfigRead is idempotent on an already-closed channel.
	require.NotPanics(t, func() { signalConfigRead(key) })
}

func TestInProcessConnRegistry(t *testing.T) {
	key := RandProcKey()
	require.Nil(t, GetInProcessConn(key))

	a, b := net.Pipe()
	defer func() { _ = a.Close(); _ = b.Close() }() //nolint

	RegisterInProcessConn(key, a)
	require.Equal(t, a, GetInProcessConn(key))

	UnregisterInProcessConn(key)
	require.Nil(t, GetInProcessConn(key))
}

// ---- log_store.go ----------------------------------------------------------

func TestTimestampFromLog(t *testing.T) {
	line := "[2000-01-01T00:00:00.000000000Z] some message here"
	require.Contains(t, TimestampFromLog(line), "2000-01-01")
}

func TestNewProcLogger(t *testing.T) {
	conf := ProcConfig{AppName: "app", LogDBLoc: filepath.Join(t.TempDir(), "log.db")}
	mLog := logging.NewMasterLogger()

	appLog, store := NewProcLogger(conf, mLog)
	require.NotNil(t, appLog)
	require.NotNil(t, store)

	// Logging through appLog fires the bbolt store hook (Fire).
	require.NotPanics(t, func() { appLog.PackageLogger("x").Info("hello from app") })
}

func TestBBoltLogStore_HookSurface(t *testing.T) {
	store, err := NewBBoltLogStore(filepath.Join(t.TempDir(), "log.db"), "app")
	require.NoError(t, err)

	require.Equal(t, log.AllLevels, store.Levels())
	require.NoError(t, store.Flush())

	// Write: short buffer is rejected, a full line is stored.
	_, err = store.Write([]byte("short"))
	require.ErrorIs(t, err, io.ErrShortBuffer)

	line := []byte("[2000-01-01T00:00:00.000000000Z] a stored log line")
	n, err := store.Write(line)
	require.NoError(t, err)
	require.Equal(t, len(line), n)

	// Fire: a real logrus entry is persisted.
	entry := log.NewEntry(log.New())
	entry.Time = time.Now()
	entry.Level = log.InfoLevel
	entry.Message = "fired entry message that is sufficiently long"
	require.NoError(t, store.Fire(entry))
}
