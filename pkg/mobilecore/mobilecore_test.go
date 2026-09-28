//go:build mobile

// Package mobilecore pkg/mobilecore/mobilecore_test.go c4-vis-core
//
// The core must start, stop and start again inside one process: iOS cannot
// restart it by killing a child process the way Android does, so every
// goroutine, listener, bbolt lock and log hook a start takes has to be given
// back by Stop. These tests run a real visor on a phone-profile config (the
// profile the Android app writes, ConfigManager.applyPhoneProfile) with the
// four client apps registered and skychat autostarted, on the lite module set
// the phone ships — hence the build tag (make mobile-test):
//
//	go test -tags mobile,withoutsystray,nomsgpack ./pkg/mobilecore/ -count=3
//
// Desktop-only modules (uptime tracker, tpviz, the transportability check,
// system survey) are not restartable in place and are not in that set.
//
// The visor dials the production dmsg deployment while it runs; the tests
// only need it to start and stop, not to connect.
package mobilecore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"strings"
	"sync"
	"testing"
	"time"

	bolt "github.com/0magnet/bbolt"
	"github.com/sirupsen/logrus"
	"github.com/stretchr/testify/require"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/visor"
)

// testStopTimeout bounds every Stop in these tests.
const testStopTimeout = 30 * time.Second

// goroutineMargin is how many goroutines above the pre-start count a stopped
// core may leave behind: net/http idle-connection readers and timers that
// wind down on their own schedule. A leak grows with every cycle; this does
// not.
const goroutineMargin = 25

// phoneCore is one generated phone-profile core in a temp dir.
type phoneCore struct {
	dataDir   string
	config    string
	apiAddr   string
	chatAddr  string
	boltFiles []string
}

// newPhoneCore generates a config with PhoneGenOptions and applies the phone
// profile to it, on free loopback ports so the test never meets a desktop
// visor or a Simulator core on 8000/8001.
func newPhoneCore(t *testing.T) *phoneCore {
	t.Helper()
	dir := t.TempDir()
	pc := &phoneCore{
		dataDir:  dir,
		config:   filepath.Join(dir, "skywire-config.json"),
		apiAddr:  freeLoopbackAddr(t),
		chatAddr: freeLoopbackAddr(t),
	}
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "bin"), 0o700))
	opts := PhoneGenOptions(pc.config, filepath.Join(dir, "bin"))
	opts.HypervisorAddr = pc.apiAddr
	require.NoError(t, GenConfig(opts))
	applyPhoneProfile(t, pc)
	return pc
}

// applyPhoneProfile is the test's copy of the edits ConfigManager.kt makes
// after `config gen` (applyPhoneProfile + pinAppArgs), with the app's
// defaults: dmsg first, Fleet off, public autoconnect off, log level info,
// no remote-management grant.
func applyPhoneProfile(t *testing.T, pc *phoneCore) {
	t.Helper()
	raw, err := os.ReadFile(pc.config)
	require.NoError(t, err)
	var c map[string]any
	require.NoError(t, json.Unmarshal(raw, &c))
	local := filepath.Join(pc.dataDir, "local")

	delete(c, "pty")
	delete(c, "skywire-tcp")
	c["cli_addr"] = ""
	c["hypervisors"] = []any{}
	c["log_level"] = "info"
	c["local_path"] = local
	c["dmsgscp"] = map[string]any{"disabled": true}

	tr := c["transport"].(map[string]any)
	tr["public_autoconnect"] = false
	tr["log_store"] = map[string]any{"location": filepath.Join(local, "transport_logs")}

	hv := c["hypervisor"].(map[string]any)
	delete(hv, "lan_dmsg_server")
	hv["db_path"] = filepath.Join(pc.dataDir, "users.db")
	hv["dmsg_ingest"] = false
	hv["tp_viz"] = map[string]any{"enable": false}
	hv["desk_addr"] = freeLoopbackAddr(t) // desktop builds may serve the desk; never on 0.0.0.0 here

	rt := c["routing"].(map[string]any)
	rt["transport_preference"] = []any{"dmsg", "stcpr", "squicr", "sudph", "stcp", "webrtc", "swsr", "swtr"}
	rt["mux_routes"] = 2

	l := c["launcher"].(map[string]any)
	l["bin_path"] = filepath.Join(pc.dataDir, "bin")
	for _, a := range l["apps"].([]any) {
		app := a.(map[string]any)
		args, _ := app["args"].(string) //nolint:errcheck // absent args are ""
		switch app["name"] {
		case "skysocks-client":
			app["args"] = pinArg(args, "--addr", "127.0.0.1:1080") + " --reconnect"
		case "skydex-client":
			args = pinArg(args, "--addr", "127.0.0.1:8051")
			app["args"] = pinArg(args, "--password-file", filepath.Join(local, "skydex-password"))
		case "skychat":
			args = strings.ReplaceAll(args, "--portless", "")
			args = pinArg(args, "--addr", pc.chatAddr)
			args = pinArg(args, "--persist-db", filepath.Join(local, "skychat-history.db"))
			args = pinArg(args, "--password-file", filepath.Join(local, "skychat-password"))
			app["args"] = strings.Join(strings.Fields(args+" --persist"), " ")
			app["auto_start"] = true
		}
	}
	out, err := json.MarshalIndent(c, "", "  ")
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(pc.config, out, 0o600))
}

// pinArg sets flag's value in a space-joined argv, adding it when absent.
func pinArg(args, flag, value string) string {
	f := strings.Fields(args)
	for i := 0; i < len(f); i++ {
		if f[i] == flag && i+1 < len(f) {
			f[i+1] = value
			return strings.Join(f, " ")
		}
		if strings.HasPrefix(f[i], flag+"=") {
			f[i] = flag + "=" + value
			return strings.Join(f, " ")
		}
	}
	return strings.Join(append(f, flag, value), " ")
}

func freeLoopbackAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	addr := l.Addr().String()
	require.NoError(t, l.Close())
	return addr
}

func (pc *phoneCore) options() Options {
	return Options{ConfigPath: pc.config, DataDir: pc.dataDir}
}

// requirePing asserts the local API answers /api/ping.
func (pc *phoneCore) requirePing(t *testing.T) {
	t.Helper()
	c := &http.Client{Timeout: 5 * time.Second}
	resp, err := c.Get("http://" + pc.apiAddr + "/api/ping")
	require.NoError(t, err)
	defer resp.Body.Close() //nolint:errcheck
	require.Equal(t, http.StatusOK, resp.StatusCode)
}

// requireRefused asserts nothing listens on addr any more.
func requireRefused(t *testing.T, addr string) {
	t.Helper()
	conn, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err == nil {
		_ = conn.Close() //nolint:errcheck
		t.Fatalf("%s still accepts connections after Stop", addr)
	}
}

// requireBoltUnlocked opens every bbolt file under the data dir with a short
// timeout: a database the stopped core did not close still holds its flock,
// and a second open blocks until the timeout.
func (pc *phoneCore) requireBoltUnlocked(t *testing.T) {
	t.Helper()
	var dbs []string
	require.NoError(t, filepath.WalkDir(pc.dataDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if isBolt(p) {
			dbs = append(dbs, p)
		}
		return nil
	}))
	require.NotEmpty(t, dbs, "the core wrote no bbolt file (users.db at least)")
	for _, p := range dbs {
		db, err := bolt.Open(p, 0o600, &bolt.Options{Timeout: 2 * time.Second})
		require.NoError(t, err, "bbolt file %s is still locked after Stop", p)
		require.NoError(t, db.Close())
	}
	pc.boltFiles = dbs
}

// isBolt recognizes a bbolt file by its magic (0xED0CDAED at offset 16 of
// the first meta page), whatever its name.
func isBolt(p string) bool {
	f, err := os.Open(p) //nolint:gosec // a file the test's own core wrote
	if err != nil {
		return false
	}
	defer f.Close() //nolint:errcheck
	var hdr [20]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return false
	}
	return bytes.Equal(hdr[16:20], []byte{0xED, 0xDA, 0x0C, 0xED})
}

// processHookCount is the number of hooks on the process-global loggers at
// the info level: the log store, the broadcaster and the sink are each added
// once per process, never once per start.
func processHookCount() int {
	return len(visor.ProcessLogger().Hooks[logrus.InfoLevel])
}

// settledGoroutines waits for the goroutine count to stop falling and
// returns it.
func settledGoroutines() int {
	n := runtime.NumGoroutine()
	for i := 0; i < 20; i++ {
		time.Sleep(250 * time.Millisecond)
		m := runtime.NumGoroutine()
		if m >= n {
			return m
		}
		n = m
	}
	return n
}

func dumpGoroutines(t *testing.T) {
	t.Helper()
	var b strings.Builder
	_ = pprof.Lookup("goroutine").WriteTo(&b, 1) //nolint:errcheck
	t.Log(b.String())
}

// TestStartStopStart runs three start/stop cycles in one process and checks
// that each stop gives everything back.
func TestStartStopStart(t *testing.T) {
	pc := newPhoneCore(t)
	var lines sync.Map
	SetLogSink(func(level int32, line string) { lines.Store(line, level) })
	t.Cleanup(func() { SetLogSink(nil) })

	baseline := settledGoroutines()
	var hooks, afterFirst int
	for cycle := 1; cycle <= 3; cycle++ {
		start := time.Now()
		require.NoError(t, Start(context.Background(), pc.options()), "cycle %d: %s", cycle, LastError())
		require.Equal(t, StateRunning, CurrentState())
		t.Logf("cycle %d: started in %s", cycle, time.Since(start).Round(time.Millisecond))
		pc.requirePing(t)

		if cycle == 1 {
			hooks = processHookCount()
		}
		require.Equal(t, hooks, processHookCount(), "cycle %d: the process loggers gained hooks", cycle)

		require.NoError(t, Stop(testStopTimeout), "cycle %d", cycle)
		require.Equal(t, StateStopped, CurrentState())
		requireRefused(t, pc.apiAddr)
		requireRefused(t, pc.chatAddr)
		pc.requireBoltUnlocked(t)
		// The next start must not find this visor's networkers: its modules
		// that start before its own launcher would bind to a closed router.
		_, err := appnet.ResolveNetworker(appnet.TypeSkynet)
		require.ErrorIs(t, err, appnet.ErrNoSuchNetworker, "cycle %d: the stopped visor's skynet networker is still registered", cycle)

		n := settledGoroutines()
		t.Logf("cycle %d: goroutines %d (before any start %d)", cycle, n, baseline)
		if cycle == 1 {
			afterFirst = n
		}
		if n > baseline+goroutineMargin || n > afterFirst+goroutineMargin/2 {
			dumpGoroutines(t)
			t.Fatalf("cycle %d: %d goroutines after Stop, %d before any start, %d after the first stop", cycle, n, baseline, afterFirst)
		}
	}
	t.Logf("bbolt files reopened after each stop: %v", pc.boltFiles)

	gotLine := false
	lines.Range(func(k, _ any) bool {
		gotLine = strings.Contains(k.(string), "Startup complete")
		return !gotLine
	})
	require.True(t, gotLine, "the log sink never saw the visor's startup line")
}

// TestStopWhileStarting stops the core while its modules are still
// initializing: Stop must return within its timeout and leave no listener.
func TestStopWhileStarting(t *testing.T) {
	pc := newPhoneCore(t)
	baseline := settledGoroutines()

	startErr := make(chan error, 1)
	go func() { startErr <- Start(context.Background(), pc.options()) }()
	require.Eventually(t, func() bool { return CurrentState() == StateStarting }, 5*time.Second, time.Millisecond)
	time.Sleep(200 * time.Millisecond)

	stopAt := time.Now()
	require.NoError(t, Stop(10*time.Second))
	t.Logf("stopped %s into the start, in %s", 200*time.Millisecond, time.Since(stopAt).Round(time.Millisecond))

	select {
	case err := <-startErr:
		if err != nil && !errors.Is(err, ErrStopped) {
			t.Fatalf("Start returned %v, want nil or ErrStopped", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Start did not return after Stop")
	}
	require.Equal(t, StateStopped, CurrentState())
	requireRefused(t, pc.apiAddr)
	requireRefused(t, pc.chatAddr)

	if n := settledGoroutines(); n > baseline+goroutineMargin {
		dumpGoroutines(t)
		t.Fatalf("%d goroutines after Stop, %d before the start", n, baseline)
	}

	// The core starts cleanly after an aborted start.
	require.NoError(t, Start(context.Background(), pc.options()), LastError())
	pc.requirePing(t)
	require.NoError(t, Stop(testStopTimeout))
	requireRefused(t, pc.apiAddr)
}

// TestRestartRoute asks the running core to restart through the local API's
// Reload (what POST …/restart calls) and checks the core comes back in the
// same process instead of the process ending.
func TestRestartRoute(t *testing.T) {
	pc := newPhoneCore(t)
	require.NoError(t, Start(context.Background(), pc.options()), LastError())
	t.Cleanup(func() { _ = Stop(testStopTimeout) }) //nolint:errcheck

	core.mu.Lock()
	v := core.visor
	core.mu.Unlock()
	require.NotNil(t, v)
	require.NoError(t, v.Reload())

	require.Eventually(t, func() bool {
		core.mu.Lock()
		defer core.mu.Unlock()
		return core.state == StateRunning && core.visor != nil && core.visor != v
	}, 60*time.Second, 100*time.Millisecond, "the core did not come back after the restart route")
	pc.requirePing(t)
}

// TestStartErrors covers the refusals that need no visor.
func TestStartErrors(t *testing.T) {
	require.Error(t, Start(context.Background(), Options{}))

	err := Start(context.Background(), Options{ConfigPath: filepath.Join(t.TempDir(), "missing.json")})
	require.Error(t, err)
	require.Equal(t, StateFailed, CurrentState())
	require.Contains(t, LastError(), "failed to read config file")
	require.NoError(t, Stop(time.Second), "stopping a failed core is a no-op")

	// The test binary is never GOOS=ios, where SetTunFD is supported.
	require.Error(t, SetTunFD(3))
}
