// Package appserver pkg/app/appserver/proc_lifecycle_test.go: tests that drive
// the external-process Start/Stop lifecycle and the RPC gateway setter
// handlers.
package appserver

import (
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

func TestProcManager_StartExternalThenStop(t *testing.T) {
	// The external-app stop path on Windows is IPC-based: signalStop blocks on
	// ipcServerWg.Wait until the app dials back over the named-pipe IPC server,
	// which a generic binary like /bin/sleep never does — so Stop would hang.
	// On POSIX, /bin/sleep is killed via SIGINT. This test only exercises the
	// POSIX lifecycle; skip it on Windows (also, /bin/sleep doesn't exist there).
	if runtime.GOOS == "windows" {
		t.Skip("external-process SIGINT lifecycle is POSIX-only; Windows uses IPC shutdown")
	}

	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	conf := appcommon.ProcConfig{
		AppName:   "sleeper",
		ProcKey:   appcommon.RandProcKey(),
		BinaryLoc: "/bin/sleep",
		ProcArgs:  []string{"30"},
	}

	pid, err := m.Start(conf)
	require.NoError(t, err)
	require.NotZero(t, pid)

	// Starting the same app again must be rejected.
	_, err = m.Start(conf)
	require.ErrorIs(t, err, ErrAppAlreadyStarted)

	// Stop signals the process and pops it from the registry.
	require.NoError(t, m.Stop("sleeper"))

	_, err = m.Stats("sleeper")
	require.ErrorIs(t, err, ErrNoSuchApp)
}

func TestProcManager_StartBadBinary(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	conf := appcommon.ProcConfig{
		AppName:   "broken",
		ProcKey:   appcommon.RandProcKey(),
		BinaryLoc: "/nonexistent/binary/path",
	}

	_, err := m.Start(conf)
	require.Error(t, err) // exec.Start fails; proc is rolled back

	_, ok := m.ProcByName("broken") //nolint
	require.False(t, ok)
}

func TestRPCGateway_Setters(t *testing.T) {
	p := &Proc{log: logging.MustGetLogger("proc-test")}
	gw := NewRPCGateway(logging.MustGetLogger("gw-test"), p)

	require.NoError(t, gw.SetConnectionDuration(123, nil))
	require.Equal(t, int64(123), p.ConnectionDuration())

	appErr := "boom"
	require.NoError(t, gw.SetError(&appErr, nil))
	require.Equal(t, "boom", p.Error())

	require.NoError(t, gw.SetAppPort(routing.Port(42), nil))
	require.Equal(t, routing.Port(42), p.GetAppPort())
}
