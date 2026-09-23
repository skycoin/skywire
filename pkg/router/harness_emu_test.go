// Package router pkg/router/harness_emu_test.go c2-net-routing
//
// The emulated multipath testbed: two RouteGroups joined by N emulated legs,
// each leg's two directions independently shaped (delay, jitter, loss, rate,
// queue, reorder, cut) by pkg/router/emu. It runs the REAL send path
// (RouteGroup.Write -> nextTransport -> selectTransport -> ManagedTransport)
// and the REAL receive path (handlePacket -> serveIntake -> handleDataPacket
// -> reorder/SACK/RACK/TLP), so scheduler placement, retransmit recovery,
// probe-only rulings, park/promote and failover are all exercised — under
// `go test`, with no root, no rig and no live deployment.
//
// The harness lives in package router because a leg is a (rg.tps, rg.fwd,
// rg.rvs, rg.mux) tuple and none of those are exported. pkg/router/emu holds
// everything that does NOT need package internals: the wire itself and the
// report format.
//
// See docs/design/emulated-testbed.md.
package router

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"math/rand"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/emu"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// emuLegSpec is one leg of the emulated group.
type emuLegSpec struct {
	// Name labels the leg in the summary table.
	Name string
	// Up shapes initiator->acceptor frames (the client's upload), Down
	// shapes acceptor->initiator (the download).
	Up, Down emu.LinkConfig
	// LatencyMs is the FIRST-HOP RTT both ends' ManagedTransport reports —
	// the ECF/BDP anchor. Set it to the real round trip of the emulated
	// pipe (Up.Delay + Down.Delay) unless a scenario is about the two
	// disagreeing.
	LatencyMs float64
	// Direct marks a one-hop leg: its transport's remote is the peer
	// endpoint, not an intermediary. Only meaningful with Directional.
	Direct bool
}

// emuOpts configures a rig. The zero value is a plain SACK mux — what a
// two-leg group negotiates today.
type emuOpts struct {
	Legs []emuLegSpec
	// Directional negotiates CapUniDir (upload confined to one leg,
	// download spread over the multihop legs).
	Directional bool
	// HolRetx negotiates CapHOLRetx (proactive frontier retransmit).
	HolRetx bool
	// LegState negotiates CapLegState (park/promote mirrored to the peer).
	LegState bool
	// LegRehome negotiates CapLegRehome (a standby group's whole chain can be
	// adopted as one of this group's mux legs in place — leg_rehome.go).
	LegRehome bool
	// SrcPort/DstPort are the group's ports. Zero means the defaults (1/2).
	// A SECOND rig between the same two ends needs its own pair, exactly as two
	// tunnels of one app do.
	SrcPort, DstPort routing.Port
	// RidBase offsets every route ID this rig reserves, so a second rig sharing
	// an end's routing table does not collide with the first.
	RidBase int
	// Shared, when set, makes this rig use another rig's routing tables and
	// register its groups with that rig's per-end dispatcher, so both rigs'
	// chains are dispatched BY RULE at each end. That is what lets a chain move
	// from one group to the other and keep being delivered (see emuHost).
	Shared *emuShared
	// Liveness starts the leg-liveness and data-progress service loops, the
	// ones that demote and prune a leg that stops carrying. Off by default:
	// they take tens of seconds to rule, so only a failover scenario wants
	// them.
	Liveness bool
	// ReadChBufSize overrides the route groups' read channel depth.
	ReadChBufSize int
}

// emuEnd is one side of the rig.
type emuEnd struct {
	rg    *RouteGroup
	rt    routing.Table
	host  *emuHost
	conns []*emu.Conn
	tps   []*transport.ManagedTransport
}

// emuHost is one visor's packet dispatch for a rig set: the routing table and
// the descriptor→group map the real router keeps (router_packet.go
// dispatchToRouteGroup). A single-rig scenario does not need it — every frame
// on a leg belongs to that rig's only group — but a chain that is RE-HOMED from
// one group to another stops being dispatchable by "which rig owns the conn",
// which is exactly the property leg_rehome.go changes. Also serves as the
// legRehomeHost the exit uses to resolve the target group.
type emuHost struct {
	mu     sync.Mutex
	rt     routing.Table
	groups map[routing.RouteDescriptor]*RouteGroup
}

func newEmuHost(rt routing.Table) *emuHost {
	return &emuHost{rt: rt, groups: make(map[routing.RouteDescriptor]*RouteGroup)}
}

func (h *emuHost) register(rg *RouteGroup) {
	h.mu.Lock()
	h.groups[rg.desc] = rg
	h.mu.Unlock()
}

// rehomeGroupFor implements legRehomeHost.
func (h *emuHost) rehomeGroupFor(desc routing.RouteDescriptor) *RouteGroup {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.groups[desc]
}

// dispatch routes one frame the way the router does: rule by route ID,
// descriptor from the rule, group from the descriptor.
func (h *emuHost) dispatch(pkt routing.Packet) {
	rule, err := h.rt.Rule(pkt.RouteID())
	if err != nil || rule == nil {
		return
	}
	if t := rule.Type(); t != routing.RuleReverse && t != routing.RuleForward {
		return
	}
	if rg := h.rehomeGroupFor(rule.RouteDescriptor()); rg != nil {
		_ = rg.handlePacket(pkt) //nolint:errcheck // a dropped packet is the SACK layer's problem
	}
}

// emuShared is the pair of per-end hosts two rigs between the same two visors
// share. Build one with newEmuShared and pass it to both rigs' emuOpts.
type emuShared struct {
	A, B *emuHost
}

// newEmuShared builds the two hosts (one per end) a rig set shares.
func newEmuShared(ml *logging.MasterLogger) *emuShared {
	return &emuShared{
		A: newEmuHost(routing.NewTable(ml.PackageLogger("emu-rt-a"))),
		B: newEmuHost(routing.NewTable(ml.PackageLogger("emu-rt-b"))),
	}
}

// emuRig is a running two-endpoint emulated group.
//
//	newEmuRig(t, opts)            build and start both ends
//	rig.Transfer(dir, n)          stream n bytes one way, return a Summary
//	rig.Leg(i).CutDown()/Restore  black-hole a direction mid-transfer
//	rig.Summary(name, dir, ...)   snapshot the counters into a report row
type emuRig struct {
	t    *testing.T
	A    *emuEnd // initiator — the client, the upload sender
	B    *emuEnd // acceptor  — the exit, the download sender
	legs []emuLegSpec
	ids  []uuid.UUID

	rulingMu sync.Mutex
	rulings  []string

	stop   chan struct{}
	closed sync.Once

	inflight  atomic.Pointer[emuTransfer]
	sendingUp atomic.Bool
}

// emuLeg addresses one leg's two directions.
type emuLeg struct {
	rig *emuRig
	idx int
}

// Leg returns a handle on leg i.
func (r *emuRig) Leg(i int) *emuLeg { return &emuLeg{rig: r, idx: i} }

// CutUp black-holes the initiator->acceptor direction of the leg.
func (l *emuLeg) CutUp() { l.rig.A.conns[l.idx].Egress().Cut() }

// CutDown black-holes the acceptor->initiator direction of the leg.
func (l *emuLeg) CutDown() { l.rig.B.conns[l.idx].Egress().Cut() }

// Cut black-holes both directions — the whole leg goes away.
func (l *emuLeg) Cut() { l.CutUp(); l.CutDown() }

// RemoveTransport is `skywire cli tp rm` on this leg: the leg's transport is
// closed at both ends and the transport manager's close hook fires, which is
// what the router registers for (router.closeLegsOnTransport). Unlike Cut —
// which black-holes a link whose socket is still open, the shape the liveness
// probe exists for — this is a link the visor itself took away.
func (l *emuLeg) RemoveTransport() {
	l.Cut()
	l.rig.A.tps[l.idx].CloseForTest()
	l.rig.B.tps[l.idx].CloseForTest()
	id := l.rig.ids[l.idx]
	l.rig.A.rg.handleTransportClosed(id)
	l.rig.B.rg.handleTransportClosed(id)
}

// LegsOn is how many legs the given end still holds.
func (r *emuRig) LegsOn(initiator bool) int {
	a, b := r.AddedLegs()
	if initiator {
		return a
	}
	return b
}

// Restore lifts a Cut on both directions.
func (l *emuLeg) Restore() {
	l.rig.A.conns[l.idx].Egress().Restore()
	l.rig.B.conns[l.idx].Egress().Restore()
}

// WireBytes is everything both directions put on this leg.
func (l *emuLeg) WireBytes() uint64 {
	return l.rig.A.conns[l.idx].Egress().Stats().SentBytes +
		l.rig.B.conns[l.idx].Egress().Stats().SentBytes
}

const (
	emuRidAFwd = 1000 // A's forward rules  (key)
	emuRidACon = 2000 // A's consume rules  (key) — B forwards to these
	emuRidBFwd = 3000 // B's forward rules  (key)
	emuRidBCon = 4000 // B's consume rules  (key) — A forwards to these
)

// newEmuRig builds both route groups, the emulated legs between them, and the
// reader goroutines that feed each end's packet intake. Everything is torn
// down by t.Cleanup.
func newEmuRig(t *testing.T, opts emuOpts) *emuRig {
	t.Helper()
	// The netem links pace every frame with its own timer: at 3 MB/s a frame's
	// service time is a few hundred microseconds, so the emulation needs a timer
	// that fires on that order. Go's Windows runtime does not have one — its
	// timers are bounded by the system timer-interrupt period — and the measured
	// result is an emulated link running ~25x under its configured rate: the
	// windows lane's cut-busiest-leg row delivered 7.5 of 8 MB in 25 s (~300 KB/s
	// against a 9 MB/s nominal), so the scenario's deadline and its 2 s
	// resume-after-cut bar are measuring the runner's clock, not the scheduler.
	// The linux and darwin lanes run the whole suite, race detector included.
	if runtime.GOOS == "windows" {
		t.Skip("emulated-link pacing needs sub-millisecond timers; the windows runtime's are interrupt-period bound")
	}
	if len(opts.Legs) == 0 {
		t.Fatal("emu rig needs at least one leg")
	}

	dst, src, hopA, hopB, hopC := endpointsForTest(t)
	hops := []cipher.PubKey{hopA, hopB, hopC}

	ml := logging.NewMasterLogger()
	ml.SetLevel(logrus.PanicLevel)

	cfg := DefaultRouteGroupConfig()
	if opts.ReadChBufSize > 0 {
		cfg.ReadChBufSize = opts.ReadChBufSize
	}

	rig := &emuRig{t: t, legs: opts.Legs, stop: make(chan struct{})}

	// A is the INITIATOR (src), B the ACCEPTOR (dst).
	srcPort, dstPort := opts.SrcPort, opts.DstPort
	if srcPort == 0 {
		srcPort = 1
	}
	if dstPort == 0 {
		dstPort = 2
	}
	var hostA, hostB *emuHost
	if opts.Shared != nil {
		hostA, hostB = opts.Shared.A, opts.Shared.B
	}
	rtA, rtB := routing.NewTable(ml.PackageLogger("emu-rt-a")), routing.NewTable(ml.PackageLogger("emu-rt-b"))
	if hostA != nil {
		rtA, rtB = hostA.rt, hostB.rt
	}
	rgA := NewRouteGroup(cfg, rtA, routing.NewRouteDescriptor(src, dst, srcPort, dstPort), ml)
	rgB := NewRouteGroup(cfg, rtB, routing.NewRouteDescriptor(dst, src, dstPort, srcPort), ml)
	rgA.initiator = true
	rgB.initiator = false
	rgA.muxEvents = &muxEventRing{}
	rgB.muxEvents = &muxEventRing{}
	if hostA != nil {
		rgA.rehomeHost, rgB.rehomeHost = hostA, hostB
		hostA.register(rgA)
		hostB.register(rgB)
	}

	rig.A = &emuEnd{rg: rgA, rt: rtA, host: hostA}
	rig.B = &emuEnd{rg: rgB, rt: rtB, host: hostB}

	var fwdA, rvsA, fwdB, rvsB []routing.Rule
	for i, spec := range opts.Legs {
		tpID := uuid.New()
		rig.ids = append(rig.ids, tpID)

		name := spec.Name
		if name == "" {
			name = fmt.Sprintf("leg%d", i)
		}
		// Seeds default to the leg index so two legs configured alike still
		// drop different frames, and a rerun drops the same ones.
		up, down := spec.Up, spec.Down
		if up.Seed == 0 {
			up.Seed = int64(i)*2 + 1
		}
		if down.Seed == 0 {
			down.Seed = int64(i)*2 + 2
		}
		connA, connB := emu.NewPair(emu.PairConfig{
			Name: name, AtoB: up, BtoA: down, APK: src, BPK: dst, Type: "emu",
		})

		mtA := transport.NewManagedTransportForTest(connA)
		mtA.Entry = transport.Entry{ID: tpID, Type: "emu"}
		mtB := transport.NewManagedTransportForTest(connB)
		mtB.Entry = transport.Entry{ID: tpID, Type: "emu"}
		if spec.LatencyMs > 0 {
			mtA.SetLatency(spec.LatencyMs)
			mtB.SetLatency(spec.LatencyMs)
		}
		// Directness: a DIRECT leg's transport remote is the peer endpoint;
		// a multihop leg's is an intermediary.
		if spec.Direct {
			setRemoteForTest(mtA, dst)
			setRemoteForTest(mtB, src)
		} else {
			hop := hops[i%len(hops)]
			setRemoteForTest(mtA, hop)
			setRemoteForTest(mtB, hop)
		}

		rb := opts.RidBase
		aFwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(emuRidAFwd+rb+i), routing.RouteID(emuRidBCon+rb+i), tpID, src, dst, srcPort, dstPort) //nolint:gosec
		aRvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(emuRidACon+rb+i), src, dst, srcPort, dstPort)                                         //nolint:gosec
		bFwd := routing.ForwardRule(DefaultRouteKeepAlive, routing.RouteID(emuRidBFwd+rb+i), routing.RouteID(emuRidACon+rb+i), tpID, dst, src, dstPort, srcPort) //nolint:gosec
		bRvs := routing.ConsumeRule(DefaultRouteKeepAlive, routing.RouteID(emuRidBCon+rb+i), dst, src, dstPort, srcPort)                                         //nolint:gosec
		for _, r := range []struct {
			rt routing.Table
			ru routing.Rule
		}{{rtA, aFwd}, {rtA, aRvs}, {rtB, bFwd}, {rtB, bRvs}} {
			if err := r.rt.SaveRule(r.ru); err != nil {
				t.Fatalf("save rule: %v", err)
			}
		}
		fwdA, rvsA = append(fwdA, aFwd), append(rvsA, aRvs)
		fwdB, rvsB = append(fwdB, bFwd), append(rvsB, bRvs)

		rig.A.conns = append(rig.A.conns, connA)
		rig.B.conns = append(rig.B.conns, connB)
		rig.A.tps = append(rig.A.tps, mtA)
		rig.B.tps = append(rig.B.tps, mtB)
	}

	rig.wireEnd(rig.A, fwdA, rvsA, opts, true, dst, src)
	rig.wireEnd(rig.B, fwdB, rvsB, opts, false, dst, src)

	// Reader goroutines: every frame a leg delivers goes to that end's
	// packet intake, exactly as the transport manager's read loop does.
	for i := range opts.Legs {
		go rig.serveLeg(rig.A, i)
		go rig.serveLeg(rig.B, i)
	}

	t.Cleanup(rig.Close)
	return rig
}

// wireEnd installs one end's rules, mux and service loops.
func (r *emuRig) wireEnd(e *emuEnd, fwd, rvs []routing.Rule, opts emuOpts, initiator bool, dst, src cipher.PubKey) {
	rg := e.rg
	m := newRouteMux(rg.logger, true) // SACK: CapSACK is advertised on every mux
	m.holRetxEnabled = opts.HolRetx
	m.legStateEnabled = opts.LegState
	m.legRehomeEnabled = opts.LegRehome

	rg.mu.Lock()
	rg.mux = m
	rg.tps = e.tps
	rg.fwd = fwd
	rg.rvs = rvs
	rg.encrypt = false
	m.growLegs(len(e.tps))
	// Both ends' legs are established before the first byte, as they are
	// after a completed handshake on every leg.
	for i := range e.tps {
		m.markLegReady(i)
	}
	m.rebuildWeights(e.tps)
	rg.mu.Unlock()

	m.SetForwardRehomeFn(rg.noteForwardRehome)
	m.SetForwardFanoutFn(rg.noteForwardFanout)
	m.SetLegProbeRulingFn(func(idx, legs int, tp *transport.ManagedTransport, probeOnly bool, reason string) {
		rg.noteLegProbeRuling(idx, legs, tp, probeOnly, reason)
		side := "acceptor"
		if initiator {
			side = "initiator"
		}
		r.rulingMu.Lock()
		r.rulings = append(r.rulings, fmt.Sprintf("%s leg %d probe_only=%v (%s)", side, idx, probeOnly, reason))
		r.rulingMu.Unlock()
	})
	if opts.Directional {
		m.setDirectional(initiator, dst, src)
	}

	// The loops a mux group runs that matter to the data plane. Keep-alive
	// is left out (it only ages rules), and the liveness/prune loops are
	// opt-in because they rule in tens of seconds.
	rg.handshakeProcessedOnce.Do(func() { close(rg.handshakeProcessed) })
	go rg.servicePacketLoop("sack", rg.cfg.KeepAliveInterval/2, rg.sackServiceFn, nil)
	go rg.serviceKnobLoop("tlp", routersettings.TLPCheckInterval, rg.tlpServiceFn)
	go rg.serviceKnobLoop("send-window", routersettings.SendWindowRefreshInterval, rg.windowServiceFn)
	go rg.serviceKnobLoop("reorder-stall", routersettings.ReorderStallInterval, rg.reorderStallServiceFn)
	if opts.Liveness {
		go rg.serviceKnobLoop("leg-liveness", routersettings.LegLivenessInterval, rg.legLivenessServiceFn)
		go rg.serviceKnobLoop("leg-dataprogress", routersettings.LegDataProgressInterval, rg.legDataProgressServiceFn)
	}
}

// serveLeg is one end's read loop for one leg.
func (r *emuRig) serveLeg(e *emuEnd, i int) {
	conn := e.conns[i]
	buf := make([]byte, 1<<16+routing.PacketHeaderSize)
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		if err := conn.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
			return
		}
		n, err := conn.Read(buf)
		if err != nil {
			if ne, ok := err.(interface{ Timeout() bool }); ok && ne.Timeout() {
				continue
			}
			return
		}
		if n < routing.PacketHeaderSize {
			continue
		}
		pkt := make(routing.Packet, n)
		copy(pkt, buf[:n])
		// With a shared host, dispatch BY RULE — a re-homed chain's frames then
		// reach whichever group now owns its consume rule, not the rig that
		// happens to hold the conn.
		if e.host != nil {
			e.host.dispatch(pkt)
			continue
		}
		_ = e.rg.handlePacket(pkt) //nolint:errcheck // a dropped packet is the SACK layer's problem
	}
}

// Close stops the rig.
func (r *emuRig) Close() {
	r.closed.Do(func() {
		close(r.stop)
		for i := range r.A.conns {
			_ = r.A.conns[i].Close() //nolint:errcheck
			_ = r.B.conns[i].Close() //nolint:errcheck
		}
		// Mark both ends remote-closed first: Close() then takes its fast
		// path instead of broadcasting close packets into a dead wire and
		// waiting closeRoutineTimeout twice per rig (4 s of every run).
		r.A.rg.setRemoteClosed()
		r.B.rg.setRemoteClosed()
		_ = r.A.rg.Close() //nolint:errcheck
		_ = r.B.rg.Close() //nolint:errcheck
	})
}

// emuDir names a transfer direction.
type emuDir string

const (
	// emuDown is the acceptor (exit) sending to the initiator (client).
	emuDown emuDir = "down"
	// emuUp is the initiator (client) sending to the acceptor (exit).
	emuUp emuDir = "up"
)

// emuTransfer is one streaming transfer's result before it is folded into a
// Summary.
type emuTransfer struct {
	Bytes    int64
	Got      int64
	Elapsed  time.Duration
	TTFB     time.Duration
	HashOK   bool
	Err      error
	FirstAt  time.Time
	StartAt  time.Time
	progress atomic.Int64
}

// Transfer streams n bytes in dir and blocks until they are all received or
// the deadline passes. The payload is a seeded pseudo-random stream with a
// 4-byte offset stamped into every 64 bytes, so a mis-ordered or duplicated
// delivery fails the hash AND says where.
func (r *emuRig) Transfer(dir emuDir, n int64, timeout time.Duration) *emuTransfer {
	r.t.Helper()
	sender, receiver := r.B.rg, r.A.rg
	if dir == emuUp {
		sender, receiver = r.A.rg, r.B.rg
	}
	payload := emuPayload(n)
	want := sha256.Sum256(payload)

	res := &emuTransfer{Bytes: n, StartAt: time.Now()}
	r.inflight.Store(res)
	r.sendingUp.Store(dir == emuUp)
	_ = sender.SetWriteDeadline(time.Now().Add(timeout))  //nolint:errcheck
	_ = receiver.SetReadDeadline(time.Now().Add(timeout)) //nolint:errcheck

	done := make(chan struct{})
	got := make([]byte, 0, n)
	go func() {
		defer close(done)
		buf := make([]byte, 64*1024)
		for int64(len(got)) < n {
			rn, err := receiver.Read(buf)
			if rn > 0 {
				if res.FirstAt.IsZero() {
					res.FirstAt = time.Now()
				}
				got = append(got, buf[:rn]...)
				res.progress.Store(int64(len(got)))
			}
			if err != nil {
				if res.Err == nil {
					res.Err = err
				}
				return
			}
		}
	}()

	// Write in application-sized chunks so the send path segments them the
	// way a proxy stream does.
	const chunk = 256 * 1024
	go func() {
		for off := int64(0); off < n; off += chunk {
			end := off + chunk
			if end > n {
				end = n
			}
			if _, err := sender.Write(payload[off:end]); err != nil {
				if res.Err == nil {
					res.Err = err
				}
				return
			}
		}
	}()

	select {
	case <-done:
	case <-time.After(timeout):
		if res.Err == nil {
			res.Err = fmt.Errorf("transfer timed out after %v with %d/%d bytes", timeout, res.progress.Load(), n)
		}
	}
	res.Elapsed = time.Since(res.StartAt)
	res.Got = int64(len(got))
	if !res.FirstAt.IsZero() {
		res.TTFB = res.FirstAt.Sub(res.StartAt)
	}
	res.HashOK = res.Got == n && sha256.Sum256(got) == want
	if res.HashOK {
		res.HashOK = bytes.Equal(got, payload)
	}
	return res
}

// ProgressBytes is how many application bytes the receiver has taken so far.
func (x *emuTransfer) ProgressBytes() int64 { return x.progress.Load() }

// emuPayload builds a deterministic n-byte stream with position markers.
func emuPayload(n int64) []byte {
	b := make([]byte, n)
	rnd := rand.New(rand.NewSource(0xC0FFEE)) //nolint:gosec // G404: test fixture, not cryptography
	_, _ = rnd.Read(b)                        //nolint:errcheck
	for off := int64(0); off+4 <= n; off += 64 {
		binary.BigEndian.PutUint32(b[off:off+4], uint32(off)) //nolint:gosec
	}
	return b
}

// Summary folds the rig's counters and a transfer's result into the report
// row every scenario prints. sender is the side that sent the bytes.
func (r *emuRig) Summary(name string, dir emuDir, x *emuTransfer) emu.Summary {
	sender, recv := r.B, r.A
	if dir == emuUp {
		sender, recv = r.A, r.B
	}
	s := emu.Summary{
		Name: name, Dir: string(dir), Bytes: x.Bytes, Got: x.Got,
		Elapsed: x.Elapsed, HashOK: x.HashOK, TTFB: x.TTFB,
	}
	for i := range r.legs {
		s.WireBytes += r.Leg(i).WireBytes()
	}

	sm := sender.rg.mux
	rm := recv.rg.mux
	sender.rg.mu.Lock()
	sendLegs := sm.snapshotLegs()
	sender.rg.mu.Unlock()
	recv.rg.mu.Lock()
	recvLegs := rm.snapshotLegs()
	recv.rg.mu.Unlock()

	for i := range r.legs {
		nm := r.legs[i].Name
		if nm == "" {
			nm = fmt.Sprintf("leg%d", i)
		}
		l := emu.LegShare{Index: i, Name: nm, RttMs: r.legs[i].LatencyMs}
		if i < len(sendLegs) {
			l.SentBytes = sendLegs[i].SentBytes
			l.Retransmits = sendLegs[i].Retransmits
			s.Retransmits += sendLegs[i].Retransmits
		}
		if i < len(recvLegs) {
			l.RecvBytes = recvLegs[i].RecvBytes
			l.PayloadBytes = recvLegs[i].PayloadBytes
		}
		l.ProbeOnly = sm.legProbeExhausted(i)
		l.Standby = sm.isLegStandby(i)
		s.Legs = append(s.Legs, l)
	}
	s.ReorderDrops = rm.reorderDrops.Load()
	s.SendWindowWaits = sm.sendWindowWaits.Load()
	s.SacksSent = rm.sacksSent.Load()
	s.SacksRecv = sm.sacksRecv.Load()
	if x.Err != nil {
		s.Notes = append(s.Notes, "error: "+x.Err.Error())
	}

	r.rulingMu.Lock()
	s.Events = append(s.Events, r.rulings...)
	r.rulingMu.Unlock()
	for _, e := range sender.rg.muxEvents.snapshot() {
		s.Events = append(s.Events, fmt.Sprintf("sender %s leg=%d %s", e.Event, e.LegIndex, e.Reason))
	}
	return s
}

// AddedLegs is how many legs each end holds now — a rebuild during a
// scenario would move it.
func (r *emuRig) AddedLegs() (a, b int) {
	r.A.rg.mu.Lock()
	a = len(r.A.rg.tps)
	r.A.rg.mu.Unlock()
	r.B.rg.mu.Lock()
	b = len(r.B.rg.tps)
	r.B.rg.mu.Unlock()
	return a, b
}

// Progress is how many application bytes the transfer currently running has
// delivered — the hook a scenario uses to act mid-transfer.
func (r *emuRig) Progress() int64 {
	if x := r.inflight.Load(); x != nil {
		return x.ProgressBytes()
	}
	return 0
}

// downProgress is Progress, named for the scenarios that only ever run a
// download.
func (r *emuRig) downProgress() int64 { return r.Progress() }

// busiestSendLeg is the index of the leg carrying the most bytes on the side
// that is currently sending, or -1 before anything has moved.
func (r *emuRig) busiestSendLeg() int {
	end := r.B
	if r.sendingUp.Load() {
		end = r.A
	}
	end.rg.mu.Lock()
	legs := end.rg.mux.snapshotLegs()
	end.rg.mu.Unlock()
	best, bestBytes := -1, uint64(0)
	for i := range legs {
		if legs[i].SentBytes > bestBytes {
			best, bestBytes = i, legs[i].SentBytes
		}
	}
	return best
}

// legBases renders one end's per-leg ECF readings — the delay basis the
// outclassed-leg gate judges on and the goodput the peer's SACKs proved — so a
// scenario can say WHY a ruling did or did not fire.
func (r *emuRig) legBases(e *emuEnd) string {
	m := e.rg.mux
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	var b strings.Builder
	b.WriteString("bases[")
	for i, lc := range m.legs {
		if lc == nil {
			continue
		}
		if i > 0 {
			b.WriteString(" ")
		}
		fmt.Fprintf(&b, "%d: delay=%.0fms deliv=%.0fB/s", i, lc.ecfRttMs, lc.ecfDelivBps)
	}
	b.WriteString("]")
	return b.String()
}

// legSentBytes is one end's per-leg SENT byte counters — the counters a
// scenario diffs across a transfer to say which leg carried it.
func (r *emuRig) legSentBytes(e *emuEnd) []uint64 {
	e.rg.mu.Lock()
	legs := e.rg.mux.snapshotLegs()
	e.rg.mu.Unlock()
	out := make([]uint64, len(legs))
	for i := range legs {
		out[i] = legs[i].SentBytes
	}
	return out
}

// splitGroupPort implements legSplitHost. The rig's ports are small and fixed,
// so a split-out group takes the lowest free one rather than the router's
// random ephemeral — the port only has to be free, and this keeps a failure
// readable.
func (h *emuHost) splitGroupPort(base routing.RouteDescriptor) (routing.Port, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for p := routing.Port(100); p < 200; p++ {
		if _, ok := h.groups[routing.NewRouteDescriptor(base.SrcPK(), base.DstPK(), p, base.DstPort())]; !ok {
			return p, nil
		}
	}
	return 0, fmt.Errorf("no free port for a split-out group of %s", base.String())
}

// registerSplitGroup implements legSplitHost.
func (h *emuHost) registerSplitGroup(rg *RouteGroup) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if _, ok := h.groups[rg.desc]; ok {
		return fmt.Errorf("a group already holds %s", rg.desc.String())
	}
	h.groups[rg.desc] = rg
	return nil
}

// singleLegGroupOn finds the group this end registered that holds exactly the
// transport tpID — how a test names a group that did not exist when the rig was
// built.
func (h *emuHost) singleLegGroupOn(tpID uuid.UUID) *RouteGroup {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, rg := range h.groups {
		if rg == nil || rg.isClosed() {
			continue
		}
		rg.mu.Lock()
		hit := len(rg.tps) == 1 && rg.tps[0] != nil && rg.tps[0].Entry.ID == tpID
		rg.mu.Unlock()
		if hit {
			return rg
		}
	}
	return nil
}
