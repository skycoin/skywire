// Package visor pkg/visor/netonline.go c3-vis-core
//
// Whether the visor can reach the skywire network. It is online while it
// holds a dmsg session and offline once it has held none for offlineGrace,
// or right away when an app or the browser reports that the device has no
// network. Coming back online refreshes every registration at once.
package visor

import (
	"context"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

var (
	// onlineCheckInterval is how often the dmsg sessions are counted.
	onlineCheckInterval = 2 * time.Second
	// offlineGrace is how long the visor goes without a dmsg session before
	// it counts as offline.
	offlineGrace = 30 * time.Second
	// reportedOfflineHold bounds how long a report of no network outweighs
	// the dmsg sessions, which can outlive the network by a keepalive.
	reportedOfflineHold = 2 * time.Minute
)

// netBackOnline is the networkMoved reason for a return from offline. Its
// sessions were just dialed, so the refresh keeps them.
const netBackOnline = "back online"

// onlineState is the connectivity state machine. Its zero value starts
// connecting at the first observation.
type onlineState struct {
	state      string
	since      time.Time
	reason     string
	changes    uint64
	start      time.Time // first observation
	lastSeen   time.Time // last observation with a dmsg session
	reportedAt time.Time // an app or the browser said there is no network
	reportedBy string
	sessions   func() int // tests replace the dmsg session count
}

// observe takes the dmsg session count at now and the mode in force. It
// returns the log line of a state change, "" when there was none, and
// whether the change was a return from offline.
func (s *onlineState) observe(now time.Time, sessions int, mode string) (line string, backOnline bool) {
	if s.start.IsZero() {
		s.start, s.state, s.since = now, visorapi.ConnConnecting, now
	}
	if sessions > 0 {
		s.lastSeen = now
	}
	target, reason := s.state, s.reason
	quiet := s.lastSeen
	if quiet.IsZero() {
		quiet = s.start
	}
	switch {
	case !s.reportedAt.IsZero() && now.Sub(s.reportedAt) < reportedOfflineHold:
		target, reason = visorapi.ConnOffline, s.reportedBy+" reported no network"
	case sessions > 0:
		target, reason = visorapi.ConnOnline, fmt.Sprintf("%d dmsg sessions", sessions)
	case mode == visorapi.ModeOffline:
		target, reason = visorapi.ConnOffline, "network mode offline"
	case now.Sub(quiet) >= offlineGrace:
		target, reason = visorapi.ConnOffline, fmt.Sprintf("no dmsg sessions for %s", offlineGrace)
	}
	if target == s.state {
		if target == visorapi.ConnOnline {
			s.reason = reason
		}
		return "", false
	}
	prev := s.state
	s.state, s.since, s.reason = target, now, reason
	s.changes++
	return fmt.Sprintf("network: %s (%s)", target, reason), prev == visorapi.ConnOffline && target == visorapi.ConnOnline
}

// report records an app's or the browser's word on the network. Offline
// holds until a later report or reportedOfflineHold.
func (s *onlineState) report(now time.Time, offline bool, by string) {
	if offline {
		s.reportedAt, s.reportedBy = now, by
		return
	}
	s.reportedAt, s.reportedBy = time.Time{}, ""
}

func (s *onlineState) snapshot() visorapi.Connectivity {
	st := s.state
	if st == "" {
		st = visorapi.ConnConnecting
	}
	return visorapi.Connectivity{
		State:   st,
		Online:  st == visorapi.ConnOnline,
		Since:   s.since,
		Reason:  s.reason,
		Changes: s.changes,
	}
}

func (v *Visor) dmsgSessionCount() int {
	if f := v.netMove.online.sessions; f != nil {
		return f()
	}
	if v.dmsgC == nil {
		return 0
	}
	return len(v.dmsgC.AllSessions())
}

func (v *Visor) currentMode() string {
	v.meter.mu.Lock()
	defer v.meter.mu.Unlock()
	return v.meter.mode
}

// checkOnline takes one observation, logs a change and refreshes the
// network on a return from offline.
func (v *Visor) checkOnline(now time.Time) {
	n, mode := v.dmsgSessionCount(), v.currentMode()
	s := &v.netMove
	s.mu.Lock()
	line, back := s.online.observe(now, n, mode)
	s.mu.Unlock()
	if line != "" {
		v.netMoveLog().Info(line)
	}
	if back {
		v.networkMoved(netBackOnline)
	}
}

// reportNetwork takes an app's or the browser's word that the device has a
// network or not. A report of one asks for a refresh with reason, since the
// dmsg client may be waiting out a backoff.
func (v *Visor) reportNetwork(online bool, by, reason string) {
	s := &v.netMove
	s.mu.Lock()
	s.online.report(time.Now(), !online, by)
	s.mu.Unlock()
	v.checkOnline(time.Now())
	if online {
		v.networkMoved(reason)
	}
}

// Connectivity is the visor's current connectivity state.
func (v *Visor) Connectivity() visorapi.Connectivity {
	s := &v.netMove
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.online.snapshot()
}

// watchOnline observes the connectivity state until ctx ends.
func (v *Visor) watchOnline(ctx context.Context) {
	v.checkOnline(time.Now())
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-time.After(onlineCheckInterval):
			v.checkOnline(now)
		}
	}
}
