// Package mobilecore pkg/mobilecore/mobilecore.go c4-vis-core
//
// Package mobilecore runs the Skywire mobile core — the lite visor and the
// four client apps (skychat, skysocks-client, vpn-client, skydex-client) —
// inside the host app's process. It is the Go side of the iOS app:
// cmd/skywire-mobile-core exports it as C functions and is built as a
// c-archive into SkywireCore.xcframework, because iOS cannot exec a child
// process the way Android runs cmd/skywire-mobile. Build it with the mobile
// tags (make ios-core / build-mobile) so the visor gets the lite module set.
//
// The package covers the lifecycle only: start, stop, state, the last error,
// config generation, the TUN hand-off and a log sink. Everything else — apps,
// transports, chat, Fleet — goes through the visor's authenticated local
// HTTP API, exactly as on Android.
//
// There is one core per process. Start can follow Stop any number of times:
// the visor is built on visor.StartInProcess and Close, never visor.Run,
// which hooks the process-global logger on every call and whose restart and
// shutdown routes end the process.
package mobilecore

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/buildinfo"
	"github.com/skycoin/skywire/pkg/visor"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"

	// The four client apps register their in-process run functions with the
	// launcher at import time (launcher.RegisterApp), so a config entry with
	// an empty `binary` runs them inside the core, as on Android.
	_ "github.com/skycoin/skywire/cmd/apps/skychat/commands"
	_ "github.com/skycoin/skywire/cmd/apps/skydex-client/commands"
	_ "github.com/skycoin/skywire/cmd/apps/skysocks-client/commands"
	_ "github.com/skycoin/skywire/cmd/apps/vpn-client/commands"
)

// State is where the core is in its lifecycle. The values are part of the C
// API (skywire_state), so they never change meaning.
type State int32

// Core states.
const (
	StateStopped  State = 0
	StateStarting State = 1
	StateRunning  State = 2
	StateStopping State = 3
	StateFailed   State = 4
)

func (s State) String() string {
	switch s {
	case StateStopped:
		return "stopped"
	case StateStarting:
		return "starting"
	case StateRunning:
		return "running"
	case StateStopping:
		return "stopping"
	case StateFailed:
		return "failed"
	default:
		return fmt.Sprintf("state(%d)", int32(s))
	}
}

// Options says what to start.
type Options struct {
	// ConfigPath is the visor config file. Required.
	ConfigPath string
	// DataDir, when set, becomes the process's working directory before the
	// visor starts, the way Android runs its core with the data directory as
	// cwd, so any path the config leaves relative lands there. The phone
	// profile makes the paths that matter absolute; this covers the rest.
	DataDir string
	// LogSink, when set, replaces the log sink (see SetLogSink).
	LogSink func(level int32, line string)
	// RestartHook is called when the visor's API asks for a restart (POST
	// /api/visors/{pk}/restart). When nil the core restarts itself: Stop,
	// then Start with the same Options.
	RestartHook func()
}

// StopTimeout bounds a stop the API asks for (POST …/shutdown) and the stop
// half of a self-restart.
const StopTimeout = 30 * time.Second

// ErrStopped is returned by a Start that Stop ended before the visor was up.
var ErrStopped = errors.New("mobilecore: stopped while starting")

// core is the process's one core. mu guards every field and is never held
// while a visor starts or closes; stopMu serializes Stop calls.
var core struct {
	mu      sync.Mutex
	state   State
	lastErr string
	visor   *visor.Visor
	opts    Options
	// cancel ends the visor's context: it aborts a start in progress, and
	// after Close it releases what the modules still hold on it.
	cancel context.CancelFunc
	// started is closed when the current Start has finished, either way.
	started chan struct{}
}

var stopMu sync.Mutex

// Start starts the core and returns once the visor's local API is up, or
// with the reason it could not start (also kept for LastError). ctx bounds
// the start only: canceling it, or calling Stop, aborts a start in progress;
// a running core lives until Stop.
func Start(ctx context.Context, opts Options) error {
	if opts.ConfigPath == "" {
		return errors.New("mobilecore: no config path")
	}
	core.mu.Lock()
	switch core.state {
	case StateStarting, StateRunning, StateStopping:
		st := core.state
		core.mu.Unlock()
		return fmt.Errorf("mobilecore: the core is %s", st)
	}
	runCtx, cancel := context.WithCancel(context.Background())
	started := make(chan struct{})
	core.state = StateStarting
	core.lastErr = ""
	core.opts = opts
	core.cancel = cancel
	core.started = started
	core.mu.Unlock()

	if opts.LogSink != nil {
		SetLogSink(opts.LogSink)
	}
	stopWatch := context.AfterFunc(ctx, cancel)
	v, err := startVisor(runCtx, opts)
	stopWatch()

	core.mu.Lock()
	defer core.mu.Unlock()
	defer close(started)
	if core.state == StateStopping {
		// Stop came in while the visor was starting. It waits on started and
		// closes whatever came up.
		core.visor = v
		return ErrStopped
	}
	if err != nil {
		cancel()
		core.state = StateFailed
		core.lastErr = err.Error()
		return err
	}
	core.visor = v
	core.state = StateRunning
	return nil
}

// Stop stops the core: it aborts a start in progress or closes the running
// visor, and returns once its listeners are closed. A core that is already
// stopped (or failed) is left alone. If closing takes longer than timeout,
// Stop returns an error and the core stays "stopping" until the close
// finishes in the background; Start refuses until then.
func Stop(timeout time.Duration) error {
	stopMu.Lock()
	defer stopMu.Unlock()

	core.mu.Lock()
	switch core.state {
	case StateStopped, StateFailed:
		core.mu.Unlock()
		return nil
	case StateStopping:
		// Only reachable after a Stop that timed out: its close is still
		// running in the background and will mark the core stopped.
		core.mu.Unlock()
		return errors.New("mobilecore: a previous stop is still closing the visor")
	}
	wasStarting := core.state == StateStarting
	core.state = StateStopping
	cancel, started := core.cancel, core.started
	core.mu.Unlock()

	if wasStarting {
		cancel()
	}
	deadline := time.NewTimer(timeout)
	defer deadline.Stop()
	select {
	case <-started:
	case <-deadline.C:
		go func() {
			<-started
			finishStop(cancel)
		}()
		return fmt.Errorf("mobilecore: the start did not unwind within %s", timeout)
	}

	done := make(chan struct{})
	go func() {
		finishStop(cancel)
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-deadline.C:
		return fmt.Errorf("mobilecore: the visor did not close within %s", timeout)
	}
}

// finishStop closes the visor a start produced, if any, ends its context and
// marks the core stopped. Close before cancel, in the order run() stops a
// visor: the modules shut down on a live context.
func finishStop(cancel context.CancelFunc) {
	core.mu.Lock()
	v := core.visor
	core.visor = nil
	core.mu.Unlock()
	var closeErr error
	if v != nil {
		closeErr = v.Close()
	}
	cancel()
	core.mu.Lock()
	core.state = StateStopped
	if closeErr != nil {
		core.lastErr = closeErr.Error()
	}
	core.mu.Unlock()
}

// CurrentState returns the core's state.
func CurrentState() State {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.state
}

// LastError returns why the last start failed (or the last close
// complained), or "" when it did not.
func LastError() string {
	core.mu.Lock()
	defer core.mu.Unlock()
	return core.lastErr
}

// startVisor reads the config and starts the visor on ctx.
func startVisor(ctx context.Context, opts Options) (*visor.Visor, error) {
	if opts.DataDir != "" {
		if err := os.MkdirAll(opts.DataDir, 0o700); err != nil {
			return nil, fmt.Errorf("data dir: %w", err)
		}
		if err := os.Chdir(opts.DataDir); err != nil {
			return nil, fmt.Errorf("data dir: %w", err)
		}
	}
	conf, err := loadConfig(opts.ConfigPath)
	if err != nil {
		return nil, err
	}
	hookConfigLogger(conf)
	host := &visor.InProcessHost{
		Restart: func() { restart(opts) },
		Stop: func() {
			if err := Stop(StopTimeout); err != nil {
				visor.ProcessLogger().PackageLogger("mobilecore").WithError(err).Error("Stop asked by the API failed.")
			}
		},
	}
	return visor.StartInProcess(ctx, conf, visor.Options{}, host)
}

// restart answers the API's restart route: the host's RestartHook when it
// has one, otherwise stop and start again with the same options.
func restart(opts Options) {
	if opts.RestartHook != nil {
		opts.RestartHook()
		return
	}
	log := visor.ProcessLogger().PackageLogger("mobilecore")
	if err := Stop(StopTimeout); err != nil {
		log.WithError(err).Error("Restart: stop failed; not starting again.")
		return
	}
	if err := Start(context.Background(), opts); err != nil {
		log.WithError(err).Error("Restart: start failed.")
	}
}

// loadConfig reads and parses the visor config the way the visor command
// does (cmd/skywire-visor/commands: loadConfig), minus the flag overrides a
// phone never passes.
func loadConfig(path string) (*visorconfig.V1, error) {
	path = filepath.Clean(path)
	raw, err := os.ReadFile(path) //nolint:gosec // the host names its own config file
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}
	log := visor.ProcessLogger().PackageLogger("visor:config")
	conf, compat, err := visorconfig.Parse(log, bytes.NewReader(raw), path, buildinfo.Get())
	if err != nil {
		return nil, fmt.Errorf("failed to read in config: %w", err)
	}
	if !compat {
		return nil, errors.New("config version is incompatible")
	}
	visorconfig.VisorConfigFile = path
	return conf, nil
}
