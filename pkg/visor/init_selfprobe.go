// Package visor pkg/visor/init_selfprobe.go c3-vis-core
package visor

import (
	"context"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// selfProbeInterval is how often the visor probes its own dmsg listeners.
const selfProbeInterval = 60 * time.Second

// selfProbeTimeout bounds each individual probe attempt.
const selfProbeTimeout = 10 * time.Second

// selfProbeRecoveryThreshold is the number of consecutive failed probes
// to the same port that will trigger a DMSG-session force-reconnect.
// Kept >1 so a single transient network hiccup doesn't churn sessions.
const selfProbeRecoveryThreshold = 2

// selfProbeRecoveryCooldown is the minimum wait between two recovery
// reconnects. Prevents thrashing when the root cause is on the server
// side and reconnecting won't help — we only try once per cooldown.
const selfProbeRecoveryCooldown = 5 * time.Minute

// initSelfProbe starts a background loop that verifies the visor's own dmsg
// listeners are reachable end-to-end, but only when nothing else already proves
// it. Each probe dials the visor's own PK through the dmsg server — exercising
// the full path that remote clients use:
//
//	visor → session → server → forward back to visor → listener → accept
//
// An accepted inbound stream from any real peer traverses that same path, so the
// loop consults dmsgC.InboundSince first and skips the probe entirely whenever a
// remote has reached us since the previous tick. A visor with live traffic — any
// hypervisor with transports, which is the common case — therefore never dials
// itself at all. The probe remains for the situation it was written for: a visor
// nobody is contacting, where silence and unreachability look identical from the
// inside and only an active dial can tell them apart.
//
// If a probe fails persistently (selfProbeRecoveryThreshold consecutive
// misses), the loop calls dmsgC.ForceReconnect() to tear down and re-dial
// all sessions. This is the recovery action that closes the loop —
// previously the probe only logged, leaving broken visors advertising
// themselves as reachable until manual intervention. The visor's
// delegated_servers list in discovery is refreshed automatically as the
// reconnect loop re-establishes sessions.
func initSelfProbe(ctx context.Context, v *Visor, log *logging.Logger) error {
	dmsgC := v.dmsgC
	if dmsgC == nil {
		return nil
	}

	// Wait for dmsg to be ready before starting probes.
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-dmsgC.Ready():
	}

	go selfProbeLoop(ctx, v, dmsgC, log)
	return nil
}

// probeState tracks consecutive-failure counters and the last recovery
// time so we can apply cooldowns.
type probeState struct {
	consecutiveFails map[uint16]int
	lastReconnect    time.Time
}

func selfProbeLoop(ctx context.Context, v *Visor, dmsgC *dmsg.Client, log *logging.Logger) {
	// Give listeners time to start before the first probe.
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return
	}

	state := &probeState{consecutiveFails: make(map[uint16]int)}

	ticker := time.NewTicker(selfProbeInterval)
	defer ticker.Stop()

	lastTick := time.Now()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			// Skip the probe when a real remote has reached us since the last tick.
			// An accepted inbound stream traverses the same path the probe tests —
			// server forwards to our session, listener accepts — so it is strictly
			// better evidence than dialing ourselves, and it costs nothing. A busy
			// visor therefore never probes; the probe survives for the case it was
			// written for, a visor nobody is reaching, where observation tells us
			// nothing and we genuinely cannot distinguish quiet from broken.
			if dmsgC.InboundSince(lastTick) {
				lastTick = now
				for port := range state.consecutiveFails {
					state.consecutiveFails[port] = 0
				}
				continue
			}
			lastTick = now
			probeResults := runSelfProbes(ctx, v, dmsgC, log)
			handleProbeResults(dmsgC, state, probeResults, now, log)
		}
	}
}

// probeReconnector is the subset of dmsg.Client that handleProbeResults
// uses. Abstracted so the test file can supply a fake without building
// a real dmsg client.
type probeReconnector interface {
	ForceReconnect() int
}

// handleProbeResults updates consecutive-failure counters and triggers
// a force-reconnect when the threshold is crossed (subject to cooldown).
// The now parameter is injected so tests can control cooldown timing
// deterministically.
func handleProbeResults(reconn probeReconnector, state *probeState, results map[uint16]bool, now time.Time, log *logging.Logger) {
	anyFailed := false
	shouldRecover := false
	for port, ok := range results {
		if ok {
			// Recovery — log at INFO if we had been failing.
			if state.consecutiveFails[port] >= selfProbeRecoveryThreshold {
				log.WithField("port", port).
					WithField("prior_failures", state.consecutiveFails[port]).
					Info("Self-probe recovered: dmsg listener reachable again")
			}
			state.consecutiveFails[port] = 0
			continue
		}
		anyFailed = true
		state.consecutiveFails[port]++
		logEntry := log.WithField("port", port).
			WithField("consecutive_failures", state.consecutiveFails[port])
		if state.consecutiveFails[port] >= selfProbeRecoveryThreshold {
			// Persistent failure — trigger recovery if cooldown elapsed.
			if now.Sub(state.lastReconnect) >= selfProbeRecoveryCooldown {
				shouldRecover = true
			} else {
				logEntry = logEntry.WithField("cooldown_remaining",
					(selfProbeRecoveryCooldown - now.Sub(state.lastReconnect)).Round(time.Second))
			}
			logEntry.Warn("Self-probe FAILED: dmsg listener unreachable (persistent)")
		} else {
			logEntry.Warn("Self-probe FAILED: dmsg listener unreachable (transient?)")
		}
	}

	if shouldRecover {
		count := reconn.ForceReconnect()
		state.lastReconnect = now
		log.WithField("sessions_closed", count).
			Warn("Self-probe recovery: forced DMSG session reconnect; reconnect loop will re-dial within 15s")
	}

	if !anyFailed && len(results) > 0 {
		log.WithField("ports", len(results)).Debug("Self-probe: all dmsg listeners healthy")
	}
}

// runSelfProbes probes each critical dmsg port and returns a map of port → healthy.
func runSelfProbes(ctx context.Context, _ *Visor, dmsgC *dmsg.Client, _ *logging.Logger) map[uint16]bool {
	results := make(map[uint16]bool)
	myPK := dmsgC.LocalPK()

	// Probe port 136 (route-setup await port) — raw DialStream + Close.
	// The visor's router.serveSetup accepts the stream, checks
	// SetupIsTrusted (our own PK won't be in the setup-nodes list),
	// closes for untrusted — but the dial success confirms reachability.
	results[skyenv.DmsgAwaitSetupPort] = probeRawDial(ctx, dmsgC, myPK, skyenv.DmsgAwaitSetupPort)

	return results
}

// probeRawDial does a bare DialStream + Close to confirm the listener is alive.
func probeRawDial(ctx context.Context, dmsgC *dmsg.Client, pk cipher.PubKey, port uint16) bool {
	probeCtx, cancel := context.WithTimeout(ctx, selfProbeTimeout)
	defer cancel()
	return dmsgC.Probe(probeCtx, pk, port)
}
