// Package visor pkg/visor/netmeter.go c3-vis-core
//
// Metered networks. A phone on mobile data, or any link the operator pays for
// by the byte, should not carry other visors' traffic. The visor learns that
// the network is metered from an app (POST /api/network), from the operator
// (`network set --metered`), or from a rule naming the interfaces that are
// (transport.network.metered_interfaces), and then runs in the configured
// metered mode until the signal ends.
package visor

import (
	"fmt"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

// dmsgOnlySessions is how many dmsg sessions dmsg-only mode keeps.
const dmsgOnlySessions = 2

// meterLevers are the switches a mode change pulls. Tests replace them.
type meterLevers interface {
	pauseAutoconnect(on bool)
	pauseTransit(on bool)
	pauseRelay(on bool)
	restrictCreate(r transport.CreateRestriction)
	closeDirect() int
	capDmsgSessions(n int) // n <= 0 restores the configured count
	pauseDmsgDial(on bool)
}

// meterState is what the visor knows about the network being metered and
// the mode it runs in. Its zero value is ready to use.
type meterState struct {
	mu         sync.Mutex
	manual     *bool  // network set --metered; nil follows the other signals
	reported   *bool  // last report from an app
	reporter   string // who reported it
	reportedIf string
	rule       bool // the route leaves through a metered_interfaces match
	ruleIf     string
	mode       string // applied mode, "" before the first evaluation
	since      time.Time
	pending    bool // a lever was not ready at the last apply

	// applyMu serializes mode changes and guards what the levers remember
	// to restore. Levers can block, so they never run under mu.
	applyMu    sync.Mutex
	acPaused   bool // autoconnect was running when a mode stopped it
	dmsgMinWas int  // dmsg min sessions before dmsg-only capped them

	// lean is read on hot paths: inbound gate and reach declaration.
	lean atomic.Bool

	levers meterLevers                // nil uses the visor
	routes func() []netutil.IfaceAddr // nil uses netutil.RouteInterfaces
}

func modeLevel(m string) int {
	if i := slices.Index(visorapi.Modes, m); i >= 0 {
		return i
	}
	return 0
}

// validMeteredMode accepts a mode name, "" meaning the default.
func validMeteredMode(m string) error {
	if m == "" || slices.Contains(visorapi.Modes, m) {
		return nil
	}
	return fmt.Errorf("unknown metered mode %q, want one of %v", m, visorapi.Modes)
}

// meteredMode is the configured mode for a metered network.
func (v *Visor) meteredMode() string {
	if p := v.networkPolicy(); p.Metered != "" {
		return p.Metered
	}
	return visorapi.ModeLean
}

// meteredNow combines the signals: the operator's override first, then an
// app's report, then the interface rule. Caller holds s.mu.
func (s *meterState) meteredNow() (bool, string) {
	switch {
	case s.manual != nil:
		return *s.manual, "manual"
	case s.reported != nil && *s.reported:
		return true, s.reporter
	case s.rule:
		return true, "rule " + s.ruleIf
	}
	return false, ""
}

// evalMeterRule checks the route interface against metered_interfaces.
func (v *Visor) evalMeterRule() {
	rules := v.networkPolicy().MeteredInterfaces
	s := &v.meter
	hit, name := false, ""
	if len(rules) > 0 {
		routes := s.routes
		if routes == nil {
			routes = netutil.RouteInterfaces
		}
		for _, r := range routes() {
			for _, e := range rules {
				if netutil.MatchPolicyEntry(e, r.Name, r.IP) {
					hit, name = true, r.Name
				}
			}
		}
	}
	s.mu.Lock()
	s.rule, s.ruleIf = hit, name
	s.mu.Unlock()
}

// meterEvaluate re-reads the rule and applies the mode the signals call for.
func (v *Visor) meterEvaluate() {
	v.evalMeterRule()
	v.applyMeterMode()
}

// applyMeterMode switches to the mode the current signals call for.
func (v *Visor) applyMeterMode() {
	s := &v.meter
	s.applyMu.Lock()
	defer s.applyMu.Unlock()
	want := visorapi.ModeNormal
	s.mu.Lock()
	metered, source := s.meteredNow()
	old, pending, levers := s.mode, s.pending, s.levers
	s.mu.Unlock()
	if metered {
		want = v.meteredMode()
	}
	if old == want && !pending {
		return
	}
	if levers == nil {
		levers = visorLevers{v}
	}
	// Gate the hot paths first when cutting, last when restoring.
	if modeLevel(want) >= 1 {
		s.lean.Store(true)
	}
	ok := applyLevers(levers, modeLevel(old), modeLevel(want))
	s.lean.Store(modeLevel(want) >= 1)
	s.mu.Lock()
	s.pending = !ok
	if old != want {
		s.mode, s.since = want, time.Now()
	}
	s.mu.Unlock()
	if old != want {
		if old != "" || want != visorapi.ModeNormal {
			v.netMoveLog().WithField("metered", metered).WithField("source", source).
				Infof("Network mode %s -> %s", orNone(old), want)
		}
	}
}

// applyLevers moves from mode level old to level want. It reports false when
// a lever had nothing to act on yet, so the next evaluation tries again.
func applyLevers(l meterLevers, old, want int) bool {
	l.pauseAutoconnect(want >= 1)
	l.pauseTransit(want >= 1)
	l.pauseRelay(want >= 1)
	switch {
	case want >= 3:
		l.restrictCreate(transport.RestrictAll)
	case want == 2:
		l.restrictCreate(transport.RestrictDirect)
	default:
		l.restrictCreate(transport.RestrictNone)
	}
	if want >= 2 && old < 2 {
		l.closeDirect()
	}
	if want == 2 {
		l.capDmsgSessions(dmsgOnlySessions)
	} else {
		l.capDmsgSessions(0)
	}
	l.pauseDmsgDial(want >= 3)
	if vl, ok := l.(visorLevers); ok {
		return vl.ready()
	}
	return true
}

// visorLevers pulls the real switches.
type visorLevers struct{ v *Visor }

func (l visorLevers) ready() bool {
	v := l.v
	if v.initLock == nil {
		return true
	}
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.router != nil && v.tpM != nil && v.dmsgC != nil
}

func (l visorLevers) pauseAutoconnect(on bool) {
	v := l.v
	s := &v.meter
	if on {
		if v.IsPublicAutoconnectRunning() {
			_ = v.StopPublicAutoconnect() //nolint:errcheck
			s.acPaused = true
		}
		return
	}
	if s.acPaused {
		s.acPaused = false
		if err := v.StartPublicAutoconnect(); err != nil {
			v.netMoveLog().WithError(err).Warn("Could not restart public autoconnect")
		}
	}
}

func (l visorLevers) pauseTransit(on bool) {
	if r, ok := l.v.routerLocked().(interface{ SetTransitPaused(bool) }); ok {
		r.SetTransitPaused(on)
	}
}

func (l visorLevers) pauseRelay(on bool) {
	if c := l.v.dmsgC; c != nil {
		c.SetRelayPaused(on)
	}
}

func (l visorLevers) restrictCreate(r transport.CreateRestriction) {
	if tm := l.v.tpMLocked(); tm != nil {
		tm.SetCreateRestriction(r)
	}
}

func (l visorLevers) closeDirect() int {
	if tm := l.v.tpMLocked(); tm != nil {
		n := tm.CloseDirectTransports()
		l.v.netMoveLog().Infof("Closed %d direct transports", n)
		return n
	}
	return 0
}

func (l visorLevers) capDmsgSessions(n int) {
	v := l.v
	c := v.dmsgC
	if c == nil {
		return
	}
	s := &v.meter
	if n <= 0 {
		if s.dmsgMinWas != 0 {
			c.SetMinSessions(s.dmsgMinWas)
			s.dmsgMinWas = 0
		}
		return
	}
	if s.dmsgMinWas == 0 {
		s.dmsgMinWas = c.MinSessions()
	}
	if was := s.dmsgMinWas; was == 0 || was > n {
		c.SetMinSessions(n)
	}
	// Keep the sessions carrying the most streams.
	sessions := c.AllSessions()
	if len(sessions) <= n {
		return
	}
	sort.Slice(sessions, func(i, j int) bool { return sessions[i].NumStreams() > sessions[j].NumStreams() })
	for _, ses := range sessions[n:] {
		_ = ses.Close() //nolint:errcheck,gosec
	}
}

func (l visorLevers) pauseDmsgDial(on bool) {
	if c := l.v.dmsgC; c != nil {
		c.SetDialPaused(on)
	}
}

func (v *Visor) routerLocked() any {
	if v.initLock == nil {
		return nil
	}
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	if v.router == nil {
		return nil
	}
	return v.router
}

func (v *Visor) tpMLocked() *transport.Manager {
	if v.initLock == nil {
		return nil
	}
	v.initLock.RLock()
	defer v.initLock.RUnlock()
	return v.tpM
}

// NetworkEvent takes an app's report: a move refreshes what the visor
// advertises, and a metered flag may change the mode.
func (v *Visor) NetworkEvent(ev visorapi.NetworkEvent) error {
	switch ev.Event {
	case "moved", "changed", "":
	default:
		return fmt.Errorf("unknown network event %q", ev.Event)
	}
	src := ev.Source
	if src == "" {
		src = "android"
	}
	s := &v.meter
	s.mu.Lock()
	if ev.Metered != nil {
		m := *ev.Metered
		s.reported, s.reporter = &m, src
	}
	s.reportedIf = ev.Interface
	s.mu.Unlock()
	if ev.Event == "moved" {
		v.networkMoved(src)
	}
	v.meterEvaluate()
	return nil
}

// setMeteredOverride takes `network set --metered`: "true", "false" or "auto".
func (v *Visor) setMeteredOverride(val string) error {
	var m *bool
	switch val {
	case "true", "yes", "on":
		t := true
		m = &t
	case "false", "no", "off":
		f := false
		m = &f
	case "auto", "":
	default:
		return fmt.Errorf("--metered wants true, false or auto, not %q", val)
	}
	v.meter.mu.Lock()
	v.meter.manual = m
	v.meter.mu.Unlock()
	return nil
}

// meterNetworkState fills the metered part of the network section.
func (v *Visor) meterNetworkState(out *visorapi.NetworkState) {
	s := &v.meter
	s.mu.Lock()
	out.Metered, out.MeteredSource = s.meteredNow()
	out.Mode, out.ModeSince = s.mode, s.since
	if out.Mode == "" {
		out.Mode = visorapi.ModeNormal
	}
	s.mu.Unlock()
	out.MeteredMode = v.meteredMode()
	out.MeteredInterfaces = v.networkPolicy().MeteredInterfaces
}
