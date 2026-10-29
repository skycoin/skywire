// Package visor pkg/visor/netmove.go c3-vis-core
//
// Reacting to a change of network. The visor learns its public IP, NAT class
// and LAN addresses once at startup and keeps re-sending them, so after a move
// to another network the address resolver keeps the old endpoints alive and
// peers dial addresses that no longer reach it. networkMoved re-learns all of
// them and makes every registration carry the new ones.
package visor

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/transport/network"
	"github.com/skycoin/skywire/pkg/transport/network/addrresolver"
	"github.com/skycoin/skywire/pkg/visor/visorapi"
)

const (
	defaultNetworkWatchInterval  = 12 * time.Second
	defaultPublicIPCheckInterval = 12 * time.Minute
)

var (
	// netMoveDebounce gathers a burst of triggers into one refresh.
	netMoveDebounce = 2 * time.Second
	// netMoveMinGap is the least time between two refreshes.
	netMoveMinGap = 30 * time.Second
	// netMoveDmsgWait bounds the wait for dmsg sessions after a reconnect.
	netMoveDmsgWait = 15 * time.Second
)

// netMoveState records what the visor knows about its network and runs the
// refreshes. Its zero value is ready to use.
type netMoveState struct {
	mu        sync.Mutex
	scheduled bool     // a refresh goroutine is queued or running
	reasons   []string // triggers since the last refresh started
	lastRun   time.Time

	publicIP, publicIPv6, publicIPSource string
	publicIPAt                           time.Time
	natAt                                time.Time

	moves                  uint64
	lastAt                 time.Time
	lastReason, lastChange string
	watchNS, ipCheckNS     atomic.Int64 // 0 = default, <0 = off
	wakeOnce               sync.Once
	wakeWatch, wakeIP      chan struct{}
	refresh                func(reason string) // tests replace the refresh
	fingerprint            func() string       // tests replace the address source
	runs                   atomic.Int64
}

func (s *netMoveState) wakeChs() (watch, ip chan struct{}) {
	s.wakeOnce.Do(func() {
		s.wakeWatch = make(chan struct{}, 1)
		s.wakeIP = make(chan struct{}, 1)
	})
	return s.wakeWatch, s.wakeIP
}

// poke makes the watcher loops re-read their intervals.
func (s *netMoveState) poke() {
	w, ip := s.wakeChs()
	for _, c := range []chan struct{}{w, ip} {
		select {
		case c <- struct{}{}:
		default:
		}
	}
}

func (s *netMoveState) notePublicIP(r publicIPResult) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.publicIP, s.publicIPv6, s.publicIPSource = r.v4, r.v6, r.source
	s.publicIPAt = time.Now()
}

func (s *netMoveState) currentPublicIP() (v4, v6 string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.publicIP, s.publicIPv6
}

func intervalOf(ns *atomic.Int64, def time.Duration) time.Duration {
	switch d := time.Duration(ns.Load()); {
	case d == 0:
		return def
	case d < 0:
		return 0
	default:
		return d
	}
}

func intervalString(d time.Duration) string {
	if d <= 0 {
		return "off"
	}
	return d.String()
}

// setStun installs a STUN result for every reader.
func (v *Visor) setStun(sc *network.StunDetails) {
	v.stun.client.Store(sc)
	v.netMove.mu.Lock()
	v.netMove.natAt = time.Now()
	v.netMove.mu.Unlock()
}

// stunPublicIP is the STUN-derived public IP, "" before STUN resolved one.
func (v *Visor) stunPublicIP() string {
	if sc := v.stun.client.Load(); sc != nil && sc.PublicIP != nil {
		return sc.PublicIP.IP()
	}
	return ""
}

// currentPublicIP is the public IPv4 the visor last determined, for the
// service discovery clients.
func (v *Visor) currentPublicIP() string {
	ip, _ := v.netMove.currentPublicIP()
	return ip
}

// networkMoved queues a refresh of everything the visor learned about its
// network. It never blocks. Triggers that arrive while one is queued or
// running are folded into a single further refresh.
func (v *Visor) networkMoved(reason string) {
	s := &v.netMove
	s.mu.Lock()
	if !slices.Contains(s.reasons, reason) {
		s.reasons = append(s.reasons, reason)
	}
	if s.scheduled {
		s.mu.Unlock()
		return
	}
	s.scheduled = true
	s.mu.Unlock()
	go v.runNetworkMoves()
}

func (v *Visor) runNetworkMoves() {
	s := &v.netMove
	ctx := v.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	for {
		s.mu.Lock()
		wait := netMoveDebounce
		if gap := time.Until(s.lastRun.Add(netMoveMinGap)); gap > wait {
			wait = gap
		}
		s.mu.Unlock()
		select {
		case <-ctx.Done():
			s.mu.Lock()
			s.scheduled = false
			s.mu.Unlock()
			return
		case <-time.After(wait):
		}

		s.mu.Lock()
		reason := strings.Join(s.reasons, ", ")
		s.reasons = nil
		s.lastRun = time.Now()
		refresh := s.refresh
		s.mu.Unlock()

		if refresh == nil {
			refresh = v.refreshNetwork
		}
		s.runs.Add(1)
		refresh(reason)

		s.mu.Lock()
		if len(s.reasons) == 0 {
			s.scheduled = false
			s.mu.Unlock()
			return
		}
		s.mu.Unlock()
	}
}

// refreshNetwork re-learns the public IP, NAT class and LAN addresses and
// makes every registration carry them.
func (v *Visor) refreshNetwork(reason string) {
	log := v.netMoveLog()
	ctx := v.ctx
	if ctx == nil {
		ctx = context.Background()
	}
	before := v.NetworkState()

	v.initLock.Lock()
	arClient := v.arClient
	v.initLock.Unlock()

	// Sessions dialed from the old network can linger. The public IP lookup
	// asks these sessions, so they have to be fresh first.
	if v.dmsgC != nil {
		v.dmsgC.ForceReconnect()
		deadline := time.Now().Add(netMoveDmsgWait)
		for len(v.dmsgC.AllSessions()) == 0 && time.Now().Before(deadline) {
			select {
			case <-ctx.Done():
				return
			case <-time.After(500 * time.Millisecond):
			}
		}
	}

	if runtime.GOOS != "js" && v.conf != nil && len(v.conf.StunServers) > 0 {
		if sc := network.GetStunDetails(v.conf.StunServers, log); sc != nil && !stunIsTransientFail(sc.NATType) {
			v.setStun(sc)
		}
	}

	if v.dmsgC != nil {
		v.applyPublicIP(arClient, v.lookupPublicIP(ctx, log))
	}

	v.readvertise()

	after := v.NetworkState()
	change := describeNetworkChange(before, after)
	v.netMove.mu.Lock()
	v.netMove.moves++
	v.netMove.lastAt = time.Now()
	v.netMove.lastReason = reason
	v.netMove.lastChange = change
	v.netMove.mu.Unlock()
	log.WithField("reason", reason).Infof("Network change handled: %s", change)
}

func describeNetworkChange(before, after *visorapi.NetworkState) string {
	var parts []string
	pair := func(name, a, b string) {
		if a == "" {
			a = "none"
		}
		if b == "" {
			b = "none"
		}
		if a == b {
			parts = append(parts, name+" "+a)
		} else {
			parts = append(parts, name+" "+a+" -> "+b)
		}
	}
	pair("public ip", before.PublicIP, after.PublicIP)
	if before.PublicIPv6 != "" || after.PublicIPv6 != "" {
		pair("public ipv6", before.PublicIPv6, after.PublicIPv6)
	}
	pair("nat", before.NAT, after.NAT)
	pair("lan", strings.Join(before.LocalAddresses, ","), strings.Join(after.LocalAddresses, ","))
	return strings.Join(parts, "; ")
}

func (v *Visor) netMoveLog() *logging.Logger {
	if v.conf == nil {
		return logging.MustGetLogger("network_move")
	}
	if mlog := v.MasterLogger(); mlog != nil {
		return mlog.PackageLogger("network_move")
	}
	return logging.MustGetLogger("network_move")
}

// NetworkState reports the visor's view of its network.
func (v *Visor) NetworkState() *visorapi.NetworkState {
	s := &v.netMove
	s.mu.Lock()
	out := &visorapi.NetworkState{
		PublicIP:              s.publicIP,
		PublicIPv6:            s.publicIPv6,
		PublicIPSource:        s.publicIPSource,
		PublicIPAt:            s.publicIPAt,
		NATAt:                 s.natAt,
		Moves:                 s.moves,
		LastMoveAt:            s.lastAt,
		LastMoveReason:        s.lastReason,
		LastMoveChange:        s.lastChange,
		Pending:               s.scheduled,
		WatchInterval:         intervalString(intervalOf(&s.watchNS, defaultNetworkWatchInterval)),
		PublicIPCheckInterval: intervalString(intervalOf(&s.ipCheckNS, defaultPublicIPCheckInterval)),
	}
	s.mu.Unlock()
	if sc := v.stun.client.Load(); sc != nil {
		out.NAT = sc.NATType.String()
	}
	if addrs, excluded, err := netutil.LocalAddressesDetail(); err == nil {
		sort.Strings(addrs)
		sort.Strings(excluded)
		out.LocalAddresses = addrs
		out.PolicyExcluded = excluded
	}
	if p := netutil.AdvertisePolicy(); !p.IsZero() {
		out.Allow, out.Deny = p.Allow, p.Deny
	}
	v.reach.mu.Lock()
	for t, raw := range v.reach.binds {
		var la addrresolver.LocalAddresses
		if err := json.Unmarshal([]byte(raw), &la); err == nil {
			if out.Advertised == nil {
				out.Advertised = make(map[string][]string)
			}
			out.Advertised[t] = la.Addresses
		}
	}
	v.reach.mu.Unlock()
	return out
}

// NetworkMoved is the RPC entry point: refresh as if the network changed.
func (v *Visor) NetworkMoved(reason string) error {
	if reason == "" {
		reason = "manual"
	}
	v.networkMoved(reason)
	return nil
}

// netFingerprint summarizes the addresses the visor would advertise and the
// route to the internet. IPv6 counts by /64 prefix, so rotating privacy
// addresses do not read as a move.
func netFingerprint() string {
	addrs, err := netutil.LocalAddresses()
	if err != nil {
		return ""
	}
	set := make(map[string]bool, len(addrs))
	for _, a := range addrs {
		ip := net.ParseIP(a)
		if ip == nil {
			continue
		}
		if ip.To4() == nil {
			a = ip.Mask(net.CIDRMask(64, 128)).String() + "/64"
		}
		set[a] = true
	}
	v4, v6 := netutil.RouteSourceIPs()
	if v4 != "" && set[v4] {
		set["route4="+v4] = true
	}
	if ip := net.ParseIP(v6); ip != nil && set[ip.Mask(net.CIDRMask(64, 128)).String()+"/64"] {
		set["route6="+ip.Mask(net.CIDRMask(64, 128)).String()] = true
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return strings.Join(keys, ",")
}

// watchNetwork calls networkMoved whenever the address fingerprint changes.
func (v *Visor) watchNetwork(ctx context.Context) {
	s := &v.netMove
	wake, _ := s.wakeChs()
	fp := s.fingerprint
	if fp == nil {
		fp = netFingerprint
	}
	last := fp()
	for {
		d := intervalOf(&s.watchNS, defaultNetworkWatchInterval)
		var tick <-chan time.Time
		if d > 0 {
			tick = time.After(d)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			continue
		case <-tick:
		}
		cur := fp()
		if cur == last || cur == "" {
			continue
		}
		last = cur
		v.networkMoved("addresses changed")
	}
}

// checkPublicIP asks the dmsg servers for the visor's address now and then,
// and calls networkMoved when it differs from the one in use.
func (v *Visor) checkPublicIP(ctx context.Context) {
	s := &v.netMove
	_, wake := s.wakeChs()
	for {
		d := intervalOf(&s.ipCheckNS, defaultPublicIPCheckInterval)
		var tick <-chan time.Time
		if d > 0 {
			tick = time.After(d)
		}
		select {
		case <-ctx.Done():
			return
		case <-wake:
			continue
		case <-tick:
		}
		if v.dmsgC == nil {
			continue
		}
		v4, v6 := v.dmsgC.LookupIPsByFamily(ctx)
		cur4, cur6 := s.currentPublicIP()
		if moved, why := publicIPMoved(cur4, cur6, v4, v6); moved {
			v.networkMoved(why)
		}
	}
}

// publicIPMoved compares a fresh lookup with the addresses in use. A family
// the lookup did not answer for is not evidence of a move.
func publicIPMoved(cur4, cur6 string, v4, v6 net.IP) (bool, string) {
	if v4 != nil && v4.String() != cur4 {
		return true, fmt.Sprintf("public ip %s -> %s", orNone(cur4), v4)
	}
	if v6 != nil && cur6 != "" && v6.String() != cur6 {
		return true, fmt.Sprintf("public ipv6 %s -> %s", cur6, v6)
	}
	return false, ""
}

func orNone(s string) string {
	if s == "" {
		return "none"
	}
	return s
}

// setNetworkIntervals applies the configured watcher periods live.
func (v *Visor) setNetworkIntervals(watch, ipCheck time.Duration) {
	v.netMove.watchNS.Store(int64(watch))
	v.netMove.ipCheckNS.Store(int64(ipCheck))
	v.netMove.poke()
}

// initNetworkWatch starts the address watcher and the public IP recheck.
func initNetworkWatch(_ context.Context, v *Visor, _ *logging.Logger) error {
	if runtime.GOOS == "js" {
		return nil
	}
	if v.conf != nil && v.conf.Transport != nil {
		v.setNetworkIntervals(time.Duration(v.conf.Transport.NetworkWatchInterval),
			time.Duration(v.conf.Transport.PublicIPCheckInterval))
	}
	ctx := v.ctx
	if ctx == nil {
		return nil
	}
	go v.watchNetwork(ctx)
	go v.checkPublicIP(ctx)
	return nil
}
