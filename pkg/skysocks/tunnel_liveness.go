// Package skysocks pkg/skysocks/tunnel_liveness.go
//
// What the pool is allowed to call dead.
//
// The hard-dead window on its own is an OBSERVATION of silence, not evidence
// of death: it fires whenever nothing arrived, including when nothing could
// arrive because the local dataplane itself was stalled. On 2026-09-18 a ~30 s
// blackout in the router's inbound loop (a transit peer that stopped draining
// — see pkg/router/router_forward.go) silenced every tunnel at once, and the
// pool answered by retiring 3 HEALTHY tunnels for "no pong and no bytes for
// 1m0s" and re-dialing onto unmeasured sudph routes, which cost the set ten
// minutes of degraded goodput for a thirty-second stall.
//
// So a retire now needs a FAILED PROBE as well: a ping this client sent that
// came back an error, or one that has been outstanding longer than
// tunnel.probe_fail_window. An idle standby that nobody pinged successfully
// *because the local visor was not moving packets* has no failed probe — its
// ping is merely outstanding, and a few seconds later the pong arrives and the
// tunnel is alive, which is the truth.
package skysocks

import (
	"time"

	"github.com/0magnet/yamux"

	"github.com/skycoin/skywire/pkg/skysocks/skysettings"
)

// sessionProbeFailWindow is how long a liveness ping may stay unanswered
// before it counts as a FAILED probe. The DEFAULT is the old hard-dead
// behaviour's own horizon (a minute), so a genuinely dead exit is retired on
// the same schedule it always was. NewClient snapshots it into
// Client.probeFailWindow and the loop reads only that field; a var so tests can
// shrink it before constructing a client, like the two beside it.
var sessionProbeFailWindow = 60 * time.Second

// probeFailWindow is the window in force: the live knob when it has been set,
// otherwise what NewClient snapshotted.
func (c *Client) probeFailWindow() time.Duration {
	if skysettings.IsSet(skysettings.TunnelProbeFailWindow) {
		return skysettings.Dur(skysettings.TunnelProbeFailWindow)
	}
	return c.probeFailWin
}

// probeLedger records what each tunnel's liveness probes have actually done.
// Owned by sessionKeepAliveLoop, so it needs no locking.
type probeLedger struct {
	sentAt  map[*yamux.Session]time.Time
	errored map[*yamux.Session]bool
}

func newProbeLedger() *probeLedger {
	return &probeLedger{
		sentAt:  make(map[*yamux.Session]time.Time),
		errored: make(map[*yamux.Session]bool),
	}
}

// sent marks a ping issued for s.
func (l *probeLedger) sent(s *yamux.Session, now time.Time) {
	l.sentAt[s] = now
	l.errored[s] = false
}

// result folds a ping's outcome in. An error is a failed probe on the spot; a
// pong clears the ledger, however late it arrives.
func (l *probeLedger) result(s *yamux.Session, ok bool) {
	if ok {
		delete(l.sentAt, s)
		l.errored[s] = false
		return
	}
	l.errored[s] = true
}

// forget drops a retired session.
func (l *probeLedger) forget(s *yamux.Session) {
	delete(l.sentAt, s)
	delete(l.errored, s)
}

// failed reports whether s has a probe this client can point at as FAILED:
// one that errored, or one still outstanding past window. A tunnel nobody has
// probed yet is not failed — silence alone never was the evidence.
func (l *probeLedger) failed(s *yamux.Session, outstanding bool, now time.Time, window time.Duration) bool {
	if l.errored[s] {
		return true
	}
	if !outstanding {
		return false
	}
	at, ok := l.sentAt[s]
	return ok && !at.IsZero() && now.Sub(at) >= window
}
