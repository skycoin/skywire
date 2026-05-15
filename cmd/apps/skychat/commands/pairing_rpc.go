// Package commands cmd/apps/skychat/commands/pairing_rpc.go
//
// Auto-reconnect wrapper for the visor RPC client used by the pairing
// subsystem (and group-feed introspection).
//
// Background: the chat-app holds a single net/rpc client to the local
// visor (see connectPairRPC). pkg/visor.(*rpcClient).Call closes the
// underlying connection on a single timeout — after which every
// subsequent call returns rpc.ErrShutdown forever. Without the
// machinery here, that one transient timeout permanently breaks all
// pair + group operations until the chat-app is restarted.
//
// This file adds:
//   - a mutex guarding the package-level pairRPC pointer
//   - a redial path that swaps a fresh client in on shutdown
//   - pairRPCCall, a retry-once-on-shutdown helper used by every call
//     site so each operation self-heals
//   - a background watchdog goroutine that periodically pings the RPC
//     so a long-idle channel doesn't surprise the next operator-facing
//     call with the reconnect cost.

package commands

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/visor"
)

// pairRPCMu guards pairRPC. All reads/writes of the package-level
// pairRPC pointer must go through the accessors below.
var pairRPCMu sync.RWMutex

// pairRPCWatchdogCancel stops the background health-check goroutine.
var pairRPCWatchdogCancel context.CancelFunc

// errPairRPCUnavailable is returned when no usable client exists —
// either pairing is disabled or both the initial dial and an in-line
// redial attempt failed.
var errPairRPCUnavailable = errors.New("pair-rpc unavailable")

// pairRPCGet returns the current client (may be nil).
func pairRPCGet() visor.API {
	pairRPCMu.RLock()
	defer pairRPCMu.RUnlock()
	return pairRPC
}

// pairRPCAlive reports whether a non-nil client is currently set.
// Mirrors the legacy `if pairRPC == nil` nil-checks but under the
// mutex.
func pairRPCAlive() bool { return pairRPCGet() != nil }

// connectPairRPCLocked dials a fresh client and swaps it in under the
// write lock. Idempotent: any previous client is closed first so the
// old TCP connection doesn't linger. Safe to call from any goroutine.
//
// Returns the new client (may be nil if dialing fails or pairing is
// disabled). Callers can use the return value to retry immediately
// without re-reading the global.
func connectPairRPCLocked(reason string) visor.API {
	if !pairEnable {
		return nil
	}
	pairRPCMu.Lock()
	defer pairRPCMu.Unlock()

	// Close any previous client so the old conn doesn't linger.
	if old := pairRPC; old != nil {
		if c, ok := old.(io.Closer); ok {
			_ = c.Close() //nolint:errcheck
		}
		pairRPC = nil
	}

	const dialTimeout = 5 * time.Second
	conn, err := net.DialTimeout("tcp", pairRPCAddr, dialTimeout)
	if err != nil {
		appLog("Pairing: visor RPC redial %s failed (%s): %v", pairRPCAddr, reason, err)
		return nil
	}
	log := logging.MustGetLogger(fmt.Sprintf("skychat-pair-rpc://%s", pairRPCAddr))
	pairRPC = visor.NewRPCClient(log, conn, visor.RPCPrefix, 30*time.Second)
	appLog("Pairing: connected to visor RPC at %s (%s)", pairRPCAddr, reason)
	return pairRPC
}

// isRPCShutdown reports whether err indicates the RPC client's
// underlying connection has been closed and will not recover on its
// own. Covers the canonical rpc.ErrShutdown and the string variant
// callers see when net.OpError or context.DeadlineExceeded surfaces
// via pkg/visor.(*rpcClient).Call.
func isRPCShutdown(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, rpc.ErrShutdown) {
		return true
	}
	// pkg/visor.(*rpcClient).Call closes the conn on context-deadline
	// (Call returns ctx.Err() in that case). The follow-up call then
	// surfaces a generic "connection is shut down" or "use of closed
	// network connection" — both are recovery-by-redial situations.
	s := err.Error()
	switch {
	case strings.Contains(s, "connection is shut down"):
		return true
	case strings.Contains(s, "use of closed network connection"):
		return true
	case errors.Is(err, context.DeadlineExceeded):
		// Treat as shutdown: rpcClient.Call has already closed the
		// conn for us, so the next call would shutdown-error anyway.
		return true
	}
	return false
}

// pairRPCCall runs fn against the current pairRPC client with
// reconnect-on-shutdown semantics. If fn returns an error indicating
// the client is dead, the helper redials once and retries. Returns
// errPairRPCUnavailable when there is no usable client.
//
// The op string is only used for log lines, e.g. "GroupList",
// "PairAdd". Keep it short.
func pairRPCCall(op string, fn func(visor.API) error) error {
	return pairRPCDo(op, pairRPCGet, connectPairRPCLocked, fn)
}

// pairRPCDo is the inner retry loop. Production callers should use
// pairRPCCall; this form takes injectable get/redial functions so
// the retry-on-shutdown behavior can be unit-tested without a real
// visor RPC server.
func pairRPCDo(op string,
	get func() visor.API,
	redial func(reason string) visor.API,
	fn func(visor.API) error) error {
	cli := get()
	if cli == nil {
		return errPairRPCUnavailable
	}
	err := fn(cli)
	if err == nil || !isRPCShutdown(err) {
		return err
	}
	appLog("Pairing: %s hit RPC shutdown (%v) — reconnecting", op, err)
	cli = redial("retry-after-shutdown:" + op)
	if cli == nil {
		return errPairRPCUnavailable
	}
	return fn(cli)
}

// startPairRPCWatchdog runs a background loop that periodically
// verifies the RPC client is alive. On detection of a shut-down
// connection it redials so the next operator-facing call doesn't
// have to pay the reconnect cost.
//
// The probe is intentionally lightweight: a Health() call. If the
// visor's API ever loses Health, swap it for another no-arg method.
//
// No-op when pairing is disabled.
func startPairRPCWatchdog(parent context.Context) {
	if !pairEnable {
		return
	}
	ctx, cancel := context.WithCancel(parent) //nolint:gosec
	pairRPCWatchdogCancel = cancel

	go func() {
		// Pick an interval shorter than typical idle-timeout values so
		// a stale conn is detected before the user notices. 60s is a
		// reasonable balance against churn for an always-on visor.
		const probeInterval = 60 * time.Second
		ticker := time.NewTicker(probeInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			cli := pairRPCGet()
			if cli == nil {
				// Try to come up from cold — the original dial may
				// have failed during startup and never been retried.
				connectPairRPCLocked("watchdog:cold-start")
				continue
			}
			if _, err := cli.Health(); err != nil && isRPCShutdown(err) {
				connectPairRPCLocked("watchdog:shutdown-detected")
			}
		}
	}()
}

// stopPairRPCWatchdog cancels the watchdog goroutine. Idempotent.
func stopPairRPCWatchdog() {
	if pairRPCWatchdogCancel != nil {
		pairRPCWatchdogCancel()
		pairRPCWatchdogCancel = nil
	}
}
