// Package visor pkg/visor/api_suspend.go
package visor

import (
	"context"
	"fmt"
	"sync"
	"time"
)

// suspendKeepSrc lists the closeStack entries Suspend must NOT tear down:
// the LOCAL CLI RPC surface (cli.listener + cli.grpc + its cmux/accept
// goroutines), which stays up so the operator/systray can call Resume.
// Everything else — transports, routing, apps, dmsg, autoconnect, the
// address-resolver/TPD registration, the reward heartbeat, and the
// dmsg/transport-bound RPC surfaces — is torn down.
var suspendKeepSrc = map[string]bool{
	"cli.listener": true,
	"cli.grpc":     true,
}

// IsSuspended reports whether the visor is currently suspended.
func (v *Visor) IsSuspended() (bool, error) {
	v.suspendMu.Lock()
	defer v.suspendMu.Unlock()
	return v.suspended, nil
}

// Suspend makes the visor quiescent WITHOUT stopping the process: it tears
// down every network subsystem (transports, routing, apps, dmsg,
// autoconnect, AR/TPD registration, the reward heartbeat, and the
// dmsg/transport-bound RPC surfaces) but keeps the local CLI RPC listener
// alive so Resume can arrive. The node stops earning, routing, and serving
// apps and drops off dmsg discovery, while remaining controllable on
// CLIAddr. It is the privilege-free alternative to stopping the service
// manager unit. Idempotent: a second Suspend is a no-op.
func (v *Visor) Suspend() error {
	v.suspendMu.Lock()
	defer v.suspendMu.Unlock()
	if v.suspended {
		return nil
	}
	log := v.MasterLogger().PackageLogger("visor:suspend")
	log.Info("Suspending: tearing down all network subsystems, keeping local RPC.")

	// 1. Cancel the network ctx first so ctx-honoring module goroutines
	//    (accept/serve/update loops) begin unwinding before we close the
	//    listeners and clients they ride on.
	if v.networkCancel != nil {
		v.networkCancel()
	}

	// 2. Walk the close stack in reverse (same LIFO order as Close) and
	//    run every closer except the kept local-RPC entries.
	v.initLock.RLock()
	stack := append([]closer(nil), v.closeStack...)
	v.initLock.RUnlock()
	for i := len(stack) - 1; i >= 0; i-- {
		cl := stack[i]
		if suspendKeepSrc[cl.src] {
			continue
		}
		v.runCloser("suspend", cl)
	}

	// 3. Under the init lock: retain only the kept closers, drop
	//    references to the torn-down subsystems, and reset the one-shot
	//    readiness state so Resume can re-run the module graph cleanly.
	v.initLock.Lock()
	kept := make([]closer, 0, len(v.closeStack))
	for _, cl := range v.closeStack {
		if suspendKeepSrc[cl.src] {
			kept = append(kept, cl)
		}
	}
	v.closeStack = kept

	v.tpM = nil
	v.router = nil
	v.rfClient = nil
	v.procM = nil
	v.appL = nil
	v.dmsgC = nil
	v.dmsgDC = nil
	v.dmsgHTTP = nil
	v.arClient = nil
	v.transportRPCMux = nil

	// dmsgHTTPReady is closed under a select/default guard (init_dmsg.go),
	// so a fresh channel is enough. stun/dmsgTracker are closed via a
	// sync.Once, so the Once must be reset alongside the channel (same
	// pattern as api_network.go's reconnect path) or Resume's re-init
	// won't re-close them and readers would block forever.
	v.dmsgHTTPReady = make(chan struct{})
	v.startupComplete = make(chan struct{})
	v.runtimeErrors = make(chan error)
	v.stun.ready = make(chan struct{})
	v.stun.readyOnce = sync.Once{}
	v.dmsgTracker.ready = make(chan struct{})
	v.dmsgTracker.readyOnce = sync.Once{}
	v.initLock.Unlock()

	v.suspended = true
	log.Info("Visor suspended (local RPC still serving on CLIAddr).")
	return nil
}

// Resume re-runs the module graph to bring a suspended visor fully back
// online. The local CLI RPC surface was never torn down (cliLocalUp), so
// initCLI only re-creates its dmsg/transport-bound surfaces. Idempotent:
// Resume on a running visor is a no-op.
func (v *Visor) Resume() error {
	v.suspendMu.Lock()
	defer v.suspendMu.Unlock()
	if !v.suspended {
		return nil
	}
	log := v.MasterLogger().PackageLogger("visor:resume")
	log.Info("Resuming: re-running the module graph.")

	// Rebuild the value-decorated, cancelable ctx exactly as NewVisor
	// does. v.ctx is the still-live process signal ctx (its cancellation
	// still tears the visor down on real shutdown).
	ctx := context.WithValue(v.ctx, visorKey, v)
	ctx = context.WithValue(ctx, runtimeErrsKey, v.runtimeErrors)
	if dmsgServer != "" {
		ctx = context.WithValue(ctx, "dmsgServer", dmsgServer) //nolint:staticcheck // SA1029: matches dmsg.Client's string key
		if dmsgServerAddr != "" {
			ctx = context.WithValue(ctx, "dmsgServerAddr", dmsgServerAddr) //nolint:staticcheck // SA1029: matches dmsg.Client's string key
		}
	}
	v.networkCtx, v.networkCancel = context.WithCancel(ctx)
	ctx = v.networkCtx

	registerModules(v.MasterLogger())
	mainModule := vis
	if v.conf.Hypervisor != nil {
		mainModule = hv
	}
	go tm.InitConcurrent(ctx)
	mainModule.InitConcurrent(ctx)
	if err := mainModule.Wait(ctx); err != nil {
		select {
		case <-ctx.Done():
		default:
			log.WithError(err).Error("Resume module init failed.")
		}
		return fmt.Errorf("resume: module init failed: %w", err)
	}
	if err := tm.Wait(ctx); err != nil {
		return fmt.Errorf("resume: transport module init failed: %w", err)
	}
	if !v.processRuntimeErrs() {
		return fmt.Errorf("resume: runtime errors during re-init")
	}
	close(v.startupComplete)
	v.suspended = false
	log.Info("Visor resumed.")
	return nil
}

// runCloser runs a single closeStack entry with the same per-module
// timeout Close uses. phase labels the log scope (e.g. "suspend").
func (v *Visor) runCloser(phase string, cl closer) {
	start := time.Now()
	errCh := make(chan error, 1)
	t := time.NewTimer(moduleShutdownTimeout)
	log := v.MasterLogger().PackageLogger(fmt.Sprintf("visor:%s:%s", phase, cl.src))
	go func(cl closer) {
		errCh <- cl.fn()
		close(errCh)
	}(cl)
	select {
	case err := <-errCh:
		t.Stop()
		if err != nil {
			log.WithError(err).WithField("elapsed", time.Since(start)).Warn("Module stopped with unexpected result.")
			return
		}
		log.WithField("elapsed", time.Since(start)).Debug("Module stopped cleanly.")
	case <-t.C:
		log.WithField("elapsed", time.Since(start)).Error("Module timed out.")
	}
}
