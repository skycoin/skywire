// Package visor pkg/visor/inprocess.go c3-vis-core
//
// Running the visor inside another program's process. pkg/mobilecore does it
// for the iOS app, which links the core as a static library because iOS has
// no fork/exec. run() (visor.go) is built for a process of its own: it adds
// hooks to the package-global logger on every call, waits on a signal
// context, and the API can end the process — Shutdown calls os.Exit and
// Reload re-enters run(). StartInProcess is the part of run() a host can
// call again and again in one process, and InProcessHost takes over the two
// API paths that would end it.
package visor

import (
	"context"
	"errors"
	"fmt"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor/logstore"
	"github.com/skycoin/skywire/pkg/visor/visorconfig"
)

// InProcessHost is what the program hosting the visor does when the API asks
// the visor to go away. Each function is called on its own goroutine after
// the API call that asked for it has returned, because the host's stop
// closes the servers that call arrived on.
type InProcessHost struct {
	// Restart replaces Reload (POST /api/visors/{pk}/restart, `cli visor
	// reload`): the host stops the visor and starts it again from the
	// config on disk.
	Restart func()
	// Stop replaces Shutdown (POST /api/visors/{pk}/shutdown, `cli visor
	// halt`), which ends the whole process on desktop.
	Stop func()
}

// processLogs is the log plumbing that lives as long as the process: the
// runtime-log ring the /runtime-logs route reads and the broadcaster the
// gRPC log streams subscribe to. run() makes new ones and hooks them onto
// the package-global logger on every call, which is right for a process that
// runs one visor and a leak for one that restarts its visor in place. An
// in-process host shares one set across restarts, so a core that restarted
// still shows the log that led up to it.
type processLogs struct {
	store logstore.Store
	hook  logrus.Hook
	bcast *logging.Broadcaster
}

var (
	inProcessLogsOnce sync.Once
	inProcessLogsVal  processLogs
)

func inProcessLogs() *processLogs {
	inProcessLogsOnce.Do(func() {
		store, hook := logstore.MakeStoreLevel(runtimeLogMaxEntries, logging.HookLevel(logstore.DefaultHookLevel))
		bcast := logging.NewBroadcaster()
		mLog.AddHook(hook)
		mLog.AddHook(bcast)
		inProcessLogsVal = processLogs{store: store, hook: hook, bcast: bcast}
	})
	return &inProcessLogsVal
}

// ProcessLogger returns the package-global master logger, the one visor
// code logs through when it has no visor at hand. An in-process host adds
// its own hooks to it once per process; hooks added per start would pile up.
func ProcessLogger() *logging.MasterLogger {
	return mLog
}

// StartInProcess starts a visor inside the caller's process and returns it
// once every module is up. It can be called again in the same process after
// the previous visor's Close has returned. conf must be freshly parsed for
// each call (visorconfig.Parse gives every config its own master logger,
// which this hooks). Canceling ctx aborts a start in progress; after a
// successful start, Close stops the visor. host must be non-nil: it is what
// keeps the restart and shutdown routes from ending the host's process.
func StartInProcess(ctx context.Context, conf *visorconfig.V1, opts Options, host *InProcessHost) (*Visor, error) {
	if host == nil || host.Restart == nil || host.Stop == nil {
		return nil, errors.New("visor: StartInProcess needs an InProcessHost with Restart and Stop")
	}
	logs := inProcessLogs()
	conf.MasterLogger().AddHook(logs.hook)
	conf.MasterLogger().AddHook(logs.bcast)

	applyStartOptions(conf, opts)
	if err := attachUIAssets(conf); err != nil {
		return nil, err
	}

	opts.LaunchBrowser = false
	opts.inProcess = host
	v, ok := NewVisor(ctx, conf, opts, logs.bcast, logs.store)
	if !ok {
		if err := ctx.Err(); err != nil {
			return nil, fmt.Errorf("visor start aborted: %w", err)
		}
		return nil, errors.New("visor failed to start (see the log)")
	}
	return v, nil
}
