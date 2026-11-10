// Package router pkg/router/route_group_close.go c2-net-routing
package router

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

func (rg *RouteGroup) sendError(ctx context.Context, rule routing.Rule, tp *transport.ManagedTransport) error {
	errPayload := rg.GetError()
	if errPayload == nil {
		return nil
	}

	if !rg.isCloseInitiator() {
		return nil
	}

	packet, err := routing.MakeErrorPacket(rule.NextRouteID(), []byte(errPayload.Error()))
	if err != nil {
		return err
	}

	return rg.writePacket(ctx, tp, packet, rule.KeyRouteID())
}

// Close closes a RouteGroup with the specified close `code`:
// - Send Close packet for all ForwardRules with the code `code`.
// - Delete all rules (ForwardRules and ConsumeRules) from routing table.
// - Close all go channels.
func (rg *RouteGroup) close(code routing.CloseCode) error {
	if rg.isClosed() {
		return nil
	}

	rg.fireCloseObserver()

	// Snapshot the legs under rg.mu and RELEASE it before the broadcast and the
	// wait below. Both are slow (a dead transport takes the full
	// closeRoutineTimeout, and the wait another one), and rg.mu is the lock the
	// router's single packet-dispatch loop takes for EVERY route group's data
	// packets — holding it across ~4s of close stalls the whole visor's intake.
	// The snapshot is what the broadcast needs; nothing here mutates rg.legs.
	rg.mu.Lock()
	for i := range rg.legs {
		if rg.legs[i].fwd == nil {
			rg.mu.Unlock()
			return ErrRuleTransportMismatch
		}
	}
	legs := append([]groupLeg(nil), rg.legs...)
	rg.mu.Unlock()

	closeInitiator := rg.isCloseInitiator()

	by, fallback := MuxByRemote, fmt.Sprintf("peer closed the group (code %d)", code)
	if closeInitiator {
		by, fallback = MuxByLocal, fmt.Sprintf("local close (code %d)", code)
	}
	rg.noteMuxEvent(MuxEvent{Event: MuxEventGroupClosed, By: by, LegIndex: -1, Legs: len(legs),
		Reason: rg.takeCloseReason(fallback)})

	if closeInitiator {
		// will wait for close response from all the transports
		rg.closeDonePending.Store(int32(len(legs))) //nolint:gosec
		rg.closeDone()
	}

	rg.broadcastClosePackets(code, legs)

	if closeInitiator && anyLiveTransport(legs) && rg.lastRecv.Load() != 0 {
		// if this visor initiated closing, we need to wait for close packets
		// to come back, or to exit with a timeout if anything goes wrong in
		// the network.
		//
		// Only while a leg can still carry the reply. When every transport
		// under the group is closed the answer cannot arrive, and waiting
		// closeRoutineTimeout for it keeps the app's reader and writer parked
		// on a group that is already gone — the whole cost of the wait with
		// none of its benefit.
		// Likewise when the peer never sent anything on the group, as after a
		// failed handshake: no answer is coming, and the dial is waiting to retry.
		if err := rg.waitForCloseRouteGroup(closeRoutineTimeout); err != nil {
			rg.logger.Errorf("Error during close route group: %v", err)
		}
	} else if closeInitiator {
		// Not waiting, so nothing else will mark the close done.
		rg.closeDonePending.Store(0)
		rg.signalCloseDone()
	}

	// Re-read the rule set under rg.mu (rather than reusing the snapshot above):
	// the lock was released for the broadcast and the wait, so a leg may have
	// come or gone in between and every live rule must still be deleted.
	rg.mu.Lock()
	rules := make([]routing.RouteID, 0, 2*len(rg.legs)+len(rg.rvsOnly))
	for i := range rg.legs {
		if r := rg.legs[i].fwd; r != nil {
			rules = append(rules, r.KeyRouteID())
		}
	}
	// Also delete the reverse/consume rules. Without this they linger in the
	// routing table until the keep-alive GC reaps them, leaking N stale rules
	// per close (N legs for a mux group) and opening the window where a packet
	// resolves the orphaned consume rule but finds no route group
	// (errRouteDescNotExist log-spam).
	for i := range rg.legs {
		if r := rg.legs[i].rvs; r != nil {
			rules = append(rules, r.KeyRouteID())
		}
	}
	for _, r := range rg.rvsOnly {
		if r != nil {
			rules = append(rules, r.KeyRouteID())
		}
	}
	rg.mu.Unlock()

	rg.rt.DelRules(rules)

	if closeInitiator {
		rg.closedOnce.Do(func() { close(rg.closed) })
	}
	rg.once.Do(func() {
		// Deliberately do NOT close(rg.readCh). readCh has multiple concurrent
		// senders (handleDataPacket, one per mux leg), so closing it races with an
		// in-flight send — a WARNING: DATA RACE under -race, and historically a
		// "send on closed channel" panic. Closure is signaled ONLY via rg.closed /
		// rg.remoteClosed: the sole reader (Read) and every sender select on those,
		// so readCh is never closed and there is nothing to race.
		rg.setRemoteClosed()
	})

	return nil
}

func (rg *RouteGroup) handleClosePacket(code routing.CloseCode) error {
	rg.logger.Debugf("Got close packet with code %d", code)

	if rg.isCloseInitiator() {
		// this route group initiated close loop and got response
		rg.logger.Debugf("Handling response close packet with code %d", code)

		if rg.closeDonePending.Add(-1) <= 0 {
			rg.signalCloseDone()
		}
		return nil
	}

	return rg.close(code)
}

// broadcastClosePackets sends the error + close packet on every leg. It takes
// the leg snapshot (a copy of rg.legs) rather than reading rg.legs,
// because it must run WITHOUT rg.mu: a dead transport makes it take the full
// closeRoutineTimeout, and rg.mu is the router's whole packet-dispatch lock.
func (rg *RouteGroup) broadcastClosePackets(code routing.CloseCode, legs []groupLeg) {
	// Use a timeout context to prevent blocking forever on dead transports.
	// Without this, a dead transport causes writePacket to block indefinitely,
	// deadlocking the GC goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	for _, lg := range legs {
		tp, fwd := lg.tp, lg.fwd
		if tp == nil || fwd == nil {
			continue
		}

		if err := rg.sendError(ctx, fwd, tp); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send error packet to %s", tp.Remote())
		}

		packet := routing.MakeClosePacket(fwd.NextRouteID(), code)
		if err := rg.writePacket(ctx, tp, packet, fwd.KeyRouteID()); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send close packet to %s", tp.Remote())
		}
	}
}

// closeDone returns the channel the close initiator waits on, creating it on
// first use. One channel for the group's lifetime: a repeat close finds it
// already signaled and does not wait again, which is what a caller that is
// only trying not to deadlock wants anyway.
func (rg *RouteGroup) closeDone() chan struct{} {
	rg.closeDoneMu.Lock()
	defer rg.closeDoneMu.Unlock()
	if rg.closeDoneCh == nil {
		rg.closeDoneCh = make(chan struct{})
	}
	return rg.closeDoneCh
}

// signalCloseDone releases every waiter, exactly once.
func (rg *RouteGroup) signalCloseDone() {
	ch := rg.closeDone()
	rg.closeDoneOnce.Do(func() { close(ch) })
}

func (rg *RouteGroup) waitForCloseRouteGroup(waitTimeout time.Duration) error {
	select {
	case <-rg.closeDone():
		return nil
	case <-time.After(waitTimeout):
		// Force-complete: zero the counter and signal the channel.
		rg.closeDonePending.Store(0)
		rg.signalCloseDone()
		return fmt.Errorf("close route group timed out after %v", waitTimeout)
	}
}

func (rg *RouteGroup) isCloseInitiator() bool {
	return rg.closeInitiated.Load() == 1
}

func (rg *RouteGroup) setRemoteClosed() {
	rg.remoteClosedOnce.Do(func() {
		close(rg.remoteClosed)
	})
}

func (rg *RouteGroup) isRemoteClosed() bool {
	return chanClosed(rg.remoteClosed)
}

func (rg *RouteGroup) isClosed() bool {
	return chanClosed(rg.closed)
}

func chanClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}

// SetCloseObserver installs a one-shot callback fired when this group closes,
// carrying the group's age and the total payload it received. The dialer uses
// it to record a route that died young without ever moving a byte (see
// dead_route_cache.go). Safe to call at most once per group; a nil fn clears it.
func (rg *RouteGroup) SetCloseObserver(fn func(age time.Duration, carried uint64)) {
	rg.closeObserverMu.Lock()
	rg.closeObserver = fn
	rg.closeObserverMu.Unlock()
}

// fireCloseObserver runs — and then drops — the close observer, so it is called
// exactly once even if close() is reached twice.
func (rg *RouteGroup) fireCloseObserver() {
	rg.closeObserverMu.Lock()
	fn := rg.closeObserver
	rg.closeObserver = nil
	rg.closeObserverMu.Unlock()
	if fn == nil {
		return
	}
	var age time.Duration
	if !rg.createdAt.IsZero() {
		age = time.Since(rg.createdAt)
	}
	fn(age, rg.networkStats.BandwidthReceived()+rg.networkStats.BandwidthSent())
}
