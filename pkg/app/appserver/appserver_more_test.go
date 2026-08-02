// Package appserver pkg/app/appserver/appserver_more_test.go: additional unit
// tests broadening coverage of the small helpers (errors, app_state, stderr),
// the Proc getters/setters and NewProc constructor, and the procManager
// lifecycle methods.
package appserver

import (
	"context"
	"errors"
	"io"
	"net"
	"path/filepath"
	"testing"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appcommon"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// mockUpdater is a no-op appdisc.Updater for Proc.Stop paths.
type mockUpdater struct{}

func (mockUpdater) Start() {}
func (mockUpdater) Stop()  {}

// ---- errors.go -------------------------------------------------------------

func TestNetErr(t *testing.T) {
	e := &netErr{err: errors.New("boom"), timeout: true, temporary: false}
	require.Equal(t, "boom", e.Error())
	require.True(t, e.Timeout())
	require.False(t, e.Temporary())
}

// ---- app_state.go ----------------------------------------------------------

func TestAppStatus_String(t *testing.T) {
	require.Equal(t, "stopped", AppStatusStopped.String())
	require.Equal(t, "running", AppStatusRunning.String())
	require.Equal(t, "errored", AppStatusErrored.String())
	require.Equal(t, "starting", AppStatusStarting.String())
	require.Equal(t, "unknown", AppStatus(99).String())
}

// ---- stderr.go -------------------------------------------------------------

func TestContainsAndIgnoreErrs(t *testing.T) {
	iErrs := getIgnoreErrs()
	require.NotEmpty(t, iErrs)
	// "rpc.Serve: accept:accept" is in the suppression list on every platform
	// (the iptables/RTNETLINK entries are unix-only — see stderr_unix.go vs
	// stderr_windows.go), so assert against it to keep this test cross-platform.
	require.True(t, contains(iErrs, "rpc.Serve: accept:accept tcp4 127.0.0.1:59664: use of closed network connection"))
	require.False(t, contains(iErrs, "some genuinely unexpected error"))
}

func TestPrintStdErr(t *testing.T) {
	r, w := io.Pipe()
	log := logrus.NewEntry(logrus.New())
	printStdErr(r, log)

	// One suppressed line, one real line — neither should panic.
	_, _ = io.WriteString(w, "RTNETLINK answers: File exists\nreal failure\n") //nolint
	require.NoError(t, w.Close())
	time.Sleep(50 * time.Millisecond)
	_ = r.Close() //nolint
}

// ---- proc.go: getters / setters --------------------------------------------

func TestProc_GettersSetters(t *testing.T) {
	p := &Proc{}

	p.SetConnectionDuration(42)
	require.Equal(t, int64(42), p.ConnectionDuration())

	p.SetError("oops")
	require.Equal(t, "oops", p.Error())

	p.SetAppPort(routing.Port(99))
	require.Equal(t, routing.Port(99), p.GetAppPort())

	require.False(t, p.IsRunning())
	require.Nil(t, p.ConnectionsSummary()) // nil rpcGW
	require.Nil(t, p.Logs())               // nil logDB
	require.Nil(t, p.Cmd())                // nil cmd

	_, ok := p.StartTime()
	require.False(t, ok) // not running
}

func TestProc_InjectConn(t *testing.T) {
	p := &Proc{connCh: make(chan struct{}, 1), log: logging.MustGetLogger("proc-test")}
	c1, c2 := net.Pipe()
	defer func() { _ = c1.Close(); _ = c2.Close() }() //nolint

	require.True(t, p.InjectConn(c1))  // first call wins
	require.False(t, p.InjectConn(c2)) // subsequent calls are no-ops
	require.NotNil(t, p.rpcGW)
}

func TestProc_SetDetailedStatus_RunningClosesReady(t *testing.T) {
	p := &Proc{readyCh: make(chan struct{}, 1), log: logging.MustGetLogger("proc-test")}
	p.SetDetailedStatus(AppDetailedStatusRunning)
	require.Equal(t, AppDetailedStatusRunning, p.DetailedStatus())

	select {
	case <-p.readyCh:
		// readyCh was closed as expected.
	default:
		t.Fatal("readyCh should be closed once status is Running")
	}

	// A second Running status must not panic re-closing readyCh.
	require.NotPanics(t, func() { p.SetDetailedStatus(AppDetailedStatusRunning) })
}

// ---- proc.go: NewProc ------------------------------------------------------

func internalConf(name string) appcommon.ProcConfig { //nolint
	return appcommon.ProcConfig{
		AppName: name,
		ProcKey: appcommon.RandProcKey(),
		RunFunc: func(_ context.Context, _ []string) error { return nil },
	}
}

func TestNewProc_Internal(t *testing.T) {
	p := NewProc(nil, internalConf("app"), mockUpdater{}, nil, "app", "")
	require.NotNil(t, p)
	require.Nil(t, p.Cmd()) // internal apps have no exec.Cmd
}

func TestNewProc_InternalWithUserWarns(t *testing.T) {
	conf := internalConf("app")
	conf.ProcUser = "nobody" // ignored for in-process apps (logs a warning)
	p := NewProc(nil, conf, mockUpdater{}, nil, "app", "")
	require.NotNil(t, p)
}

func TestNewProc_External(t *testing.T) {
	conf := appcommon.ProcConfig{
		AppName:   "app",
		ProcKey:   appcommon.RandProcKey(),
		BinaryLoc: "/bin/true",
	}
	p := NewProc(nil, conf, mockUpdater{}, nil, "app", "")
	require.NotNil(t, p.Cmd()) // external apps build an exec.Cmd
}

func TestNewProc_ExternalWithLogDB(t *testing.T) {
	conf := appcommon.ProcConfig{
		AppName:   "app",
		ProcKey:   appcommon.RandProcKey(),
		BinaryLoc: "/bin/true",
		LogDBLoc:  filepath.Join(t.TempDir(), "log.db"),
	}
	p := NewProc(nil, conf, mockUpdater{}, nil, "app", t.TempDir())
	require.NotNil(t, p)
	require.NotNil(t, p.Logs()) // bbolt log store wired up
}

// ---- proc_manager.go -------------------------------------------------------

func newManager(t *testing.T) *procManager {
	t.Helper()
	mI, err := NewProcManager(nil, nil, nil, nil, ":0", "")
	require.NoError(t, err)
	m, ok := mI.(*procManager)
	require.True(t, ok)
	return m
}

func TestProcManager_Addr(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint
	require.NotNil(t, m.Addr())
}

func TestProcManager_SetGetError(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	_, ok := m.ErrorByName("app")
	require.False(t, ok)

	require.NoError(t, m.SetError("app", "boom"))
	got, ok := m.ErrorByName("app")
	require.True(t, ok)
	require.Equal(t, "boom", got)
}

func TestProcManager_AppByPort(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	p := &Proc{}
	p.SetAppPort(routing.Port(1234))
	m.mx.Lock()
	m.procs["app"] = p
	m.mx.Unlock()

	name, ok := m.AppByPort(routing.Port(1234))
	require.True(t, ok)
	require.Equal(t, "app", name)

	_, ok = m.AppByPort(routing.Port(4321))
	require.False(t, ok)
}

func TestProcManager_GetAppPort(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	p := &Proc{}
	p.SetAppPort(routing.Port(7))
	m.mx.Lock()
	m.procs["app"] = p
	m.mx.Unlock()

	port, err := m.GetAppPort("app")
	require.NoError(t, err)
	require.Equal(t, routing.Port(7), port)

	_, err = m.GetAppPort("nope")
	require.ErrorIs(t, err, ErrNoSuchApp)
}

func TestProcManager_StatsAndConnectionsSummary(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	m.mx.Lock()
	m.procs["app"] = &Proc{}
	m.mx.Unlock()

	stats, err := m.Stats("app")
	require.NoError(t, err)
	require.Nil(t, stats.Connections) // nil rpcGW → no summaries
	require.Nil(t, stats.StartTime)   // not running

	_, err = m.Stats("nope")
	require.ErrorIs(t, err, ErrNoSuchApp)

	cs, err := m.ConnectionsSummary("app")
	require.NoError(t, err)
	require.Nil(t, cs)

	_, err = m.ConnectionsSummary("nope")
	require.ErrorIs(t, err, ErrNoSuchApp)
}

func TestProcManager_StopNotRunning(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	m.mx.Lock()
	m.procs["app"] = &Proc{} // isRunning == 0
	m.mx.Unlock()

	require.ErrorIs(t, m.Stop("app"), errProcNotStarted)
	require.ErrorIs(t, m.Stop("nope"), ErrNoSuchApp)
}

func TestProcManager_StopRunning(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	p := &Proc{
		disc:    mockUpdater{},
		connCh:  make(chan struct{}, 1),
		log:     logging.MustGetLogger("proc-test"),
		appName: "app",
	}
	p.isRunning = 1
	m.mx.Lock()
	m.procs["app"] = p
	m.mx.Unlock()

	require.NoError(t, m.Stop("app"))

	_, err := m.Stats("app") // popped out of the registry
	require.ErrorIs(t, err, ErrNoSuchApp)
}

func TestProcManager_Wait(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	p := &Proc{}
	p.isRunning = 1 // Wait requires running; waitMx is unlocked so it returns immediately
	m.mx.Lock()
	m.procs["app"] = p
	m.mx.Unlock()

	require.NoError(t, m.Wait("app"))

	require.ErrorIs(t, m.Wait("nope"), ErrNoSuchApp)
}

func TestProcManager_Close(t *testing.T) {
	m := newManager(t)
	require.NoError(t, m.Close())
	require.ErrorIs(t, m.Close(), ErrClosed) // idempotent → ErrClosed
}

func TestProcManager_StartAfterClose(t *testing.T) {
	m := newManager(t)
	require.NoError(t, m.Close())

	_, err := m.Start(internalConf("app"))
	require.ErrorIs(t, err, ErrClosed)

	_, err = m.Register(internalConf("app"))
	require.ErrorIs(t, err, ErrClosed)
}

func TestProcManager_RegisterDuplicateRunning(t *testing.T) {
	m := newManager(t)
	defer func() { _ = m.Close() }() //nolint

	running := &Proc{connCh: make(chan struct{}, 1), disc: mockUpdater{}, log: logging.MustGetLogger("proc-test")}
	running.isRunning = 1
	m.mx.Lock()
	m.procs["app"] = running
	m.mx.Unlock()

	_, err := m.Register(appcommon.ProcConfig{AppName: "app", ProcKey: appcommon.RandProcKey()})
	require.ErrorIs(t, err, ErrAppAlreadyStarted)
}
