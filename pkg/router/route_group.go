// Package router pkg/router/route_group.go c2-net-routing
package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/ioutil"
	"github.com/skycoin/skywire/pkg/dmsg/noise"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/deadline"
)

const (
	defaultRouteGroupKeepAliveInterval = DefaultRouteKeepAlive / 2
	defaultReadChBufSize               = 1024
	closeRoutineTimeout                = 2 * time.Second
	// intakeChBufSize is the depth of a route group's own inbound intake
	// queue — the buffer between the router's single transport read loop and
	// this group's handler (see serveIntake). It is a constant rather than a
	// `route settings` knob for the reason settings.go states: the channel is
	// made once when the group is built, so changing it live would mean
	// reaching into every running group, which is a locking change and not a
	// knob.
	intakeChBufSize = 1024
	// intakeWarnInterval rate-limits the "intake queue full" warning to one
	// per route group per interval. A stalled app overflows the queue at line
	// rate, and a per-packet warning is itself a way to wedge a visor.
	intakeWarnInterval = 10 * time.Second
	// maxConsecutiveWriteFailures is the number of consecutive transport write failures
	// before the RouteGroup closes itself to stop spamming logs.
	maxConsecutiveWriteFailures = 5

	// legLivenessInterval is how often each multiplexed leg is probed
	// end-to-end. pruneDeadTransports only sees a dead LOCAL (first-hop)
	// transport; a transport dying BEYOND the first hop leaves the local tp
	// open, so the leg silently black-holes. This probe sends a Ping down each
	// leg that the destination echoes (Pong) — purely source-side, no wire or
	// remote-version change (every visor already echoes pings).
	legLivenessIntervalDefault = 30 * time.Second
	// legPongMissThreshold is the number of consecutive liveness probes with no
	// echo after which a leg is treated as black-holing and dropped (mirrors
	// transport-level pongMissThreshold). The leg is removed from the mux but
	// its (possibly shared) transport is NOT closed, so a false positive simply
	// triggers a self-heal re-dial — never an outage, and never the last leg.
	legPongMissThresholdDefault = 3
	// legDataProgressInterval is how often the fast data-progress prune samples
	// each leg's rg-scoped RecvBytes. Much tighter than legLivenessInterval so a
	// leg that black-holes bulk DATA (while still echoing the tiny liveness ping)
	// is caught in seconds, not the ~90s the pong-miss path takes — the
	// difference between a mux that limps at the fragile leg's retransmit tax and
	// one that sheds the leg and runs at the reliable legs' rate.
	legDataProgressIntervalDefault = 5 * time.Second
	// legDataStallGapAge is how long a reorder frontier gap must stay open before
	// the data-progress prune's STALLED path acts. Long enough that ordinary
	// latency-skew interleave (which closes in well under a second) never trips
	// it; short enough to react quickly once a leg genuinely stops delivering its
	// share.
	legDataStallGapAgeDefault = 3 * time.Second
	// legBlackHoleMinTopBytes gates the frontier-HEALTHY black-hole prune: even
	// when the reorder frontier is not stuck, a leg delivering essentially nothing
	// (< 1/64 of the leader) while a clearly-moving leader carries the group is a
	// goodput black-hole — normal latency, ~zero delivery — that the latency-band
	// demotion never catches. Under an even/round-robin spread it still draws its
	// share of frames, stalling the frontier and burning retransmits, so it is
	// shed without waiting for a full stall. The leader must have moved at least
	// this much over one legDataProgressInterval so top/64 is a meaningful floor
	// and a merely-slow or idle group is never judged (~128KB over 5s ≈ 25KB/s).
	legBlackHoleMinTopBytesDefault = 128 * 1024
	// soleBlackHole* gate the sole-leg black-hole reaping (see soleLegBlackHoled).
	// A group down to one active leg whose route has SENT more than
	// soleBlackHoleSentFloor (a real request went out) but DELIVERED no payload at
	// all, held for soleBlackHoleTicks consecutive data-progress intervals, is a
	// dead route the two ordinary prunes cannot see — dial a replacement. The
	// judgment is on the leg's PayloadBytes (unique in-order payload), not its raw
	// RecvBytes: the raw count includes the handshake, liveness pongs and SACKs,
	// which a black-holed route still receives, so it used to be held against a
	// 16 KiB floor — and a working route carrying a light session (a proxy client
	// whose responses are a few hundred bytes) never cleared that floor and was
	// replaced every 15 s, tearing down the session it was serving. The sent
	// floor rejects an idle group that never requested anything; the tick count
	// (≥15s at a 5s cadence) rejects a merely-slow origin. Both counters are
	// CUMULATIVE, so a route that ever delivered payload is never flagged — this
	// targets dead-from-establishment routes.
	soleBlackHoleSentFloorDefault = 256
	soleBlackHoleTicksDefault     = 3
	// reorderStallInterval is how often the receive side checks for a reorder
	// frontier gap stuck past reorderTimeout and, if so, emits a SACK to prompt
	// the sender to retransmit the missing seq IN ORDER (see
	// RouteGroup.reorderStallServiceFn). Shorter than reorderTimeout (1.5s) so a
	// stalled gap is nudged within ~one reorderTimeout of going silent.
	reorderStallIntervalDefault = 500 * time.Millisecond

	// legStateResyncInterval is how often the active-set side re-asserts its
	// COMPLETE leg standby/active set to the peer (CapLegState). The park/promote
	// signals are otherwise purely EVENT-driven, so a single dropped LegStatePacket
	// (loss, a multihop re-stamp miss, or a park that raced the leg's handshake)
	// desyncs the two ends permanently — the accept side (all-active) then keeps
	// striping its send traffic across legs this side has parked (the wide-mux
	// download over-subscription; measured the peer holding 33 active legs while
	// this side had ~8). A periodic full resync lets any lost event self-correct.
	legStateResyncIntervalDefault = 7 * time.Second

	// bandDemoteRatio: an ACTIVE leg whose end-to-end latency is more than this
	// factor off the active-set median (either tail) is demoted to warm standby
	// so the mux never STRIPES across latency-disparate legs. Striping a 2ms LAN
	// short-circuit beside a 300ms route opens a reorder gap the size of the
	// difference on every interleave, HoL-capping (or, if the fast leg
	// black-holes, stalling) the no-skip reorder buffer — the multi-leg
	// collapse-to-~0 measured against a healthy single-leg reference. 3.0 matches
	// the adaptive engine's own high-side outlier multiplier so the two never
	// fight over the same leg.
	bandDemoteRatioDefault = 3.0
	// bandAdmitRatio: a warm-standby leg is re-admitted to the active set only
	// once it is within this tighter factor of the median. The gap between admit
	// and demote is hysteresis — it stops a leg hovering at the band edge from
	// flip-flopping active/standby every interval.
	bandAdmitRatioDefault = 2.5
	// bandDemoteRatioTight / bandAdmitRatioTight are the SAME hysteresis pair but
	// tighter, used when the mux is in capacity (aggregation) mode. Aggregating a
	// single stream across M active legs requires the arrivals to be near-in-order
	// so the no-skip reorder frontier does not stall; a 3x active-band spread
	// stalls it, the data-progress prune then sheds the laggards, and the active
	// set collapses toward one leg (the observed capacity-mode collapse from 4
	// active to 1). A ~2x active band keeps the stripe set homogeneous enough that
	// the frontier holds and the prune never fires, so the multi-active set is
	// HELD. Failover mode (1 active + standbys) is unaffected — it never stripes.
	bandDemoteRatioTightDefault = 2.0
	bandAdmitRatioTightDefault  = 1.6
	// goodputGateFrac: an active leg delivering at least this fraction of the
	// best active leg's recent goodput is spared from latency demotion (its high
	// measured latency is self-inflicted queuing, not a bad route — BBR/bufferbloat
	// principle). 0.15 matches the capacity scheduler's cold-leg floor share.
	goodputGateFracDefault = 0.15
	// bandMinLegs: latency-band admission only runs with at least this many legs
	// carrying a measured latency. Below it the median is not a meaningful cluster
	// anchor, and the 1-2 leg pathologies are already handled by the sole-leg
	// black-hole heal and the data-progress prune.
	bandMinLegsDefault = 3
)

var (
	// ErrNoTransports is returned when RouteGroup has no transports.
	ErrNoTransports = errors.New("no transports")
	// ErrNoRules is returned when RouteGroup has no rules.
	ErrNoRules = errors.New("no rules")
	// ErrBadTransport is returned when transport is nil.
	ErrBadTransport = errors.New("bad transport")
	// ErrRuleTransportMismatch is returned when number of forward rules does not equal to number of transports.
	ErrRuleTransportMismatch = errors.New("rule/transport mismatch")
	// ErrNoSuitableTransport is returned when no suitable transport was found.
	ErrNoSuitableTransport = errors.New("no suitable transport")
	// ErrNoRouteFound is return when no route founds after specific tries
	ErrNoRouteFound = errors.New("no route founds")
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type sendServicePacketFn func(interval time.Duration)

// RouteGroupConfig configures RouteGroup.
type RouteGroupConfig struct {
	ReadChBufSize     int
	KeepAliveInterval time.Duration
	// FEC advertises CapFEC in the mux handshake so repair frames are striped
	// alongside data. Off by default (router Config.MuxFEC).
	FEC bool
}

// DefaultRouteGroupConfig returns default RouteGroup config.
// Used by default if config is nil.
func DefaultRouteGroupConfig() *RouteGroupConfig {
	return &RouteGroupConfig{
		KeepAliveInterval: defaultRouteGroupKeepAliveInterval,
		ReadChBufSize:     defaultReadChBufSize,
	}
}

// RouteGroup should implement 'io.ReadWriteCloser'.
// It implements 'net.Conn'.
type RouteGroup struct {
	// atomic requires 64-bit alignment for struct field access
	lastSent atomic.Int64

	// lastRecv is the arrival time of the most recent inbound packet. It exists
	// for the service-loop gate (service_gate.go): a group being downloaded to
	// sends almost nothing, so without a receive term it would read as idle and
	// the gated loops would park and unpark once per arriving packet.
	lastRecv atomic.Int64

	// consecutiveWriteFailures tracks repeated transport write errors.
	// After maxConsecutiveWriteFailures, the RouteGroup closes itself.
	consecutiveWriteFailures int32

	// wake releases the service loops parked by their gate (service_gate.go).
	wake serviceWake

	mu sync.Mutex

	cfg    *RouteGroupConfig
	logger *logging.Logger
	desc   routing.RouteDescriptor // describes the route group
	rt     routing.Table
	// appName is the originating app's name for the dialing side
	// (skysocks-client, vpn-client, etc.). Empty for accept-side
	// rg's where the app context isn't available locally. Set via
	// SetAppName from saveRouteGroupRules. Used for app-scoped mux
	// info lookups since the descriptor's SrcPort is ephemeral and
	// doesn't resolve through procManager.AppByPort.
	appName string
	// knobHolder carries this group's resolved router knobs — the visor-wide
	// catalog with the owning app's `route settings --app <name>` overrides
	// folded in. Resolved when the group is built, again when appName arrives,
	// and thereafter only when the catalog version moves (settings_group.go).
	knobHolder *routersettings.Holder
	// tunnelRole is the DIALING app's own label for this route group:
	// "active" (it carries streams) or "standby" (held open, measured, ready
	// to take over). Empty for every route group that is not one of a
	// multi-tunnel app's tunnels, and empty on the accept side — the exit has
	// no idea which of a peer's tunnels are in standby. Set via
	// SetTunnelRole from finishDial, read back in MuxStats.
	tunnelRole string
	// legReserve marks a group a leg split built (leg_split.go): a chain handed
	// back to the pool with no app session and no end-to-end handshake of its
	// own. It may be taken again as a LEG, never promoted to a tunnel: only
	// the app that dialed a group can put streams on it. Set before the group
	// is registered and never changed.
	legReserve bool

	handshakeProcessed     chan struct{}
	handshakeProcessedOnce sync.Once
	encrypt                bool

	// Per-frame noise (inverse-mux). perFrameNoiseWant advertises CapPerFrameNoise
	// in our handshake (gated by SKYWIRE_PERFRAME_NOISE so it stays opt-in during
	// rollout). nsConf carries the KK keys (local SK/PK + peer PK), copied from the
	// same noise.Config that EncryptConn would use. ns is the per-frame session;
	// perFrameNoiseMsg caches our outgoing KK message so a handshake RETRANSMIT
	// resends identical bytes instead of re-advancing the noise state machine.
	// perFrameNoiseActive flips true once ns's transport cipher is ready and the
	// mux seal/open are wired; from then on EncryptConn is bypassed. Guarded by rg.mu
	// (set inside handshake processing / sendHandshake, both under mu).
	perFrameNoiseWant   bool
	nsConf              noise.Config
	ns                  *noise.Noise
	perFrameNoiseMsg    []byte
	perFrameNoiseActive bool
	perFrameNoiseOnce   sync.Once

	// forwardHops stores the complete route path as originally calculated.
	// This is the full multi-hop route, not just local transports.
	forwardHops []routing.Hop

	// legForwardHops stores EACH mux leg's full forward route, keyed by the
	// leg's first-hop transport ID (survives index shifts, like
	// legE2ELatency). forwardHops above records only the primary leg; this
	// captures every leg so the per-leg mux view can show each leg's whole
	// path (all hops, full PKs, per-hop transport type), not just its first
	// transport. Populated by SetForwardHops (primary) + AddMuxRouteByHops
	// (aux legs). Guarded by rg.mu.
	legForwardHops map[uuid.UUID][]routing.Hop

	// legRemoteTp maps a leg's LOCAL first-hop transport ID (the same key
	// legForwardHops uses) to the FAR END's first-hop transport ID for that
	// leg — i.e. Reverse[0].TpID of the plan this leg was dialed with.
	//
	// That value is an exact mirror of one entry in the peer's own rg.tps: the
	// setup node installs `respEdge.Forward` on the destination, which is the
	// ForwardRule generated for the reverse route's first hop, and
	// appendRouteToGroup registers r.tm.Transport(rules.Forward.NextTransportID())
	// as that leg's transport there. Keeping the mapping locally lets the
	// aux-leg planners predict — before paying a setup-node round trip — that
	// the destination would refuse a plan whose reverse leaves it over a
	// transport it already has a leg on. Initiator-local; no wire change.
	//
	// Read only through remoteLegTransportIDs, which walks the LIVE tps, so an
	// entry for a leg that has since been pruned stops excluding anything
	// (it is inert, not sticky). Guarded by rg.mu.
	legRemoteTp map[uuid.UUID]uuid.UUID

	// initiator is true when this visor dialed the remote end (called
	// router.DialRoutes); false when this visor accepted the route via
	// AcceptRoutes / saveRouteGroupRules from a setup-node request.
	// Set once at construction in saveRouteGroupRules from nsConf.Initiator
	// and never mutated afterwards.
	initiator bool

	// localPK is this visor's own public key, copied from the noise config in
	// saveRouteGroupRules. The descriptor alone cannot say which end is local:
	// the setup node hands EACH edge the descriptor that points at that edge
	// (setupnode.go initEdge/respEdge), so Dst is this visor on both sides.
	// farEndPK needs the local key to name the peer without guessing from
	// initiator. Null on a group built without a noise config (the emulated
	// testbed), where farEndPK falls back to the descriptor's Src.
	localPK cipher.PubKey

	// 'tps' is transports used for writing/forward rules.
	// It should have the same number of elements as 'fwd'
	// where each element corresponds with the adjacent element in 'fwd'.
	tps []*transport.ManagedTransport

	// The following fields are used for writing:
	// - fwd/tps should have the same number of elements.
	// - the corresponding element of tps should have tpID of the corresponding rule in fwd.
	// - fwd references 'ForwardRule' rules for writes.
	fwd []routing.Rule // forward rules (for writing)
	rvs []routing.Rule // reverse rules (for reading)

	// 'readCh' reads in incoming packets of this route group.
	// - Router should serve call '(*transport.Manager).ReadPacket' in a loop,
	//      and push to the appropriate '(RouteGroup).readCh'.
	readCh  chan []byte  // push reads from Router
	readBuf bytes.Buffer // for read overflow

	// inCh is this group's INBOUND INTAKE QUEUE. The router has one goroutine
	// reading every transport (serveTransportManager), so anything that blocks
	// in a route group's handler blocks EVERY group on the visor: a group whose
	// app stopped reading used to park the shared loop for up to 30 s per packet
	// on readCh, and all route groups — on every first hop — stopped receiving
	// at once for a minute at a time. The shared loop now only enqueues here
	// (never blocking) and one worker per group (serveIntake) runs the
	// synchronous handler, so a stuck group stalls only itself.
	//
	// One queue and one worker, so per-group packet order is preserved. Close
	// packets deliberately bypass it (see handlePacket).
	inCh chan routing.Packet
	// intakeDrops counts packets dropped because inCh was full, and
	// intakeWarnAt is the last time that was warned about (unix nanos), for the
	// rate limit. Both are surfaced in `visor state --select diag`.
	intakeDrops  atomic.Uint64
	intakeWarnAt atomic.Int64

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline

	networkStats *networkStats

	// createdAt is when this group was constructed, and closeObserver (if the
	// dialer installed one) is called once from close() with the group's age
	// and the payload it carried. It is how the router learns that a route it
	// just dialed died young without ever moving a byte — see
	// dead_route_cache.go. Both are written once at setup and read at close.
	createdAt       time.Time
	closeObserverMu sync.Mutex
	closeObserver   func(age time.Duration, carried uint64)

	// used as a bool to indicate if this particular route group initiated close loop
	closeInitiated   int32
	remoteClosedOnce sync.Once
	remoteClosed     chan struct{}
	closed           chan struct{}
	// rotateNow signals the rotation service loop to run its on_tick controller
	// IMMEDIATELY, out of band from the periodic interval — fired on a leg death
	// so a warm standby is promoted the instant an active leg is lost, instead of
	// waiting up to a full rotation interval. Buffered (1) and sent non-blocking,
	// so a death during an in-progress tick just coalesces into one extra run.
	rotateNow chan struct{}
	// used to wait for all the `Close` packets to run through the loop and come back.
	// Atomic counter + channel instead of sync.WaitGroup to avoid
	// "WaitGroup reused before previous Wait returned" panics.
	closeDonePending int32 // atomic counter of outstanding close acks
	// closeDoneCh is closed when closeDonePending reaches 0. It is reached
	// from three goroutines — both Close() callers and the packet loop
	// handling the peer's close replies — so it is created and closed through
	// closeDone()/signalCloseDone() and never touched directly: it used to be
	// assigned unlocked in close() and read unlocked in
	// waitForCloseRouteGroup, which the race detector caught on CI's windows
	// lane (and which two concurrent Close() calls could also turn into a
	// close of a closed channel).
	closeDoneMu   sync.Mutex
	closeDoneOnce sync.Once
	closeDoneCh   chan struct{}
	once          sync.Once // guards the readCh/remoteClosed cleanup
	closedOnce    sync.Once // guards close(rg.closed) — closable from two paths

	errorMu    sync.RWMutex
	closeError error

	// For synchronous latency measurement
	pendingPongCh chan float64 // Receives measured latency when pong arrives
	pendingPongMu sync.Mutex

	// Route multiplexing layer (nil when mux not negotiated).
	// Encapsulates sequencing, reordering, SACK, and transport selection.
	mux *routeMux

	// rehomeHost is the router this group is registered with, as the narrow
	// interface leg re-home needs: a re-home arriving on THIS group's chain
	// names a DIFFERENT group by descriptor, and only the router can resolve
	// one. Nil for a group built outside the router (tests, datagram setup),
	// which simply refuses every re-home. See leg_rehome.go.
	rehomeHost legRehomeHost

	// legChangeHook, when non-nil, fires from the leg-mutation
	// paths (appendForwardLeg / appendRules / leg-prune on
	// transport close). Returning a non-Unset DistributionConfig
	// causes the route group to re-apply distribution against the
	// new leg set. See pkg/router/dial_hook.go LegChangeHook for
	// the contract.
	legChangeHook LegChangeHook
	// legChangeInfo is the DialInfo the hook receives. Set once
	// at route group construction time when the dialing visor
	// configures the hook; unset on accept-side rg's (the
	// hook is only meaningful on the dialing side).
	legChangeInfo DialInfo

	// rotationHook, when non-nil, fires periodically at
	// rotationInterval from the rotation servicePacketLoop —
	// returning a RotationAction the route group applies (drop
	// listed legs, optionally request an aux leg via
	// rotationApplyAdd). Zero interval = no rotation goroutine.
	// See pkg/router/dial_hook.go RotationHook.
	rotationHook     RotationHook
	rotationApplyAdd func(excludeHops []string)
	// rotationApplyAddForward dials one FORWARD-ONLY aux leg
	// (appendRouteAsymmetric addFwd=true/addRev=false) for a
	// RotationAction.AddForwardLeg — extra upstream send capacity that leaves
	// the reverse/download set untouched. Nil disables forward widening.
	rotationApplyAddForward func(excludeHops []string)
	rotationInterval        time.Duration

	// standbyNewLegs mirrors onto the mux (SetStandbyNewLegs) so newly-grown
	// aux legs enter warm standby on add and the rotation engine promotes them
	// one per tick, goodput-gated, instead of every dialed leg going hot at
	// once and churning the group. Set true when SetRotation wires a promoting
	// engine. See routeMux.standbyNewLegs.
	standbyNewLegs bool

	// selfHealAdd restores the multiplexed degree in the background when a
	// leg dies. pruneDeadTransports drops the dead leg (surviving legs
	// immediately carry its share via the selector), then maybeSelfHeal
	// dials replacement aux legs up to selfHealTarget — without blocking
	// traffic. Wired for EVERY mux dial, not just policy-rotation ones, so
	// a plain `--routes N` group self-heals too. healInFlight guards against
	// piling up concurrent heals while a replacement is still dialing.
	selfHealAdd    func(excludeHops []string)
	selfHealTarget int
	healInFlight   atomic.Bool

	// The standby-pool arbiter's per-group state (pool_arbiter.go): the legs
	// this group took from the pool, when the last one was taken, and the load
	// episode marks the release is measured from. All guarded by rg.mu.
	poolTaken     []poolTakenLeg
	poolLastTake  time.Time
	poolLoadAt    time.Time
	poolBusySince time.Time
	poolBytesMark uint64
	// poolBytesAt is when poolBytesMark was sampled, so the episode is judged
	// on a RATE rather than on "any delta at all" (see poolLoadSignal).
	poolBytesAt time.Time

	// dirFanoutTicks throttles the directional confinement/fan-out summary logged
	// by legDataProgressServiceFn (see there) so a busy download does not spam it.
	dirFanoutTicks int

	// Per-leg end-to-end liveness (issue #2). Keyed by leg transport ID so the
	// state survives index shifts from prune/append. legPongSeen records
	// whether the echo for a leg's probe came back since the last tick;
	// legMissed counts consecutive missed ticks; inflightPings maps a probe's
	// send-timestamp (the verbatim-echoed correlator) back to the leg it
	// tested. Guarded by legLivenessMu (NEVER held while taking rg.mu).
	legLivenessMu sync.Mutex
	legPongSeen   map[uuid.UUID]bool

	// reorderWedge* track the reorder-stall service's WARN throttling: ticks since
	// the current wedge started (0 = not wedged) and the gap age at the last log,
	// so a sustained wedge logs ~once per reorderTimeout instead of every tick.
	// Written only from the single reorder-stall service goroutine, but read from
	// the telemetry path (MuxStats), so the counters the `recovery` view exposes
	// are atomic: reorderWedgeTicks is the CURRENT wedge's tick count,
	// reorderWedgeStartNano when it began, reorderWedgeSeq the frontier seq it is
	// stuck on, reorderWedges how many wedges have cleared, and
	// reorderWedgeLongestMs the longest one seen on this group.
	soleBHTicks           int
	reorderWedgeTicks     atomic.Int64
	reorderWedgeStartNano atomic.Int64
	reorderWedgeSeq       uint32
	reorderWedges         atomic.Uint64
	reorderWedgeLongestMs atomic.Int64
	reorderWedgeLoggedAt  time.Duration

	// delayedAckArmed guards the one-shot delayed-ack timer (see
	// scheduleDelayedAck): 1 while a timer is outstanding. Atomic.
	delayedAckArmed int32
	legMissed       map[uuid.UUID]int
	inflightPings   map[int64]uuid.UUID
	// legE2ELatency is the EWMA-smoothed END-TO-END round-trip latency per leg,
	// keyed by transport ID (survives index shifts), folded from the
	// leg-liveness pong. This is the leg's TRUE route latency (all hops), unlike
	// tp.GetLatency() which is only the first-hop transport RTT — so a policy
	// evicting the "slowest leg" and the fastest-leg retransmit picker judge the
	// whole path, not just the near edge. Guarded by legLivenessMu.
	legE2ELatency map[uuid.UUID]float64
	// legOWD holds each leg's moving window of RAW end-to-end round-trip samples
	// (ms), keyed by transport ID (survives index shifts), folded from the same
	// leg-liveness pong as legE2ELatency but BEFORE the EWMA — the OWD-variation
	// series RFC 8382 shared-bottleneck detection needs (see bottleneck.go). No new
	// probe traffic: it reuses the pong the liveness loop already sends. Guarded by
	// legLivenessMu.
	legOWD map[uuid.UUID]*sbdWindow
	// legRTTWin holds each leg's TIME-bounded window of raw end-to-end round-trip
	// samples (ms), keyed by transport ID, folded from the same leg-liveness pong
	// as legE2ELatency. Its minimum is the load-robust latency statistic the
	// latency band judges on — the latest sample (and the EWMA over it) is
	// queue-inflated under load and made the band flap. Guarded by legLivenessMu.
	legRTTWin map[uuid.UUID]*legRTTWindow
	// sbdTrialMu guards sbdTrials, sbdIndependent and sbdRuledAt. Leaf lock: NEVER
	// held while taking rg.mu, legLivenessMu or adaptiveParkMu.
	sbdTrialMu sync.Mutex
	// sbdTrials holds, per PARKED leg transport ID, the standing shared-bottleneck
	// park trial: a park is provisional until the group's aggregate goodput is
	// re-read with the leg out (see sbd_trial.go).
	sbdTrials map[uuid.UUID]sbdTrial
	// sbdAckedPrev holds, per leg transport ID, the cumulative SACK-acknowledged
	// byte count read at the previous data-progress tick — the baseline the
	// SEND-path delivered rate is differenced from (sbdSendDeltas). Guarded by
	// sbdTrialMu.
	sbdAckedPrev map[uuid.UUID]uint64
	// sbdIndependent holds, per leg PAIR, the verified-independent window a failed
	// park trial opened: while it runs, no SBD ruling may merge those two legs.
	sbdIndependent map[sbdPairKey]sbdSuppression
	// sbdRuledAt holds, per leg PAIR, when an OBSERVATIONAL shared-bottleneck
	// ruling was last recorded for it (sbd_demote off). The detector rules on
	// every data-progress tick, so without this the record is one event every 5 s
	// for the whole transfer. Guarded by sbdTrialMu.
	sbdRuledAt map[sbdPairKey]time.Time
	// adaptiveParkMu guards adaptiveParks. Leaf lock: NEVER held while taking
	// rg.mu or legLivenessMu.
	adaptiveParkMu sync.Mutex
	// adaptiveParks holds, per leg transport ID (survives index shifts), the
	// standing ADAPTIVE park: when a controller parked that leg and the reason it
	// named. It is the hysteresis state that stops the shared-bottleneck and
	// latency-band controllers — which share one data-progress tick — from
	// trading the same leg back and forth every 5s. See park_hysteresis.go.
	adaptiveParks map[uuid.UUID]adaptivePark
	// peerParkMu guards peerParkedLegs. Leaf lock: NEVER held while taking rg.mu.
	peerParkMu sync.Mutex
	// peerParkedLegs holds the leg indices currently standby because the PEER said
	// so (a mirrored CapLegState park), not because a controller here decided it.
	// The leg-state resync re-broadcasts this side's OWN active/standby set to
	// repair a dropped signal; re-asserting a park the peer originated instead
	// pins the leg down from both ends, which is how a peer-mirrored park survived
	// five downloads. Written by handleLegStatePacket, read by the resync.
	peerParkedLegs map[int]struct{}
	// legRecvSnap is each leg's last-sampled rg-scoped RecvBytes, keyed by
	// transport ID (survives index shifts), for the fast data-progress prune:
	// an ACTIVE leg whose recv is flat across an interval while the group keeps
	// receiving AND a reorder gap stays open is black-holing DATA (a leg that
	// still echoes the tiny liveness ping, so pong-miss liveness never catches
	// it). Guarded by legLivenessMu.
	legRecvSnap map[uuid.UUID]uint64
	// closeReason is why the next close of this group happens, when the caller
	// knows more than the close code does (see setCloseReason). Its own mutex:
	// the close paths reach it both with and without rg.mu held.
	closeReasonMu sync.Mutex
	closeReason   string
	// muxEvents is the router's shared bounded history of leg/group changes
	// (see mux_events.go). Set by the router that owns this group; nil for a
	// route group built outside one, in which case nothing is recorded.
	muxEvents *muxEventRing
	// ownEvents is this group's private event ring, sized by
	// mux.event_ring_per_group. MuxInfo reads it rather than filtering the
	// router-wide ring, so one busy group cannot evict another's history.
	ownEvents muxEventRing
}

// NewRouteGroup creates a new RouteGroup.
func NewRouteGroup(cfg *RouteGroupConfig, rt routing.Table, desc routing.RouteDescriptor, mLoggger *logging.MasterLogger) *RouteGroup {
	if cfg == nil {
		cfg = DefaultRouteGroupConfig()
	}
	logger := logging.MustGetLogger(fmt.Sprintf("RouteGroup %s", desc.String()))
	if mLoggger != nil {
		logger = mLoggger.PackageLogger(fmt.Sprintf("RouteGroup %s", desc.String()))
	}

	rg := &RouteGroup{
		cfg:                cfg,
		logger:             logger,
		desc:               desc,
		rt:                 rt,
		tps:                make([]*transport.ManagedTransport, 0),
		fwd:                make([]routing.Rule, 0),
		rvs:                make([]routing.Rule, 0),
		readCh:             make(chan []byte, cfg.ReadChBufSize),
		inCh:               make(chan routing.Packet, intakeChBufSize),
		readBuf:            bytes.Buffer{},
		remoteClosed:       make(chan struct{}),
		closed:             make(chan struct{}),
		rotateNow:          make(chan struct{}, 1),
		readDeadline:       deadline.MakePipeDeadline(),
		writeDeadline:      deadline.MakePipeDeadline(),
		handshakeProcessed: make(chan struct{}),
		networkStats:       newNetworkStats(),
		createdAt:          time.Now(),
		legPongSeen:        make(map[uuid.UUID]bool),
		legMissed:          make(map[uuid.UUID]int),
		inflightPings:      make(map[int64]uuid.UUID),
		legE2ELatency:      make(map[uuid.UUID]float64),
		legOWD:             make(map[uuid.UUID]*sbdWindow),
		legRTTWin:          make(map[uuid.UUID]*legRTTWindow),
		legForwardHops:     make(map[uuid.UUID][]routing.Hop),
		legRemoteTp:        make(map[uuid.UUID]uuid.UUID),
		legRecvSnap:        make(map[uuid.UUID]uint64),
		sbdTrials:          make(map[uuid.UUID]sbdTrial),
		sbdIndependent:     make(map[sbdPairKey]sbdSuppression),
		sbdRuledAt:         make(map[sbdPairKey]time.Time),
		// Knobs start on the visor-wide view; setKnobApp re-resolves against the
		// owning app's overrides the moment the dial tag arrives.
		knobHolder: routersettings.NewHolder(""),
		ownEvents:  muxEventRing{perGroup: true},
	}

	// The intake worker starts with the group: a handshake or data packet can
	// arrive before anything else is wired (the initiator blocks on
	// handshakeProcessed, which only the worker can close). It exits on
	// rg.closed / rg.remoteClosed, and every abandoned-group path — a failed
	// handshake send, a handshake timeout, a canceled dial, the router GC —
	// calls Close(), which ends in setRemoteClosed(), so it cannot leak on a
	// group that never completes its handshake.
	go rg.serveIntake()

	return rg
}

// SetAppName attaches app_name=<n> as a logrus field on the route
// group's logger AND records it on the rg for later lookups (mux
// info by app, etc.). Used by the router-side rg saver so 'cli
// proxy start --verbose' can scope rg-internal events to the
// originating app's session.
// No-op when name is empty.
func (rg *RouteGroup) SetAppName(name string) {
	if name == "" {
		return
	}
	rg.appName = name
	rg.setKnobApp(name)
	rg.logger = &logging.Logger{FieldLogger: rg.logger.WithField("app_name", name)}
}

// AppName returns the rg's stored app name (set via SetAppName).
// Empty when the rg was created without app context (accept-side).
func (rg *RouteGroup) AppName() string {
	return rg.appName
}

// SetTunnelRole records the dialing app's label for this route group —
// "active" or "standby". No-op when role is empty, so a route group that is
// not one of a multi-tunnel app's tunnels keeps an empty role and the field is
// simply absent from its JSON. Guarded by rg.mu because MuxStats reads it from
// whichever goroutine is serving `visor state`.
func (rg *RouteGroup) SetTunnelRole(role string) {
	if role == "" {
		return
	}
	// The app labels its tunnels, so from here on one of its groups with NO
	// label is a role that has not arrived yet rather than a group the pool
	// rules do not cover (pool_arbiter.go).
	noteRoleReportingApp(rg.AppName())
	rg.mu.Lock()
	prev := rg.tunnelRole
	rg.tunnelRole = role
	rg.mu.Unlock()
	// DEMOTION to standby: whatever width this group held as an active tunnel,
	// a pooled one holds a single leg. Give the extras back now rather than
	// leaving them parked on the exit (pool_arbiter.go).
	if role == tunnelRoleStandby && prev != tunnelRoleStandby {
		rg.shedLegsForStandby()
	}
}

// TunnelRole returns the dialing app's label for this route group ("active" /
// "standby"), or "" when it has none.
func (rg *RouteGroup) TunnelRole() string {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	return rg.tunnelRole
}

// Read reads the next packet payload of a RouteGroup.
// The Router, via transport.Manager, is responsible for reading incoming packets and pushing it
// to the appropriate RouteGroup via (*RouteGroup).readCh.
func (rg *RouteGroup) Read(p []byte) (n int, err error) {
	if rg.isClosed() {
		return 0, io.ErrClosedPipe
	}

	if rg.readDeadline.Closed() {
		return 0, timeoutError{}
	}

	if len(p) == 0 {
		return 0, nil
	}

	return rg.read(p)
}

// Write writes payload to a RouteGroup, splitting it into as many data frames as
// the wire format needs. A routing packet's payload length is a uint16, so one
// frame carries at most maxWritePayload bytes of application data. Before
// per-frame noise the stream-noise wrapper (EncryptConn) sliced every write into
// ≤4 KiB frames, so this path never saw a large payload; with per-frame noise the
// group receives the application's raw writes (net/rpc hands a >64 KiB gob reply
// down in a single call), and a payload over the frame limit has to be segmented
// here rather than rejected — the rejection burned a sequence number, and the
// peer's no-skip reorder buffer then waited on that hole forever (#4484 stage 2:
// hypervisor RPC over skynet stalled after the first 4 KiB of a Summary reply).
// Each segment goes through leg selection on its own, so a mux stripes them.
func (rg *RouteGroup) Write(p []byte) (n int, err error) {
	if rg.isClosed() {
		return 0, io.ErrClosedPipe
	}

	if rg.writeDeadline.Closed() {
		return 0, timeoutError{}
	}

	if len(p) == 0 {
		return 0, nil
	}

	// Release the traffic-gated service loops BEFORE the send-window park
	// below, so send-window is refreshing by the time a writer could block on
	// it. One relaxed atomic load when nothing is parked.
	rg.signalServiceWake()

	for n < len(p) {
		// Per-leg send window: park while every ready leg is at its window so a
		// slow leg is never fed seconds deep (test plan §3.1); bounded, so the
		// write always makes progress.
		if rg.mux != nil {
			rg.mu.Lock()
			tpsSnap := append([]*transport.ManagedTransport(nil), rg.tps...)
			rg.mu.Unlock()
			rg.mux.waitSendWindow(tpsSnap, rg.closed)
		}
		rg.mu.Lock()
		chunk := len(p) - n
		if maxChunk := rg.maxWritePayload(); chunk > maxChunk {
			chunk = maxChunk
		}
		tp, rule, leg, err := rg.nextTransport(p[n : n+chunk])
		rg.mu.Unlock()
		if err != nil {
			return n, err
		}

		written, err := rg.write(p[n:n+chunk], tp, rule, leg)
		n += written
		if err != nil {
			return n, err
		}
	}
	return n, nil
}

// maxWritePayload is the most application bytes one data frame of this group
// can carry: the uint16 packet payload limit less the mux frame's own overhead
// (sequence number, and the AEAD tag under per-frame noise). Called with rg.mu
// held (mux.seal is wired under it).
func (rg *RouteGroup) maxWritePayload() int {
	if rg.mux == nil {
		return math.MaxUint16
	}
	return math.MaxUint16 - rg.mux.frameOverhead()
}

// Close closes a RouteGroup.
func (rg *RouteGroup) Close() error {
	if rg.isClosed() {
		return io.ErrClosedPipe
	}

	if rg.isRemoteClosed() {
		// remote already closed, everything is cleaned up,
		// we just need to close signal channel at this point.
		// Guard with closedOnce: two concurrent Close() callers (e.g. an
		// app-level Close and the GC's removeRouteGroupOfRule->Close) can both
		// pass the isClosed() gate in the window before the channel is closed,
		// and a bare close() here would panic with "close of closed channel".
		rg.closedOnce.Do(func() { close(rg.closed) })
		return nil
	}

	atomic.StoreInt32(&rg.closeInitiated, 1)

	// rg.mu is deliberately NOT held across this call: close() broadcasts close
	// packets (2s ctx) and then waits up to closeRoutineTimeout for the peer's
	// replies, and serveTransportManager is a SINGLE loop that dispatches every
	// transport's packets through rg.mu — a data packet for the closing group
	// would block on it and stall the whole visor's packet intake for ~4s
	// (the #4217 deadlock class). close() takes rg.mu itself, only to snapshot.
	return rg.close(routing.CloseRequested)
}

// LocalAddr returns destination address of underlying RouteDescriptor.
func (rg *RouteGroup) LocalAddr() net.Addr {
	return rg.desc.Dst()
}

// RemoteAddr returns source address of underlying RouteDescriptor.
func (rg *RouteGroup) RemoteAddr() net.Addr {
	return rg.desc.Src()
}

// SetDeadline sets both read and write deadlines.
func (rg *RouteGroup) SetDeadline(t time.Time) error {
	if err := rg.SetReadDeadline(t); err != nil {
		return err
	}

	return rg.SetWriteDeadline(t)
}

// SetReadDeadline sets read deadline.
func (rg *RouteGroup) SetReadDeadline(t time.Time) error {
	rg.readDeadline.Set(t)
	return nil
}

// SetWriteDeadline sets write deadline.
func (rg *RouteGroup) SetWriteDeadline(t time.Time) error {
	rg.writeDeadline.Set(t)
	return nil
}

// IsAlive checks whether connection is alive.
func (rg *RouteGroup) IsAlive() bool {
	return !rg.isClosed() && !rg.isRemoteClosed()
}

// Latency returns latency till remote (ms).
func (rg *RouteGroup) Latency() time.Duration {
	return rg.networkStats.Latency()
}

// UploadSpeed returns upload speed (bytes/s).
func (rg *RouteGroup) UploadSpeed() uint32 {
	return rg.networkStats.UploadSpeed()
}

// DownloadSpeed returns download speed (bytes/s).
func (rg *RouteGroup) DownloadSpeed() uint32 {
	return rg.networkStats.DownloadSpeed()
}

// BandwidthSent returns amount of bandwidth sent (bytes).
func (rg *RouteGroup) BandwidthSent() uint64 {
	return rg.networkStats.BandwidthSent()
}

// BandwidthReceived returns amount of bandwidth received (bytes).
func (rg *RouteGroup) BandwidthReceived() uint64 {
	return rg.networkStats.BandwidthReceived()
}

// SetError sets the close error.
func (rg *RouteGroup) SetError(err error) {
	rg.errorMu.Lock()
	defer rg.errorMu.Unlock()

	rg.closeError = err
}

// GetError gets the close error.
func (rg *RouteGroup) GetError() error {
	rg.errorMu.RLock()
	defer rg.errorMu.RUnlock()

	return rg.closeError
}

// read reads incoming data. It tries to fetch the data from the internal buffer.
// If buffer is empty it blocks on receiving from the data channel
func (rg *RouteGroup) read(p []byte) (int, error) {
	// first try the buffer for any already received data
	rg.mu.Lock()
	if rg.readBuf.Len() > 0 {
		n, err := rg.readBuf.Read(p)
		rg.mu.Unlock()

		return n, err
	}
	rg.mu.Unlock()

	// Data already delivered outranks a remote close. Without this, a peer that
	// writes and immediately closes races itself: both remoteClosed and readCh
	// are ready, Go picks a ready case at random, and half the time the reader
	// gets EOF while the bytes sit unread in readCh — a stream must surface
	// buffered data first and report EOF only once it's drained. (Cost the
	// skychat file transfer its final receipt frame, turning a delivered file
	// into a reported failure.)
	select {
	case data, ok := <-rg.readCh:
		return rg.readData(data, ok, p)
	default:
	}

	select {
	case <-rg.readDeadline.Wait():
		return 0, timeoutError{}
	case <-rg.closed:
		return 0, io.ErrClosedPipe
	case <-rg.remoteClosed:
		// readCh is never closed (see close()), so a remote-initiated close is
		// observed here rather than via a closed readCh returning ok==false. One
		// last non-blocking check for the same reason as above: the close signal
		// may have won a race against data that is already here.
		select {
		case data, ok := <-rg.readCh:
			return rg.readData(data, ok, p)
		default:
			return 0, io.EOF
		}
	case data, ok := <-rg.readCh:
		return rg.readData(data, ok, p)
	}
}

// readData buffers one delivery from readCh into p.
func (rg *RouteGroup) readData(data []byte, ok bool, p []byte) (int, error) {
	if !ok || len(data) == 0 {
		// route group got closed or empty data received. Behavior on the empty
		// data is equivalent to the behavior of `read()` unix syscall as described here:
		// https://www.ibm.com/support/knowledgecenter/en/SSLTBW_2.4.0/com.ibm.zos.v2r4.bpxbd00/rtrea.htm
		return 0, io.EOF
	}

	rg.mu.Lock()
	defer rg.mu.Unlock()

	return ioutil.BufRead(&rg.readBuf, data, p)
}

func (rg *RouteGroup) write(data []byte, tp *transport.ManagedTransport, rule routing.Rule, leg int) (int, error) {
	var packet routing.Packet
	var err error
	if rg.mux != nil {
		// Tag the retx entry with the transport's stable UUID (leg indices
		// shift on slice compaction, so an index tag goes stale mid-session).
		tpID := uuid.Nil
		if tp != nil {
			tpID = tp.Entry.ID
		}
		packet, _, err = rg.mux.wrapPayload(rule.NextRouteID(), data, tpID)
	} else {
		packet, err = routing.MakeDataPacket(rule.NextRouteID(), data)
	}
	if err != nil {
		return 0, err
	}

	rg.logger.WithField("func", "RouteGroup.write").Tracef("Writing packet of type %s, route ID %d and next ID %d", packet.Type(),
		rule.KeyRouteID(), rule.NextRouteID())

	ctx, cancel := context.WithCancel(context.Background())

	errCh := rg.writePacketAsync(ctx, tp, packet, rule.KeyRouteID())
	defer cancel()

	select {
	case <-rg.writeDeadline.Wait():
		return 0, timeoutError{}
	case err := <-errCh:
		if err != nil {
			return 0, err
		}

		rg.lastSent.Store(time.Now().UnixNano())

		// Per-mux-leg sent counter. The aggregate networkStats is
		// already updated inside writePacket (and gates on packet
		// type); this records the same packet against the specific
		// leg that carried it so 'proxy mux-info' can show
		// where bandwidth is going across the mux'd routes.
		if rg.mux != nil && leg >= 0 {
			rg.mux.recordSent(leg, uint64(packet.Size()))
		}

		// FEC: if wrapPayload just completed a block, schedule its repair frames
		// on the fastest live leg (off the data path). Inert unless CapFEC negotiated.
		if rg.mux != nil && rg.mux.fecEnabled {
			go rg.flushFECRepairs()
		}

		return len(data), nil
	}
}

// flushFECRepairs drains any queued FEC repair frames and sends each on the
// FASTEST live leg (the same rationale as retransmits: a repair rescues a
// slow-leg straggler, so it must not itself ride the slow leg). Best-effort — a
// repair that fails to send just leaves the receiver on the reorder+SACK
// fallback for that block. Runs in its own goroutine off the data path.
func (rg *RouteGroup) flushFECRepairs() {
	if rg.mux == nil || !rg.mux.fecEnabled {
		return
	}
	frames := rg.mux.fecDrainRepairs()
	if len(frames) == 0 {
		return
	}
	rg.mu.Lock()
	tp, rule, _, err := rg.mux.selectFastestTransport(rg.tps, rg.fwd)
	rg.mu.Unlock()
	if err != nil || tp == nil || rule == nil {
		return
	}
	for _, f := range frames {
		pkt, perr := routing.MakeRepairPacket(rule.NextRouteID(), f.blockID, f.idx, f.symLen, f.symbol)
		if perr != nil {
			continue
		}
		ctx, cancel := context.WithCancel(context.Background())
		rg.mux.fecRepairBytesSent.Add(uint64(pkt.Size()))
		errCh := rg.writePacketAsync(ctx, tp, pkt, rule.KeyRouteID())
		select {
		case <-rg.writeDeadline.Wait():
		case <-errCh:
		}
		cancel()
	}
}

// writePaddingFrame emits one empty sequenced data frame (a FEC padding frame)
// through the normal mux send path, bypassing Write's empty-payload short-circuit.
func (rg *RouteGroup) writePaddingFrame() error {
	if rg.isClosed() || rg.writeDeadline.Closed() {
		return io.ErrClosedPipe
	}
	var empty []byte
	rg.mu.Lock()
	tp, rule, leg, err := rg.nextTransport(empty)
	rg.mu.Unlock()
	if err != nil {
		return err
	}
	_, err = rg.write(empty, tp, rule, leg)
	return err
}

func (rg *RouteGroup) writePacketAsync(ctx context.Context, tp *transport.ManagedTransport, packet routing.Packet,
	ruleID routing.RouteID) chan error {
	errCh := make(chan error)
	go func() {
		defer close(errCh)
		err := rg.writePacket(ctx, tp, packet, ruleID)
		select {
		case <-ctx.Done():
			return
		case errCh <- err:
			return
		}
	}()

	return errCh
}

func (rg *RouteGroup) writePacket(ctx context.Context, tp *transport.ManagedTransport, packet routing.Packet,
	ruleID routing.RouteID) error {
	err := tp.WritePacket(ctx, packet)
	// note equality here. update activity only if there was NO error
	if err == nil {
		if packet.Type() != routing.ClosePacket && packet.Type() != routing.HandshakePacket {
			rg.networkStats.AddBandwidthSent(uint64(packet.Size()))
		}

		if err := rg.rt.UpdateActivity(ruleID); err != nil {
			if !rg.isClosed() {
				rg.logger.WithError(err).Debugf("error updating activity of rule %d", ruleID)
			}
		}
	}

	return err
}

// Initiator reports whether this visor dialed the remote end of the route
// (true) or accepted the route from a setup-node request (false).
func (rg *RouteGroup) Initiator() bool {
	return rg.initiator
}

// honorsMirrorActiveSet reports whether this side must DEFER its active/standby
// set to the peer's mirror (CapLegState) rather than run its own promote/demote
// controllers. The initiator owns the active-set decision and mirrors park/promote
// to the acceptor (see the unidir/mirror comments on routeMux); the ACCEPTOR must
// honor that mirror. When leg-state signaling is negotiated, the acceptor's own
// homogeneity controllers (enforceLatencyBand / enforceBottleneckGroups) must NOT
// run — otherwise they re-admit legs the initiator parked and the bulk sender
// sprays its send traffic across them (the wide-mux download over-subscription:
// measured the acceptor holding ~33 active legs while the initiator had ~8, so
// ~99% of a download rode legs the initiator had parked). With signaling off,
// both ends keep their local behavior (no mirror to honor).
func (rg *RouteGroup) honorsMirrorActiveSet() bool {
	return rg.mux != nil && rg.mux.legStateEnabled && !rg.initiator
}

const (
	// legStallIgnore: the legs are IDLE, not stalled — leave them alone.
	legStallIgnore legStallAction = iota
	// legStallPark: park to warm standby (non-destructive, re-promotable).
	legStallPark
	// legStallRemove: remove + self-heal re-dial (a genuine data black-hole).
	legStallRemove
)
