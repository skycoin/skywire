// Package router pkg/router/route_mux.go c2-net-routing
package router

import (
	"fmt"
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/router/routersettings"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

// LegStats is a snapshot of per-mux-leg traffic counters at a point
// in time. One entry per active route in the rg's tps[] order.
//
// Counters are cumulative since route-group creation (atomic uint64);
// callers wanting rates take two snapshots and divide by elapsed time.
// Used by 'cli proxy mux-info' to show where bandwidth is going across
// the mux'd routes — the missing piece for verifying that adding more
// routes actually aggregates throughput rather than just trading share.
type LegStats struct {
	// Index in the rg's tps[] slice. Stable for the rg's lifetime
	// (transports are appended, never re-ordered) so the index is
	// also a stable identifier for runtime add/remove operations.
	Index int
	// SentBytes / SentPackets are what THIS leg carried outbound.
	SentBytes   uint64
	SentPackets uint64
	// RecvBytes / RecvPackets are what THIS leg carried inbound.
	// Resolved from the reverse-rule's KeyRouteID at packet-handle
	// time, so it reflects what actually arrived on this transport,
	// not what the peer said it sent.
	RecvBytes   uint64
	RecvPackets uint64
	// PayloadBytes is the UNIQUE in-order payload this leg delivered (each seq
	// counted once, on the leg it first arrived on) — retransmits/duplicates
	// excluded, so the per-leg values sum to the transfer size and give a
	// confound-free per-direction attribution (which legs carried the download vs
	// the upload), unlike RecvBytes which includes retransmit/duplicate inflation.
	PayloadBytes uint64
	// DupBytes is inbound DUPLICATE data this leg carried (seqs already
	// delivered/buffered on arrival) — the peer's spurious-retransmit waste,
	// which selectFastestTransport concentrates on the fastest leg. RepairBytes
	// is inbound FEC repair frames (deliberate overhead). Together with
	// PayloadBytes they decompose RecvBytes so "why is this (standby) leg
	// receiving" is answerable from telemetry.
	DupBytes    uint64
	RepairBytes uint64
	// Retransmits is how many SACK retransmit packets THIS leg has
	// carried. A high retransmits:sentPackets ratio marks a lossy leg —
	// the signal a routing policy needs to shed lossy intermediates and
	// the scheduler needs to deweight them.
	Retransmits uint64
	// GoodputUpBps / GoodputDownBps are this leg's recent goodput split by
	// direction — the EWMA of the SENT (up) and RECV (down) byte deltas per
	// second over the telemetry refresh window (~1s for the status page).
	// Distinct from the cumulative SentBytes/RecvBytes counters above: they
	// are the RATE each direction is moving now, not the lifetime total. 0
	// until a second sample lands. Sampled in snapshotLegs.
	GoodputUpBps   float64
	GoodputDownBps float64
	// GoodputBps is the combined (up+down) recent goodput, retained as the
	// sum of GoodputUpBps+GoodputDownBps for back-compat with callers that
	// want a single figure.
	GoodputBps float64
}

const (
	// goodputEWMAAlpha weights the newest goodput sample in the per-leg
	// bytes/sec EWMA (snapshotLegs). Higher = more responsive, noisier.
	goodputEWMAAlpha = 0.4
	// goodputMinSampleNano is the minimum window between goodput samples: two
	// observers refreshing closer than this reuse the stored rate rather than
	// dividing a tiny byte delta by a tiny interval into a spurious spike.
	goodputMinSampleNano = int64(250 * time.Millisecond)
	// capacityColdFloorFrac is the floor share a just-promoted active leg gets
	// under WeightModeCapacity, as a fraction of the fastest active leg's weight.
	// Big enough that a fresh leg carries a measurable trickle to prove its
	// goodput and ramp; small enough that a persistently slow leg stays near it
	// and can't open a large reorder gap. See rebuildWeights.
	capacityColdFloorFrac = 0.15
)

type legCounters struct {
	sentBytes   uint64 // atomic
	sentPackets uint64 // atomic
	recvBytes   uint64 // atomic
	recvPackets uint64 // atomic
	retransmits uint64 // atomic
	// payloadBytes is the UNIQUE in-order payload this leg delivered: each
	// sequence credited once, to the leg it FIRST arrived on. Unlike recvBytes
	// (every inbound frame, incl. retransmits/duplicates), a retransmit of a seq
	// already seen on another leg is NOT counted, so the per-leg payloadBytes sum
	// equals the transfer size and cleanly attributes which legs carried a
	// direction's data — the confound-free basis for per-direction leg telemetry.
	payloadBytes uint64 // atomic
	// dupBytes is inbound DUPLICATE data on this leg (a seq already delivered
	// or buffered when it arrived here) — the peer's spurious-retransmit waste,
	// which rides the fastest leg and otherwise masquerades as payload in
	// recvBytes. repairBytes is inbound FEC repair frames — deliberate overhead,
	// counted apart from waste. Both atomic.
	dupBytes    uint64 // atomic
	repairBytes uint64 // atomic
	// lastTotalBytes snapshots sentBytes+recvBytes at the previous
	// capacity-weight rebuild; the delta since then is this leg's
	// recent throughput, used by WeightModeCapacity. Touched only
	// under legMu in rebuildWeights, so it needs no atomic.
	lastTotalBytes uint64
	// Goodput-rate sampling (bytes/sec EWMA over the observer's refresh
	// window), maintained by snapshotLegs — NOT the data path and NOT the
	// capacity rebuild. lastRateSentBytes/lastRateRecvBytes/lastRateNano
	// snapshot the sent counter, recv counter and wall clock at the previous
	// sample; each direction's delta over the elapsed window, EWMA-smoothed,
	// is goodputUpBps (sent/sec) / goodputDownBps (recv/sec). Kept separate
	// from lastTotalBytes (which the capacity rebuild resets on its own
	// cadence) so the two samplers never disturb each other. Touched under
	// legMu (snapshotLegs upgrades to a write lock for the sample), so no
	// atomic needed.
	lastRateSentBytes uint64
	lastRateRecvBytes uint64
	lastRateNano      int64
	goodputUpBps      float64
	goodputDownBps    float64
	// ECF (WeightModeECF) per-leg state, maintained by rebuildWeights' ECF
	// branch (under legMu, off the data path). ecfLastSentBytes snapshots the
	// sent counter at the previous ECF refresh so the delta over the refresh
	// window is this leg's send rate (kept separate from lastTotalBytes /
	// lastRateSentBytes so the ECF sampler never disturbs the capacity or
	// telemetry samplers). ecfRttMs / ecfJitterMs are the EWMA'd mean RTT and
	// jitter (sigma) the ECF predicate consumes — the RTT over the leg's
	// END-TO-END feedback delay, max(first-hop RTT, send→ack delay).
	ecfLastSentBytes uint64
	// ecfLastAckedBytes snapshots the bytes the peer's SACKs had acknowledged on
	// this leg at the previous ECF refresh: the delta is the leg's DELIVERY
	// rate, which sizes its send window (see rebuildWeights).
	ecfLastAckedBytes uint64
	// ecfLastAckedNano is when ecfLastAckedBytes was taken (a refresh that saw
	// new acks); ecfCwndBytes is the window that refresh proved, kept by
	// refreshes that see no new ack.
	ecfLastAckedNano int64
	ecfCwndBytes     float64
	// ecfDelivBps is an EWMA of that same delivery rate (bytes acknowledged per
	// second), kept apart from the window it sizes because the outclassed-leg
	// gate judges PRODUCTIVITY with it: a leg whose delay basis is inflated by
	// its own queue but which is still delivering its share is not outclassed.
	// A refresh that sees no new ack for over a second folds in a zero, so a
	// leg that went silent decays toward 0 instead of holding a stale rate.
	ecfDelivBps float64
	ecfRttMs    float64
	ecfJitterMs float64
	// ecfRttMinMs is the leg's baseline (minimum observed) RTT — the
	// uncongested latency. It seeds the stable BDP for cwndBytes and, against
	// the live ecfRttMs, is the congestion signal ecfSaturated reads. Tracked
	// as a running minimum with a slow upward creep (ecfRttMinCreep) so a leg
	// whose true latency genuinely rose is not pinned to a stale floor forever.
	ecfRttMinMs float64
	// ecfHopRttMinMs is the same running minimum over the FIRST-HOP transport
	// latency alone. It is the one delay on this leg that our own send window
	// cannot inflate — the transport measures it out of band — so it is the
	// ceiling the BDP baseline is held under (see refreshLegWindows).
	ecfHopRttMinMs float64
	// probeBasisBits is this leg's delay basis (float64 bits) at the moment
	// ruleProbeOnlyLegs judged it OUTCLASSED by the group's best active leg —
	// and 0 while it is not outclassed, which is what the selection gate reads
	// to mean "full share". probeWinNano opens the current probe window and
	// probeWinBytes is what has been placed on the leg inside it, so an
	// outclassed leg carries at most LegProbeBytes per window instead of a
	// proportional share. All three are atomic: the ruling is written under
	// legMu by the window refresh, the bytes are charged from the send path by
	// recordSent, and the gate reads them per pick.
	probeBasisBits uint64 // atomic (float64 bits)
	probeWinNano   int64  // atomic
	probeWinBytes  uint64 // atomic
}

// routeMux encapsulates route multiplexing state and logic.
// It is composed into RouteGroup as an optional field (nil when mux is not negotiated).
// This separates mux concerns (sequencing, reordering, SACK, transport selection)
// from the core route-layer connection managed by RouteGroup.
type routeMux struct {
	logger *logging.Logger

	// Sequence numbering for outgoing packets
	writeSeq uint32 // atomic: next outgoing sequence number
	tpIndex  uint32 // atomic: round-robin fallback index for transport selection

	// RACK-TLP tail-loss probe + DSACK reorder-window adaptation (see rack_tlp.go).
	// lastSendNano stamps the last DATA/retransmit frame put on the wire (the TLP
	// idle timer reads it). tlpProbeCount is the number of consecutive tail probes
	// fired since the last ack progress — bounded so a truly dead tail doesn't probe
	// forever. rackFactorMilli is the DSACK-adapted reorder tolerance ×1000: a
	// duplicate report from the receiver widens it (we retransmitted too eagerly),
	// clean acks decay it back toward the static baseline. All atomic.
	lastSendNano    int64
	tlpProbeCount   int32
	rackFactorMilli int64
	lastAckedContig uint32 // atomic: highest SACK lastContiguous seen (ack-progress edge for TLP reset)
	// ackDelayMilli is an EWMA (×1000) of the measured send→ack delay of
	// never-retransmitted frames (fed by retxBuf.onAckDelay). Under load it is
	// the queue, not the wire, that dominates feedback delay: a saturated leg
	// holds seconds of in-flight data while the transport's idle-ping RTT still
	// reads ~25ms, and a RACK threshold built only on the latter declares every
	// queued frame lost (measured: 899 spurious retransmits on a ~1250-frame
	// upload). rackThreshold takes the max of both signals. Atomic; 0 = no
	// sample yet.
	ackDelayMilli int64
	// ackDelayByTp is the same EWMA kept PER LEG, keyed by the transport the
	// frame last rode. The group-wide value is refreshed by every SACK's
	// fast-leg samples and collapses back within a second of each slow-leg
	// sample, so judged by it a frame queued on a slow leg reads as lost while
	// it is merely in flight (measured live 2026-09-16: 26801 of a three-leg
	// group's 29360 retransmits came from the reactive SACK path). A leg's
	// holes are judged against its own estimate (rackThresholdFor).
	ackDelayByTp   map[uuid.UUID]*ackDelayEst
	ackDelayByTpMu sync.Mutex
	// sbdFoldNano records, per leg, when the last send→ack delay sample was
	// handed to onLegDelaySample, so the SACK path folds at most one sample per
	// SBDSampleInterval into a leg's shared-bottleneck window instead of
	// overwriting the whole window with one burst. Guarded by ackDelayByTpMu.
	sbdFoldNano map[uuid.UUID]int64
	// onLegDelaySample, when set, receives the rate-limited per-leg send→ack
	// delay samples (ms). The route group points it at its SBD windows
	// (RouteGroup.foldLegDelaySample), which is what lets shared-bottleneck
	// detection rule within seconds of a transfer starting rather than waiting
	// out sbdMinSamples liveness pongs (~110s). Set once, before the mux carries
	// traffic; nil on a mux nobody wired (unit tests).
	onLegDelaySample func(uuid.UUID, float64)

	// legE2EByTp is each leg's smoothed END-TO-END round-trip latency (ms, from
	// the leg-liveness pong), keyed by transport ID, pushed by the route group
	// (setLegE2ERTT). The mux's own per-leg numbers are the FIRST-HOP transport
	// RTT (tp.GetLatency) and the send→ack delay (ackDelayByTp); neither is the
	// leg's route latency. A leg whose first hop is 20ms but whose whole route is
	// 410ms therefore read as a 20ms leg to every loss detector until a send→ack
	// sample landed — and that estimate expires after ackDelayStale — so its
	// in-flight frames were judged against the FAST leg's delay and declared
	// lost. Guarded by legE2EMu.
	legE2EByTp map[uuid.UUID]float64
	legE2EMu   sync.RWMutex

	// windowCh wakes a writer parked in waitSendWindow when a SACK may have
	// freed per-leg window; sendWindowWaits / sendWindowTimeouts count the waits
	// and the ones that gave up after sendWindowWaitMax (diagnostics).
	windowCh           chan struct{}
	sendWindowWaits    uint64
	sendWindowTimeouts uint64

	// reorderDrops counts RECEIVE-side packets the reorder buffer dropped at
	// maxGap (see reorderBuffer.InsertOrDrop). Previously invisible: the drop
	// was silent and the packet was SACKed anyway, so a wedge caused by it had
	// no witness at all. Non-zero here means the frontier gap grew past the
	// whole reorder window — a leg died mid-stream — and the dropped sequences
	// are waiting on the sender's retransmit. atomic.
	reorderDrops uint64

	// lastSACKNano rate-limits receiver-side SACK feedback. Cross-leg
	// reordering from latency skew makes nearly every packet arrive
	// out-of-order, so firing a SACK per out-of-order packet would spawn a
	// goroutine and emit a control packet thousands of times per second.
	// atomic: UnixNano of the last SACK we sent.
	lastSACKNano int64

	// Loss-recovery counters (all atomic), surfaced as the `recovery` object in
	// `visor state --select mux_route_groups` / `proxy mux info --json`. A
	// reorder wedge is a TWO-ENDED failure — the receiver sees a frontier stuck
	// at some seq while the sender sees nothing at all — and the only witness
	// used to be the receiver's log. These make both halves readable from either
	// end: sacksRecv/lastSACKRecvNano say whether the peer's SACKs are arriving
	// at all (a silent sender + a wedged receiver means the feedback path itself
	// is black-holed), retxSkippedMissing counts the seqs a SACK/TLP/flush asked
	// for that the retx buffer no longer held (no retransmit can ever refill
	// that gap), retxSendErrors counts the resends that failed to reach a leg,
	// tlpProbes counts tail-loss probes, and sacksSent/sackSendErrors/
	// lastSACKSentNano are the receiver-side mirror.
	retxSkippedMissing uint64
	// retxReqSACK / retxReqHOL / retxReqFlush count the sequences each
	// retransmit path ASKED to resend (reactive SACK holes past the RACK
	// threshold, the proactive head-of-line nudge, the demote-time flush), so a
	// retransmit storm names its source from visor state.
	retxReqSACK  uint64
	retxReqHOL   uint64
	retxReqFlush uint64
	// retxDeferredYoung counts the holes a SACK named that were NOT resent
	// because the frame is still younger than the delay basis of the leg it was
	// sent on (legDelayBasisMs × the reorder factor) — the duplicates that used
	// to leave on the group's clock. Rising here while retx_req_sack stays low
	// is the fix working; both rising together is a genuinely lossy leg.
	retxDeferredYoung uint64
	retxSendErrors    uint64
	tlpProbes         uint64
	sacksRecv         uint64
	lastSACKRecvNano  int64
	sacksSent         uint64
	sackSendErrors    uint64
	lastSACKSentNano  int64

	// knobHolder carries the owning route group's resolved router knobs (see
	// settings_group.go). Shared with the RouteGroup, so a `route settings --app`
	// override reaches the dataplane and the service loops together.
	knobHolder *routersettings.Holder

	// Incoming packet reordering
	reorderBuf *reorderBuffer

	// Adaptive transport weighting based on latency
	tpSelector *transportSelector

	// SACK retransmission
	sackEnabled bool         // true when both peers advertised CapSACK
	sackTracker *sackTracker // receiver: tracks received seqs for SACK generation
	retxBuf     *retxBuffer  // sender: holds unACKed packets for retransmission

	// Proactive head-of-line retransmit (see hol_retx.go). holRetxEnabled is true
	// when both peers advertised CapHOLRetx (implies sackEnabled — it reuses the
	// SACK wire message). When false the mux keeps today's purely reactive SACK
	// behavior. holRetx is the sender-side per-seq rate limiter for proactive
	// retransmits; holSACKNano rate-limits the receiver-side proactive HoL SACK
	// (separate from lastSACKNano so it doesn't fight the window-ack SACK limiter).
	holRetxEnabled bool
	holRetx        *holRetxTracker
	holSACKNano    int64 // atomic: UnixNano of the last proactive HoL SACK we sent

	// legStateEnabled is true when both peers advertised CapLegState. When set, a
	// park/promote of a leg is signaled to the remote (LegStatePacket) so it
	// mirrors the active/standby set on its send side — otherwise standby is a
	// send-side-only decision and the bulk-sending peer stripes across every
	// established leg, head-of-line-stalling the reorder frontier (the wide-mux
	// download stall). See handleLegStatePacket / sendLegState in route_group.go.
	legStateEnabled bool

	// Unidirectional per-leg send selection (CapUniDir, see unidir.go). When
	// directional is set (both peers advertised CapUniDir), each end restricts its
	// OWN send to legs matching its direction: the initiator uploads on the DIRECT
	// (1-hop) leg, the acceptor downloads on the MULTIHOP legs. flipped swaps that
	// mapping (heavy direction gets the mux). dstPK/srcPK identify a direct leg
	// (its transport's remote is one of the route-group endpoints). Guarded by
	// legMu; read as a snapshot (dirConfig) on the send path.
	directional bool
	flipped     bool
	initiator   bool
	dstPK       cipher.PubKey
	srcPK       cipher.PubKey
	// flipPin is the operator's MANUAL direction pin (see unidir.go setFlipPin):
	// routing.DirectionAuto (0) leaves the flip controller in charge;
	// DirectionPinDefault/DirectionPinFlipped force the mapping and put the
	// controller to sleep until released. Guarded by legMu like flipped.
	flipPin byte
	// Flip-controller hysteresis state (see unidir.go unidirFlipTick). Touched
	// ONLY by the single unidir-flip loop goroutine, so no lock of their own.
	flipUpHits   int
	flipDownHits int
	flipCooldown int

	// Per-frame noise (inverse-mux). When CapPerFrameNoise is negotiated the
	// RouteGroup installs these: seal AEAD-encrypts each outgoing frame under
	// its sequence-nonce (in wrapPayload, before retx storage so retransmits
	// resend the sealed frame verbatim); open AEAD-decrypts an incoming frame
	// under its sequence-nonce (in deliverData, before the reorder buffer).
	// Both nil for stream-noise/plain groups (no-op). Set once at handshake
	// completion, read on the data path; a plain word write/read is safe under
	// the RouteGroup's ordering (set-before-first-data, mu-guarded install).
	seal func(seq uint32, plaintext []byte) []byte
	open func(seq uint32, ciphertext []byte) ([]byte, error)

	// Forward error correction (fec.go / fec_mux.go). fecEnabled is true when
	// BOTH peers advertised CapFEC (implies CapMux). FEC operates in the WIRE
	// (post-seal) domain: the striper batches K on-wire data-frame payloads per
	// block and emits R repair frames (queued in fecRepairQ for the send loop to
	// schedule on a fast leg); the reassembler retains on-wire block symbols and,
	// when the reorder frontier is gap-blocked, reconstructs the missing on-wire
	// frame from any K of the block's K+R symbols — which is then opened via the
	// normal per-frame path and inserted, so recovery is bound by the FAST legs
	// instead of the slow leg. All nil/false unless negotiated (inert — the
	// pre-integration behavior is preserved byte-for-byte when fecEnabled=false).
	fecEnabled     bool
	fecStriper     *fecStriper
	fecReassembler *fecReassembler
	fecRepairMu    sync.Mutex
	fecRepairQ     []fecRepairFrame
	// FEC-block striping cap: keep no single leg above fecDefaultR frames of any
	// K-frame FEC block, so a leg that fully stalls stays within FEC's R-erasure
	// recovery (a leg carrying >R of a block's K frames exceeds what repair can
	// reconstruct, and the group falls back to SACK-retransmit — the webrtc wedge).
	// fecStripeUsed counts, for the CURRENT block, how many frames each leg took.
	fecStripeMu    sync.Mutex
	fecStripeBlock uint32
	fecStripeUsed  map[int]int
	// FEC telemetry (atomic). fecRepairBytesSent/Recv are cumulative repair-frame
	// bytes this group scheduled onto / received from legs; fecReconstructs counts
	// frontier frames recovered from repair (each a slow-leg stall avoided). These
	// let an observer decompose mux traffic into data vs repair (the true FEC
	// overhead) vs retransmit, separately from the aggregate byte counters.
	fecRepairBytesSent uint64
	fecRepairBytesRecv uint64
	fecReconstructs    uint64

	// Per-leg traffic counters parallel to the rg's tps[] / fwd[] /
	// rvs[] slices. Mutated atomically. Read via Snapshot().
	// legMu guards the slice itself (extended on AppendRoute), not
	// the individual counters.
	legMu sync.RWMutex
	legs  []*legCounters
	// ready[i] reports whether leg i may be SELECTED for sending. The
	// primary leg (0) is ready from the start; an aux leg only becomes
	// ready once we have received a packet (data or handshake) on it,
	// which proves the peer finished registering its rule. Without this,
	// the selector could steer the first writes onto an aux leg the peer
	// has not set up yet — those packets are dropped, the reorder buffer
	// stalls on the missing sequence, and the stream hangs until it is
	// closed (the mux>=2 "0 bytes / close code 0" bug). Guarded by legMu.
	ready []bool
	// standby[i] marks leg i as a WARM STANDBY: its rules stay installed and
	// the keepalive/liveness loops keep it alive, but it is never SELECTED for
	// sending (folded into legReadyAt). A demoted leg still receives on its
	// reverse rule — standby is a send-side decision. Promoting a standby leg
	// is instant (clear the flag) with no route setup, vs the drop→re-dial
	// tear-and-rebuild. Parallel to ready[]; grown/compacted in lockstep.
	// Guarded by legMu. See docs/warm_standby_legs_rfc.md.
	standby []bool

	// ecfLastRebuildNano is the wall-clock (UnixNano) of the previous ECF-state
	// refresh, used to turn each leg's sent-byte delta into a bytes/sec rate.
	// Touched only under legMu in rebuildWeights' ECF branch.
	ecfLastRebuildNano int64

	// legGroups holds each leg's SHARED-BOTTLENECK group id (RFC 8382; see
	// bottleneck.go), parallel to legs[]. Legs with the same id are judged to
	// funnel through one physical pipe, so rebuildWeights counts the group as ONE
	// unit of capacity instead of N competing pipes. Recomputed each data-progress
	// tick by the route group and pushed via SetLegGroups; a leg with no entry (or
	// a value equal to its own index) is its own singleton group — the default, so
	// a mux with no bottleneck detection behaves exactly as before. Guarded by
	// legMu.
	legGroups []int

	// standbyNewLegs makes every NEWLY-grown aux leg (index > 0) enter the
	// warm-standby pool instead of going straight into the active send set.
	// The primary leg (index 0) is never affected. Set only when a promoting
	// rotation engine is wired (SetRotation), so the engine's paced,
	// goodput-gated growActive/promote path admits legs one per tick as its
	// throughput signal warrants — instead of every dialed leg going hot the
	// instant it receives its first inbound packet (markLegReady), which
	// floods the active set faster than the reactive stall gate can demote and
	// churns the group into collapse. When false (no rotation engine) new legs
	// stay active-on-add so a non-adaptive group never strands aux legs in
	// standby. Guarded by legMu.
	standbyNewLegs bool

	// legLatencyFn resolves ONE leg's measured END-TO-END route latency in ms
	// from its transport id — the EWMA of the per-leg liveness pong across ALL
	// hops (rg.legEndToEndLatencyMs), wired at mux construction. 0 means "no
	// sample yet"; nil means the mux was built without a route group (tests).
	// The liveness pongs run on every leg on their own cadence, so this is a
	// measurement the mux can read for a leg it is NOT currently sending on —
	// no probe traffic of its own. Read on the send path under the route
	// group's mu; the rg.mu -> legLivenessMu order is the documented one.
	legLatencyFn func(uuid.UUID) float64

	// confinedFwd* cache the leg confinedForwardLeg last chose, so the forward
	// confinement re-measures at most once per confinedForwardRefresh instead of
	// once per frame, and so a burst stays on one leg rather than oscillating
	// between two near-equal legs. confinedFwdChal* hold the CHALLENGER a
	// candidate must be for forwardSwitchSamples consecutive samples before the
	// direction moves. Touched only from selectTransportRaw and the retransmit
	// pick, both of which run under the route group's mu.
	confinedFwdIdx      int
	confinedFwdLatMs    float64
	confinedFwdAtNano   int64
	confinedFwdChalIdx  int
	confinedFwdChalHits int

	// confinedFwdCur mirrors confinedFwdIdx for readers OUTSIDE the route
	// group's mu — waitSendWindow runs on the writer with rg.mu dropped, and it
	// has to know which leg's window it is waiting on. -1 = no leg confined.
	confinedFwdCur atomic.Int64

	// onForwardRehome, when wired (SetForwardRehomeFn), is called under the
	// route group's mu whenever the forward direction moves to a different leg,
	// so the route group can record a forward_rehomed mux event. prev is -1 the
	// first time a leg is chosen (no event is emitted for that).
	onForwardRehome func(prev, next int, tp *transport.ManagedTransport, legs int, reason string)

	// onLegProbeRuling, when wired (SetLegProbeRulingFn), is called from the
	// window refresh with legMu dropped whenever a leg is ruled probe-only or
	// restored to a full share, so the route group can record the event.
	onLegProbeRuling func(idx, legs int, tp *transport.ManagedTransport, probeOnly bool, reason string)
}

const (
	// confinedForwardRefresh bounds how often the forward confinement
	// re-measures its leg. A burst is thousands of frames; the leg latencies
	// behind the decision move on the liveness-pong cadence, so re-reading them
	// per frame buys nothing and costs a lock each time.
	confinedForwardRefresh = 250 * time.Millisecond
	// forwardSwitchMarginDefault is how much LOWER a challenger leg's end-to-end
	// latency must be before the confinement moves off the leg it already holds
	// (0.2 = 20 % lower), and forwardSwitchSamples is how many CONSECUTIVE
	// refreshes it must clear that margin for. Live as ForwardSwitchMargin().
	//
	// One sample was not enough: the live legs-2 set (44 ms vs 166 ms) still
	// produced 6 forward flips in ten rows, because a single loaded sample from
	// the incumbent crosses a 15 % margin easily — and every flip splits the
	// upload across two skewed legs, which is the collapse this confinement
	// exists to prevent (measured x0.24 on a 50 MB upload).
	forwardSwitchMarginDefault  = 0.2
	forwardSwitchSamplesDefault = 2
)

// reorderWindow bounds how far the receiver's reorder buffer will hold
// out-of-order packets waiting for a gap to fill before it force-flushes.
//
// The underlying leg transports (stcpr / squicr / sudph) are RELIABLE and
// ordered, so a gap across mux legs is not loss — it is latency SKEW: the
// "missing" sequence is simply in flight on a slower leg and arrives within
// the inter-leg skew window. The buffer must therefore HOLD the gap until it
// fills, never skip it: skipping delivers an out-of-order hole that corrupts
// the noise/TLS byte stream riding the mux (the old maxGap=64 force-flush was
// the root cause of mux>1 wedging under load — the fast leg routinely ran 64
// ahead of a ~tens-of-ms-slower leg, triggering a destructive flush every
// time). Sized to absorb the bandwidth-delay-product of a realistic skew
// (hundreds of ms at multi-MB/s aggregate) so normal mux>1 operation never
// flushes. A flush at this cap is a last-resort OOM guard for a genuinely
// stalled/dead leg — which per-leg liveness prunes, after which the peer
// retransmits that leg's unacked sequences on the surviving legs.
// Sized to the aggregate bandwidth-delay-product per the MPTCP receive-buffer
// requirement B >= 2*sum(BW)*RTT_max: at the ~500 Mbps gigabit target with a
// ~350 ms slowest-active-leg RTT that is ~46 MB ~= 32Ki packets, so the old 2Ki
// (~2.8 MB) window was ~16x too small — it collapsed throughput under wide-mux
// skew and hit the OOM backstop. This is the CAP, not steady occupancy: normal
// skew buffers only a handful; only a very lagged/dead leg approaches it, and at
// the cap the buffer now DROPS excess (never skips) while the leg-dataprogress
// prune + SACK retransmit refill the frontier in order. TODO: make adaptive to
// the measured RTT_max of the active set instead of a flat gigabit-sized cap.
const reorderWindowDefault = 32768

// newRouteMux creates a new routeMux instance with all sub-components initialized.
func newRouteMux(logger *logging.Logger, sackEnabled bool) *routeMux {
	m := &routeMux{
		logger:      logger,
		knobHolder:  routersettings.NewHolder(""),
		reorderBuf:  newReorderBuffer(routersettings.ReorderWindow.Int()),
		tpSelector:  newTransportSelector(),
		sackEnabled: sackEnabled,
		sackTracker: newSACKTracker(),
		// Sender-side retx window kept in step with the receiver's reorder
		// window so a genuinely-lost sequence is still held for retransmit
		// while the receiver is holding the gap open for it.
		retxBuf:  newRetxBuffer(routersettings.ReorderWindow.Int()),
		windowCh: make(chan struct{}, 1),
		// Proactive HoL retransmit tracker is always constructed; it is only
		// consulted when holRetxEnabled is set at handshake (see hol_retx.go).
		holRetx: newHolRetxTracker(),
		// RACK reorder factor starts at the static baseline; DSACK feedback
		// widens it and clean acks decay it back (see rack_tlp.go).
		rackFactorMilli: int64(routersettings.RackReorderFactor.Ratio() * 1000),
		// No leg held by the forward confinement yet, and no challenger.
		confinedFwdIdx:     -1,
		confinedFwdChalIdx: -1,
	}
	m.confinedFwdCur.Store(-1)
	// Feed measured send→ack delays into the RACK basis (see ackDelayMs /
	// rackThreshold): under load the queue, not the wire, dominates feedback
	// delay, and only this sample sees it.
	m.retxBuf.onAckDelay = m.recordAckDelay
	m.retxBuf.onAckDelayTp = m.recordAckDelayTp
	// Default every mux to ECF (Earliest Completion First). It only spills a
	// frame onto a slower leg once the fastest leg is saturated (a full BDP in
	// flight), so it never over-assigns a slow leg and stalls the no-skip reorder
	// frontier — the failure mode of the old latency-weighted default, where a
	// low-latency but low-bandwidth leg drew traffic it couldn't clear and the
	// whole mux collapsed BELOW single-leg rate. Cold/empty ECF state and
	// single-leg groups fall back to the round-robin schedule in SelectECF, so
	// they are unaffected. A routing-policy distribution can still override this.
	m.tpSelector.SetMode(WeightModeECF)
	return m
}

// selectTransport picks the next transport/rule pair for sending.
// Uses latency-weighted selection when data is available, falls back to round-robin.
// Returns the index in tps[] alongside the tp/rule so the caller can
// record per-leg byte counts after a successful write.
//
// payload is the upcoming packet's payload bytes (or nil for
// handshake / control / retx paths that don't have a meaningful
// payload). Used by WeightModeSizeThreshold, WeightModeSticky5Tuple,
// WeightModeLatencyAdaptive, and WeightModeDSCPPriority — they
// inspect the payload directly. Other modes ignore it and fall
// back to the schedule-based pick.
//
// NOTE: not thread-safe, caller must hold the RouteGroup mu.
// selectTransport picks the leg for the next data frame, then applies the
// FEC-block striping cap so no leg exceeds fecDefaultR frames per K-frame block.
// The cap is a thin, best-effort post-pass over selectTransportRaw: it never fails
// a send (if every alive-ready leg is already block-full it keeps the raw pick),
// and it is inert unless FEC is negotiated on a multi-leg group — so the
// pre-integration selection is preserved byte-for-byte when fecEnabled=false.
func (m *routeMux) selectTransport(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	tp, rule, idx, err := m.selectTransportRaw(tps, fwd, payload)
	if err != nil || idx < 0 {
		return tp, rule, idx, err
	}
	if alt, ok := m.fecStripeReassign(tps, idx); ok && alt < len(fwd) {
		tp, rule, idx = tps[alt], fwd[alt], alt
	}
	m.fecStripeUse(idx)
	return tp, rule, idx, nil
}

func (m *routeMux) selectTransportRaw(tps []*transport.ManagedTransport, fwd []routing.Rule, payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}

	// Unidirectional send selection (CapUniDir).
	// DIRECTION governs which CLASS of leg (direct vs multihop) carries this end's
	// traffic; WITHIN the class the initiator-mirrored ACTIVE set governs which
	// legs. selectByDirection prefers the active (mirrored) class legs — even a
	// mirrored-active reverse leg that never received inbound bulk (so its
	// readiness gate never fired) is preferred over the warm-standby reserve — so
	// the exit's download fan-out stays bounded to the few reverse legs the
	// initiator parked active instead of spraying every warm-standby reverse leg
	// (which over-subscribes the no-skip reorder frontier and wedges the group →
	// the observed collapse-to-0). It returns ok=true (and we use its pick) as long
	// as ANY leg of the wanted class exists, so the download is NEVER handed to the
	// wrong-direction direct leg while a reverse leg is available. Only when there
	// is genuinely no reverse leg does it return ok=false and selection falls
	// through to the standard path.
	if directional, wantDirect, dstPK, srcPK := m.dirConfig(); directional {
		// The FORWARD direction (client -> exit: uploads and requests) rides ONE
		// leg. Which leg is selectConfinedForward's decision — the direct leg when
		// the group has one, else the lowest-latency leg — but THAT it is one leg
		// is decided here, by this end's ROLE, not by the direction->class mapping
		// the flip controller maintains. Reading the class instead was the bug:
		// when the flip controller moved the forward direction onto the multihop
		// class (a sustained upload flips it, and legs-2 has no direct leg for the
		// light direction to sit on), selectByDirection's tier 1 matched BOTH
		// multihop legs and handed the upload straight to the ECF scheduler. That
		// is the measured 79 % / 21 % split across a 44 ms and a 166 ms leg, and
		// the 6 flips in ten rows behind it.
		if m.forwardSender() {
			if tp, rule, idx, ok := m.selectConfinedForward(tps, fwd, wantDirect, dstPK, srcPK); ok {
				return tp, rule, idx, nil
			}
		} else if tp, rule, idx, ok := m.selectByDirection(tps, fwd, wantDirect, dstPK, srcPK); ok {
			// REVERSE (exit -> client: downloads) keeps its fan-out untouched.
			return tp, rule, idx, nil
		}
		// The REVERSE direction with no leg of its class left falls through to the
		// standard path below — there is genuinely nothing to confine a download
		// to, and spreading it is what the reverse direction is meant to do. The
		// FORWARD direction never reaches here: selectConfinedForward widens its
		// own candidate set (class-matching legs first, then any live leg, then
		// any ruled leg) rather than handing the upload to the scheduler.
	}

	// Payload-inspecting modes: ask the selector for a leg
	// derived from the bytes. Empty payload (handshake / retx)
	// falls through to the schedule-based pick.
	if m.tpSelector != nil && len(payload) > 0 {
		switch m.tpSelector.Mode() {
		case WeightModeSizeThreshold,
			WeightModeSticky5Tuple,
			WeightModeLatencyAdaptive,
			WeightModeDSCPPriority,
			WeightModeECF,
			WeightModeOTIAS,
			WeightModeSTMS:
			m.feedInflight(tps)
			idx := m.tpSelector.SelectForPayload(payload)
			// A leg ruled probe-only that has spent its budget declines the
			// frame here and the weighted path below re-homes it; every other
			// leg answers false, so a group of comparable legs is unchanged.
			if idx < len(tps) && !m.legProbeExhausted(idx) {
				tp := tps[idx]
				if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
					return tp, fwd[idx], idx, nil
				}
			}
		}
	}

	// Use weighted selector if available
	if m.tpSelector != nil && m.tpSelector.Len() > 0 {
		idx := m.tpSelector.Select()
		// A weighted pick that lands on a leg at its send window moves to a
		// leg with room, so the window bounds every mode, not only the
		// predictive ones that consult saturation themselves.
		if m.retxBuf != nil && m.sackEnabled {
			m.feedInflight(tps)
			if m.tpSelector.Saturated(idx) {
				if alt := m.tpSelector.FirstUnsaturated(); alt >= 0 {
					idx = alt
				}
			}
		}
		// …and a pick that lands on a leg out of probe budget moves to one that
		// still has a share, so a leg whose delay basis is a multiple of its
		// sibling's cannot take every other frame.
		if m.legProbeExhausted(idx) {
			if alt := m.firstProbeReadyLeg(tps); alt >= 0 {
				idx = alt
			}
		}
		if idx < len(tps) {
			tp := tps[idx]
			if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
				return tp, fwd[idx], idx, nil
			}
		}
	}

	// Fallback: round-robin with skip-dead and skip-not-ready. An aux leg
	// the peer has not confirmed yet is skipped so we never send the first
	// packets onto a route whose rule the peer has not registered; the
	// primary leg (0) is always ready, so this loop always finds it. When
	// direction filtering is active, non-matching legs are skipped here too.
	n := uint32(len(tps)) //nolint:gosec
	start := atomic.AddUint32(&m.tpIndex, 1) - 1
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legReadyAt(idx) {
			return tp, fwd[idx], idx, nil
		}
	}

	// EMERGENCY FAILOVER: no ACTIVE leg is selectable — every active leg is
	// dead/not-ready. Rather than fail the send (a dead connection), fall through
	// to any alive, ready WARM-STANDBY leg. The 512-deep standby reserve exists
	// precisely so the connection survives the instant its active set is lost,
	// with ZERO promote latency — a parked leg keeps its rules installed and its
	// transport alive, so it can carry a packet immediately. This is what makes
	// the warm reserve a real "switch in at a moment's notice" pool instead of
	// something that only helps on the next 20s rotation tick. The leg-death
	// trigger + rotation tick restore a proper active set right after; this just
	// guarantees no gap. legSelectableIgnoringStandby is legReadyAt WITHOUT the
	// standby exclusion (a parked leg that was active is ready), so it never
	// picks a leg the peer has not confirmed.
	for i := uint32(0); i < n; i++ {
		idx := int((start + i) % n) //nolint:gosec
		tp := tps[idx]
		if tp != nil && !tp.IsClosed() && m.legSelectableIgnoringStandby(idx) {
			return tp, fwd[idx], idx, nil
		}
	}
	return nil, nil, -1, ErrNoSuitableTransport
}

// selectFastestTransport picks the live, ready, non-standby leg with the LOWEST
// measured latency — the pick for the RETRANSMIT path, independent of the
// group's configured distribution mode.
//
// A retransmitted segment is one the receiver's reorder buffer is head-of-line
// blocked on: every later segment that already arrived on a fast leg is being
// withheld until this gap fills. Healing that gap on the FASTEST leg (rather
// than the normal spray/weight pick, which might re-send it down the very slow
// leg that stalled it) advances the delivery window in one fast RTT instead of
// waiting out the reorder timeout. This is what keeps a slow leg from dragging
// the whole stream: it still carries its share of new data, but its stragglers
// are rescued on a fast path. Falls back to the first ready leg when no leg has
// a latency measurement yet.
func (m *routeMux) selectFastestTransport(tps []*transport.ManagedTransport, fwd []routing.Rule) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	bestIdx, firstReady := -1, -1
	bestLat := -1.0
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		if firstReady < 0 {
			firstReady = idx
		}
		lat := tp.GetLatency()
		if lat <= 0 {
			continue // unknown latency — only a last resort
		}
		if bestLat < 0 || lat < bestLat {
			bestLat, bestIdx = lat, idx
		}
	}
	if bestIdx < 0 {
		bestIdx = firstReady
	}
	if bestIdx < 0 {
		return nil, nil, -1, ErrNoSuitableTransport
	}
	return tps[bestIdx], fwd[bestIdx], bestIdx, nil
}

// SetLegLatencyFn wires the per-leg END-TO-END route latency lookup (ms by
// transport id; 0 = unmeasured). Called once by the route group when the mux is
// built. Nil-safe: a mux without it simply has no end-to-end basis and falls
// back to the first-hop transport RTT.
func (m *routeMux) SetLegLatencyFn(fn func(uuid.UUID) float64) { m.legLatencyFn = fn }

// forwardSender reports whether THIS end sends the FORWARD direction of the
// route group — client → exit: uploads and requests. That is the INITIATOR's
// send, always: the flip controller moves which CLASS of leg each direction
// prefers, never which end is the client. Reading the class mapping here
// instead of the role is what let a sustained upload flip the forward direction
// onto the multihop class and straight into the ECF scheduler.
func (m *routeMux) forwardSender() bool {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return m.directional && m.initiator
}

// SetForwardRehomeFn wires the callback the mux fires when the forward
// direction moves to a different leg, so the route group can record a
// forward_rehomed mux event. Called once by the route group when the mux is
// built; the callback runs under the route group's mu.
func (m *routeMux) SetForwardRehomeFn(fn func(prev, next int, tp *transport.ManagedTransport, legs int, reason string)) {
	m.onForwardRehome = fn
}

// confinedForwardIdx is the leg the forward direction is currently confined to,
// or -1 when none is held. Lock-free, for readers outside the route group's mu
// (waitSendWindow runs on the writer with rg.mu dropped).
func (m *routeMux) confinedForwardIdx() int { return int(m.confinedFwdCur.Load()) }

// selectConfinedForward is the FORWARD direction's whole send decision: one leg,
// every frame. The leg is confinedForwardLeg's pick; the only thing that may
// move a frame off it is the --forward-spill knob, which is OFF by default.
//
// With spill off, a frame that arrives while the confined leg is at its send
// window is not re-homed onto another leg — the writer WAITS for the window
// (waitSendWindow, bounded by --send-window-wait-max). That is the correct
// trade: the alternative is a 10 MB upload split across a 44 ms and a 166 ms
// leg, and every such row measured on the live rig collapsed (x0.68 at 10 MB,
// x0.24 at 50 MB) because the peer's no-skip reorder frontier waits out the
// skew. Spilling is kept as a knob because it is what the code did before, not
// because it is the better default.
func (m *routeMux) selectConfinedForward(tps []*transport.ManagedTransport, fwd []routing.Rule,
	wantDirect bool, dst, src cipher.PubKey) (*transport.ManagedTransport, routing.Rule, int, bool) {
	idx := m.confinedForwardLeg(tps, wantDirect, dst, src)
	if idx < 0 || idx >= len(fwd) || idx >= len(tps) {
		return nil, nil, -1, false
	}
	if ForwardSpill() && m.tpSelector != nil && m.retxBuf != nil && m.sackEnabled {
		m.feedInflight(tps)
		if m.tpSelector.Saturated(idx) {
			if alt := m.tpSelector.FirstUnsaturated(); alt >= 0 && alt < len(fwd) && alt < len(tps) {
				if tp := tps[alt]; tp != nil && !tp.IsClosed() && m.legReadyAt(alt) {
					return tp, fwd[alt], alt, true
				}
			}
		}
	}
	return tps[idx], fwd[idx], idx, true
}

// forwardCandidates lists the legs the forward direction may be confined to, in
// three widening passes: the legs of the wanted CLASS that are live, ready and
// active; then any live, ready, active leg (a multihop-only group has no direct
// leg to sit on, which is the legs-2 shape); then any live leg with a rule at
// all, so a group whose whole active set was just parked still sends.
func (m *routeMux) forwardCandidates(tps []*transport.ManagedTransport, wantDirect bool, dst, src cipher.PubKey) []int {
	live := func(idx int) bool {
		tp := tps[idx]
		return tp != nil && !tp.IsClosed()
	}
	for _, match := range []func(int) bool{
		func(idx int) bool {
			return live(idx) && legIsDirect(tps[idx], dst, src) == wantDirect && m.legReadyAt(idx)
		},
		func(idx int) bool { return live(idx) && m.legReadyAt(idx) },
		func(idx int) bool { return live(idx) && m.legSelectableIgnoringStandby(idx) },
	} {
		cand := make([]int, 0, len(tps))
		for idx := range tps {
			if match(idx) {
				cand = append(cand, idx)
			}
		}
		if len(cand) > 0 {
			return cand
		}
	}
	return nil
}

// forwardLatencies measures every candidate on ONE comparable basis: the
// END-TO-END route latency (all hops, from the leg-liveness pong) when every
// candidate has a sample — the number that actually describes a multihop leg —
// else the FIRST-HOP transport RTT when every candidate has one. The two are
// never mixed across legs: they are different quantities, and comparing them
// would hand the direction to whichever leg happened to lack a pong. ok=false
// means "not comparably measured", and the caller keeps the lowest-indexed
// candidate (leg 0 in practice) exactly as an unmeasured group did before.
func (m *routeMux) forwardLatencies(tps []*transport.ManagedTransport, cand []int) ([]float64, bool) {
	lat := make([]float64, len(cand))
	if m.legLatencyFn != nil {
		ok := true
		for i, idx := range cand {
			ms := m.legLatencyFn(tps[idx].Entry.ID)
			if ms <= 0 {
				ok = false
				break
			}
			lat[i] = ms
		}
		if ok {
			return lat, true
		}
	}
	for i, idx := range cand {
		ms := tps[idx].GetLatency()
		if ms <= 0 {
			return nil, false
		}
		lat[i] = ms
	}
	return lat, true
}

// confinedForwardLeg picks the single leg the FORWARD direction is confined to.
//
// The confinement itself is not in question — spraying an upload across every
// forward leg over-subscribes the no-skip reorder frontier and was measured at
// 67-172 MB sent for a 10 MB upload. WHICH leg is decided here: the lowest
// measured latency among the candidates (forwardCandidates, forwardLatencies),
// which for a group that has a direct leg IS the direct leg, since the
// class-matching pass yields it alone.
//
// Two things keep the pick still. The decision is cached for
// confinedForwardRefresh, so a burst of thousands of frames re-measures a
// handful of times. And moving off the leg the direction already holds needs a
// challenger at least ForwardSwitchMargin LOWER for forwardSwitchSamples
// CONSECUTIVE refreshes — a single loaded sample cannot take the upload away,
// which is what produced 6 forward flips in ten live rows and split every one
// of them across a 44 ms and a 166 ms leg.
//
// The incumbent is dropped without hysteresis only when it is no longer a
// candidate at all: dead, parked to standby, or outclassed by a direct leg that
// has just joined. That is a REHOME, and it is reported through onForwardRehome
// so `visor state --select diag` says which leg took the direction and why.
//
// Caller holds the route group's mu (as for selectTransportRaw).
func (m *routeMux) confinedForwardLeg(tps []*transport.ManagedTransport, wantDirect bool, dst, src cipher.PubKey) int {
	now := time.Now().UnixNano()
	if m.confinedFwdIdx >= 0 && m.confinedFwdIdx < len(tps) &&
		now-m.confinedFwdAtNano < int64(confinedForwardRefresh) {
		if tp := tps[m.confinedFwdIdx]; tp != nil && !tp.IsClosed() && m.legReadyAt(m.confinedFwdIdx) {
			return m.confinedFwdIdx
		}
	}

	cand := m.forwardCandidates(tps, wantDirect, dst, src)
	if len(cand) == 0 {
		return -1
	}
	lat, measured := m.forwardLatencies(tps, cand)

	prev := m.confinedFwdIdx
	// Is the leg the direction already holds still eligible?
	held := -1
	for i, idx := range cand {
		if idx == prev {
			held = i
			break
		}
	}

	best, bestAt := cand[0], 0
	if measured {
		for i := range cand {
			if lat[i] < lat[bestAt] {
				best, bestAt = cand[i], i
			}
		}
	} else if held >= 0 {
		// Not comparably measured: never move a direction that is already placed.
		best, bestAt = prev, held
	}

	// An incumbent placed before any leg was comparably measured is a
	// PLACEHOLDER (leg 0, the leg that happened to be added first), not a
	// decision — the first real measurement replaces it outright, with no
	// hysteresis to clear and no rehome to report.
	placeholder := held >= 0 && m.confinedFwdLatMs <= 0

	reason := ""
	switch {
	case placeholder:
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	case held < 0 && prev >= 0:
		reason = fmt.Sprintf("leg %d is no longer selectable (dead, parked to standby, or a direct leg joined) — rehomed to leg %d", prev, best)
	case held >= 0 && best != prev && measured:
		// Hysteresis: the challenger must clear the margin on consecutive samples.
		margin := ForwardSwitchMargin()
		if lat[bestAt] > lat[held]*(1-margin) {
			m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
			best, bestAt = prev, held
			break
		}
		if m.confinedFwdChalIdx == best {
			m.confinedFwdChalHits++
		} else {
			m.confinedFwdChalIdx, m.confinedFwdChalHits = best, 1
		}
		if m.confinedFwdChalHits < m.knInt(routersettings.ForwardSwitchSamples) {
			best, bestAt = prev, held
			break
		}
		reason = fmt.Sprintf("leg %d measured %.0f ms against leg %d's %.0f ms (at least %.0f%% lower for %d consecutive samples)",
			best, lat[bestAt], prev, lat[held], margin*100, m.knInt(routersettings.ForwardSwitchSamples))
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	default:
		m.confinedFwdChalIdx, m.confinedFwdChalHits = -1, 0
	}

	at := now
	if measured {
		m.confinedFwdLatMs = lat[bestAt]
	} else {
		// Nothing was measured, so nothing was decided: hold the leg for this
		// frame but re-measure on the next one rather than sitting on a
		// placeholder for a whole refresh interval.
		m.confinedFwdLatMs, at = 0, 0
	}
	m.confinedFwdIdx, m.confinedFwdAtNano = best, at
	m.confinedFwdCur.Store(int64(best))
	if reason != "" && prev != best && m.onForwardRehome != nil {
		m.onForwardRehome(prev, best, tps[best], len(tps), reason)
	}
	return best
}

// growLegs extends the per-leg counter slice to cover at least n
// legs. Called when transports are appended to the rg (initial setup
// + AppendRoute). Idempotent — extending past the current size is a
// no-op for legs that already exist.
func (m *routeMux) growLegs(n int) {
	m.legMu.Lock()
	for len(m.legs) < n {
		m.legs = append(m.legs, &legCounters{})
	}
	for len(m.ready) < n {
		// The primary leg (index 0) is ready immediately; aux legs start
		// not-ready and are marked ready on the first inbound packet.
		m.ready = append(m.ready, len(m.ready) == 0)
	}
	for len(m.standby) < n {
		// The primary leg (index 0, the first append) is always active. Aux
		// legs enter warm standby when standbyNewLegs is set (a promoting
		// rotation engine is wired), so the engine promotes them one per tick
		// as its goodput signal warrants instead of all going hot at once.
		m.standby = append(m.standby, m.standbyNewLegs && len(m.standby) > 0)
	}
	m.legMu.Unlock()
}

// SetStandbyNewLegs controls whether newly-grown aux legs enter warm standby on
// add (see the standbyNewLegs field). Called by the route group when a promoting
// rotation engine is wired, before aux legs are appended. Idempotent.
func (m *routeMux) SetStandbyNewLegs(v bool) {
	m.legMu.Lock()
	m.standbyNewLegs = v
	m.legMu.Unlock()
}

// SetLegGroups records the per-leg shared-bottleneck group ids (see the
// legGroups field). The slice is copied and is parallel to legs[] in the rg's
// tps[] order; a shorter slice leaves the trailing legs as singletons. Cheap and
// idempotent — called each data-progress tick from the route group after it
// recomputes groups from per-leg OWD statistics.
func (m *routeMux) SetLegGroups(groups []int) {
	m.legMu.Lock()
	m.legGroups = append([]int(nil), groups...)
	m.legMu.Unlock()
}

// groupOf returns leg i's shared-bottleneck group id, defaulting to i itself
// (its own singleton group) when no grouping is recorded for it. Caller holds
// legMu.
func (m *routeMux) groupOf(i int) int {
	if i < len(m.legGroups) {
		return m.legGroups[i]
	}
	return i
}

// removeLegs drops the given ORIGINAL leg indices from legs[] and ready[] so
// they stay aligned with the rg's compacted tps[]/fwd[]/rvs[] after a leg is
// removed (RemoveMuxRouteByTransport / pruneDeadTransports). It rebuilds both
// slices skipping the dropped indices (order-independent), then re-asserts the
// leg-0-always-ready invariant — leg 0 may have been promoted from an aux when
// a primary transport is pruned. Without this lockstep compaction the arrays
// desync from tps[]: readiness and per-leg accounting attach to the wrong leg,
// which can flip a live leg to not-ready (the mux>=2 hang — see the ready[]
// note above) or mis-attribute bytes after an index is reused. The counterpart
// to growLegs.
func (m *routeMux) removeLegs(indices ...int) {
	if len(indices) == 0 {
		return
	}
	drop := make(map[int]bool, len(indices))
	for _, i := range indices {
		drop[i] = true
	}
	m.legMu.Lock()
	if len(m.legs) > 0 {
		kept := make([]*legCounters, 0, len(m.legs))
		for i, c := range m.legs {
			if !drop[i] {
				kept = append(kept, c)
			}
		}
		m.legs = kept
	}
	if len(m.ready) > 0 {
		kept := make([]bool, 0, len(m.ready))
		for i, r := range m.ready {
			if !drop[i] {
				kept = append(kept, r)
			}
		}
		m.ready = kept
		if len(m.ready) > 0 {
			m.ready[0] = true // the (possibly newly-promoted) primary is always ready
		}
	}
	if len(m.standby) > 0 {
		kept := make([]bool, 0, len(m.standby))
		for i, s := range m.standby {
			if !drop[i] {
				kept = append(kept, s)
			}
		}
		m.standby = kept
		if len(m.standby) > 0 {
			m.standby[0] = false // the primary leg is never standby
		}
	}
	m.legMu.Unlock()
}

// markLegReady records that leg idx has carried inbound traffic, so it
// is now safe to select for sending. Idempotent and bounds-checked.
func (m *routeMux) markLegReady(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.Lock()
	if idx < len(m.ready) {
		m.ready[idx] = true
	}
	m.legMu.Unlock()
}

// legReadyAt reports whether leg idx may be selected for sending.
// Out-of-range indices and the never-grown case report not-ready, except
// the primary leg (0) which is always ready so a group with no readiness
// info still sends on its primary route.
func (m *routeMux) legReadyAt(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	// A warm-standby leg is never selected for sending, regardless of
	// readiness — its rules stay installed but it carries no forward traffic.
	if idx < len(m.standby) && m.standby[idx] {
		return false
	}
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// legSelectableIgnoringStandby reports whether leg idx may carry a packet as an
// EMERGENCY FAILOVER target — the same readiness gate as legReadyAt but WITHOUT
// the warm-standby exclusion. A parked leg keeps its rules installed and its
// transport alive, and was ready (peer-confirmed) before it was parked, so it
// can carry traffic the instant no active leg is available. selectTransport uses
// this only as a last resort, after every active leg has been found dead/not-
// ready, so the connection never dies while ANY leg in the group is alive.
func (m *routeMux) legSelectableIgnoringStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.ready) {
		return idx == 0
	}
	return m.ready[idx]
}

// setLegStandby marks (or clears) leg idx as a warm standby: kept alive but
// not selected for sending. Bounds-checked; the primary leg (0) cannot be put
// on standby (a group must always have a selectable send leg). Clearing the
// flag PROMOTES the leg back to active instantly, with no route setup.
func (m *routeMux) setLegStandby(idx int, standby bool) {
	if idx <= 0 {
		return // leg 0 is never standby
	}
	m.legMu.Lock()
	// The standby slice grows lazily as legs are selected, so a leg appended a
	// moment ago may have no slot yet — a bounded write then silently dropped an
	// explicit promotion (the operator-pinned second leg of a two-leg set stayed
	// parked). Grow to cover idx, filling the gap with the add-time default.
	for len(m.standby) <= idx {
		m.standby = append(m.standby, m.standbyNewLegs && len(m.standby) > 0)
	}
	m.standby[idx] = standby
	m.legMu.Unlock()
	m.signalWindow()
}

// parkAllAuxStandby marks every aux leg (index > 0) as warm standby. Used by the
// acceptor when leg-state signaling is negotiated so its active set starts EMPTY
// and is filled only by the initiator's mirror promotes — the acceptor must never
// send the bulk direction across a leg the initiator hasn't activated. Leg 0 (the
// primary) is left active.
func (m *routeMux) parkAllAuxStandby() {
	m.legMu.Lock()
	for i := 1; i < len(m.standby); i++ {
		m.standby[i] = true
	}
	m.legMu.Unlock()
}

// swapLegs exchanges the mux's per-leg state (counters, readiness, standby
// marker) between two leg indices so the primary slot (0) can be RE-ELECTED
// onto a healthier leg without tearing down any route. The caller (RouteGroup.
// reelectPrimary) swaps the parallel tps[]/fwd[]/rvs[] entries in the same
// critical section, so leg accounting and rules stay attached to their own
// transport across the swap. After the swap the new primary (whatever leg landed
// at index 0) is forced ready and out of standby — it is chosen from the active,
// carrying set, so this is belt-and-suspenders, not a state change. Bounds-
// checked; a no-op if either index is out of range or i == j.
func (m *routeMux) swapLegs(i, j int) {
	if i == j || i < 0 || j < 0 {
		return
	}
	m.legMu.Lock()
	defer m.legMu.Unlock()
	if i < len(m.legs) && j < len(m.legs) {
		m.legs[i], m.legs[j] = m.legs[j], m.legs[i]
	}
	if i < len(m.ready) && j < len(m.ready) {
		m.ready[i], m.ready[j] = m.ready[j], m.ready[i]
	}
	if i < len(m.standby) && j < len(m.standby) {
		m.standby[i], m.standby[j] = m.standby[j], m.standby[i]
	}
	// Re-assert the leg-0 invariants: the primary is always ready and never
	// standby. (The re-elected leg was active and carrying, so this only guards
	// against a stale flag.)
	if len(m.ready) > 0 {
		m.ready[0] = true
	}
	if len(m.standby) > 0 {
		m.standby[0] = false
	}
}

// activeLegCount reports how many legs are currently active (not warm standby)
// — the striped set width. Used for wedge/hold diagnostics.
func (m *routeMux) activeLegCount() int {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if len(m.standby) == 0 {
		return len(m.legs)
	}
	active := 0
	for _, s := range m.standby {
		if !s {
			active++
		}
	}
	return active
}

// isLegStandby reports whether leg idx is a warm standby. Bounds-checked.
func (m *routeMux) isLegStandby(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	return idx < len(m.standby) && m.standby[idx]
}

// recordSent atomically increments the sent-bytes/packets counters
// for leg idx. Bounds-checked; out-of-range indices are silently
// dropped (defensive: a leg can be removed between selectTransport
// and the actual write returning).
func (m *routeMux) recordSent(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].sentBytes, n)
		atomic.AddUint64(&m.legs[idx].sentPackets, 1)
		// The probe budget is charged from what actually went on the wire, so a
		// leg ruled probe-only carries one probe per window however large the
		// frames are (the gate's own charge would have to guess the size).
		atomic.AddUint64(&m.legs[idx].probeWinBytes, n)
	}
	m.legMu.RUnlock()
}

// recordRecv atomically increments the recv-bytes/packets counters
// for leg idx. Same bounds-check semantics as recordSent.
func (m *routeMux) recordRecv(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].recvBytes, n)
		atomic.AddUint64(&m.legs[idx].recvPackets, 1)
	}
	m.legMu.RUnlock()
}

// recordPayload atomically credits leg idx with n bytes of UNIQUE in-order
// payload (a seq's first arrival). Same bounds-check semantics as recordRecv.
func (m *routeMux) recordPayload(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].payloadBytes, n)
	}
	m.legMu.RUnlock()
}

// recordDup atomically credits leg idx with n bytes of DUPLICATE data (a seq
// that had already been delivered or buffered when it arrived on this leg).
// Splitting dupBytes out of recvBytes is what attributes a "standby leg with
// traffic" honestly: the peer's spurious retransmits ride the fastest leg
// (resendSeqs → selectFastestTransport), which is typically the parked direct
// leg, and without this counter that flow is indistinguishable from striped
// payload. Same bounds-check semantics as recordRecv.
func (m *routeMux) recordDup(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].dupBytes, n)
	}
	m.legMu.RUnlock()
}

// recordRepair atomically credits leg idx with n bytes of FEC repair frames.
// Repairs are overhead by design (they buy gap-fill latency); counting them
// per leg separates that deliberate cost from spurious-retransmit waste.
func (m *routeMux) recordRepair(idx int, n uint64) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].repairBytes, n)
	}
	m.legMu.RUnlock()
}

// recordRetransmit atomically increments the retransmit counter for leg
// idx (the leg that carried a SACK retransmit). The retransmitted bytes
// are still recorded via recordSent; this is the separate loss signal.
func (m *routeMux) recordRetransmit(idx int) {
	if idx < 0 {
		return
	}
	m.legMu.RLock()
	if idx < len(m.legs) {
		atomic.AddUint64(&m.legs[idx].retransmits, 1)
	}
	m.legMu.RUnlock()
}

// retransmitsAt returns leg idx's cumulative retransmit count (0 if out of
// range), for snapshotLegs / LegInfo without a full Snapshot allocation.
func (m *routeMux) retransmitsAt(idx int) uint64 {
	if idx < 0 {
		return 0
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx < len(m.legs) {
		return atomic.LoadUint64(&m.legs[idx].retransmits)
	}
	return 0
}

// snapshotLegs returns a stable copy of the current per-leg counters and, as a
// side effect, samples each leg's goodput RATE (bytes/sec EWMA) over the window
// since the previous snapshot. The byte/packet counters are point-in-time
// atomic loads; the rate is maintained under legMu (a write lock) so concurrent
// observers don't corrupt the per-leg sample state. Called at telemetry/UI
// cadence (the status page's ~1s push, CLI mux-info), never on the data path.
func (m *routeMux) snapshotLegs() []LegStats {
	now := time.Now().UnixNano()
	m.legMu.Lock()
	out := make([]LegStats, len(m.legs))
	for i, c := range m.legs {
		sent := atomic.LoadUint64(&c.sentBytes)
		recv := atomic.LoadUint64(&c.recvBytes)
		m.sampleGoodput(c, sent, recv, now)
		out[i] = LegStats{
			Index:          i,
			SentBytes:      sent,
			SentPackets:    atomic.LoadUint64(&c.sentPackets),
			RecvBytes:      recv,
			RecvPackets:    atomic.LoadUint64(&c.recvPackets),
			PayloadBytes:   atomic.LoadUint64(&c.payloadBytes),
			DupBytes:       atomic.LoadUint64(&c.dupBytes),
			RepairBytes:    atomic.LoadUint64(&c.repairBytes),
			Retransmits:    atomic.LoadUint64(&c.retransmits),
			GoodputUpBps:   c.goodputUpBps,
			GoodputDownBps: c.goodputDownBps,
			GoodputBps:     c.goodputUpBps + c.goodputDownBps,
		}
	}
	m.legMu.Unlock()
	return out
}

// sampleGoodput updates leg c's per-direction goodput EWMAs from the sent and
// recv byte counters observed at wall-clock now (UnixNano). Caller holds legMu
// for writing. The first observation only seeds the baseline (no rate emitted).
// To keep the metric stable when several observers interleave, samples closer
// together than goodputMinSampleNano are skipped and the stored rates are left
// unchanged.
func (m *routeMux) sampleGoodput(c *legCounters, sent, recv uint64, now int64) {
	if c.lastRateNano == 0 {
		c.lastRateSentBytes = sent
		c.lastRateRecvBytes = recv
		c.lastRateNano = now
		return
	}
	elapsed := now - c.lastRateNano
	if elapsed < goodputMinSampleNano {
		return
	}
	secs := float64(elapsed) / float64(time.Second)
	c.goodputUpBps = ewmaRate(c.goodputUpBps, byteDelta(sent, c.lastRateSentBytes), secs)
	c.goodputDownBps = ewmaRate(c.goodputDownBps, byteDelta(recv, c.lastRateRecvBytes), secs)
	c.lastRateSentBytes = sent
	c.lastRateRecvBytes = recv
	c.lastRateNano = now
}

// byteDelta is cur-prev, clamped at 0 so a counter reset (route rebuild) yields
// no negative rate.
func byteDelta(cur, prev uint64) uint64 {
	if cur >= prev {
		return cur - prev
	}
	return 0
}

// ewmaRate folds a byte delta over secs seconds into the running bytes/sec EWMA
// (goodputEWMAAlpha weights the newest sample). A zero prior seeds directly.
func ewmaRate(prev float64, delta uint64, secs float64) float64 {
	sample := float64(delta) / secs
	if prev == 0 {
		return sample
	}
	return goodputEWMAAlpha*sample + (1-goodputEWMAAlpha)*prev
}

// perFrameSealOverhead is the AEAD tag the per-frame seal appends to every mux
// frame. Both noise cipher suites in use (AES-GCM, ChaCha20-Poly1305) carry a
// 16-byte tag.
const perFrameSealOverhead = 16

// frameOverhead is what a sequenced data frame spends on top of the application
// payload: the sequence number, plus the AEAD tag once per-frame noise is wired.
func (m *routeMux) frameOverhead() int {
	overhead := routing.SeqSize
	if m.seal != nil {
		overhead += perFrameSealOverhead
	}
	return overhead
}

// wrapPayload creates a sequenced data packet and optionally stores it for
// retransmission, tagged with the TRANSPORT UUID of the leg it is about to be
// sent on (uuid.Nil = unknown) so the demote-time flush can target only the
// stranded leg's sequences. The tag is the transport identity, not the leg
// index — indices shift on slice compaction.
// Returns the packet and the sequence number used.
func (m *routeMux) wrapPayload(routeID routing.RouteID, data []byte, tpID uuid.UUID) (routing.Packet, uint32, error) {
	// Reject an oversized frame BEFORE taking a sequence number. A seq consumed by
	// a frame that never goes out is a permanent hole: the receiver's no-skip
	// reorder buffer waits on it forever. RouteGroup.Write segments to
	// maxWritePayload so this is a guard, not a path.
	if len(data)+m.frameOverhead() > math.MaxUint16 {
		return nil, 0, routing.ErrPayloadTooBig
	}
	seq := atomic.AddUint32(&m.writeSeq, 1) - 1
	// Per-frame AEAD: seal the app payload under seq as the nonce. The sealed
	// bytes are what go on the wire AND into the retx buffer, so a SACK
	// retransmit resends the identical sealed frame (same seq ⇒ same nonce ⇒
	// same ciphertext), and the receiver opens it independently, out of order.
	if m.seal != nil {
		data = m.seal(seq, data)
	}
	packet, err := routing.MakeSequencedDataPacket(routeID, seq, data)
	if err != nil {
		return nil, 0, err
	}

	// Store for retransmission before sending
	if m.sackEnabled && m.retxBuf != nil {
		m.retxBuf.Store(seq, data, tpID) //nolint:errcheck
	}

	// FEC: feed the on-wire (post-seal) payload to the striper. A completed block
	// queues R repair frames for the send loop (RouteGroup.write) to schedule on a
	// fast leg. Inert unless CapFEC was negotiated.
	m.fecOnSend(seq, data)

	// Stamp the TLP idle timer: new data just went out, so the tail-loss probe
	// clock restarts. A probe only fires once this stays quiet for a PTO.
	atomic.StoreInt64(&m.lastSendNano, time.Now().UnixNano())

	return packet, seq, nil
}

// deliverData inserts a received sequenced packet into the reorder buffer
// and returns any payloads that are now deliverable in order.
// Also tracks the sequence for SACK generation.
//
// leg is the arrival leg index (the transport this frame came in on). A seq seen
// here for the FIRST time credits that leg's payloadBytes with the frame's app
// payload — so per-leg payloadBytes attributes the transfer's unique payload to
// the legs that actually carried it, retransmits/duplicates excluded. Pass a
// negative leg to skip attribution (callers/tests without a leg context).
func (m *routeMux) deliverData(leg int, seq uint32, data []byte) (delivered [][]byte, gapDetected bool) {
	// FEC: record the on-wire (pre-open) payload so a sibling in this block can be
	// reconstructed if it is late/lost on a slow leg. Inert unless CapFEC
	// negotiated. Must run BEFORE open — reconstruction reproduces the on-wire
	// frame, which is then opened via the same path below.
	m.fecOnRecvData(seq, data)

	// Per-frame AEAD: open the frame under its sequence-nonce before it enters
	// the reorder buffer. A frame that fails to open (tamper, or a stale
	// duplicate whose seq the peer reused after a rekey) is dropped, exactly as
	// a corrupt packet would be — never delivered. SACK/reorder then treat it as
	// not-yet-received and it is retransmitted if genuinely missing.
	if m.open != nil {
		pt, err := m.open(seq, data)
		if err != nil {
			if m.logger != nil {
				m.logger.WithError(err).Tracef("per-frame open failed for seq %d; dropping", seq)
			}
			return nil, false
		}
		data = pt
	}

	// Attribute UNIQUE payload to the arrival leg: credit this leg only the FIRST
	// time a seq arrives, so a retransmit of a seq already seen on another leg is
	// not double-counted and the per-leg payloadBytes sum equals the transfer
	// size. Already-delivered is seq < reorderBuf.NextSeq (no seq-0 ambiguity);
	// already-buffered-out-of-order is the received set. Checked BEFORE
	// RecordReceived/Insert record this seq.
	if leg >= 0 {
		isNew := true
		if m.reorderBuf != nil && seq < m.reorderBuf.NextSeq() {
			isNew = false // already delivered
		} else if m.sackTracker != nil && m.sackTracker.alreadyBuffered(seq) {
			isNew = false // already buffered out of order
		}
		if isNew {
			m.recordPayload(leg, uint64(len(data)))
		} else {
			m.recordDup(leg, uint64(len(data)))
		}
	}

	// Insert FIRST, then track for SACK generation — and only if the packet was
	// actually buffered. At maxGap the reorder buffer DROPS the packet; recording
	// it as received (the old order) made the next SACK set its bit, the sender
	// purged it from the retransmit buffer, and the no-skip frontier then wedged
	// forever on a sequence nobody could resend. A dropped seq stays unrecorded,
	// so the SACK reports it missing and the sender retransmits it.
	var dropped bool
	delivered, dropped = m.reorderBuf.InsertOrDrop(seq, data)
	if dropped {
		atomic.AddUint64(&m.reorderDrops, 1)
		if m.logger != nil {
			m.logger.Debugf("reorder buffer full: dropped seq %d (not SACKed, sender will retransmit)", seq)
		}
	}

	if m.sackEnabled && m.sackTracker != nil {
		if dropped {
			// The frontier is gap-blocked and this arrival was discarded: still ask
			// for a SACK so the sender resends the sequence the frontier waits on.
			gapDetected = true
		} else {
			gapDetected = m.sackTracker.RecordReceived(seq)
		}
	}

	// Sync SACK tracker with reorder buffer delivery state
	if m.sackEnabled && m.sackTracker != nil {
		m.sackTracker.AdvanceContiguous(m.reorderBuf.NextSeq())
	}

	// FEC: if the frontier is now gap-blocked but this frame completed a block's
	// K-of-(K+R) quorum, reconstruct the stuck frontier frame(s) and append them
	// to the delivered run so they reach the app in order — no wait on the slow
	// leg. Inert unless CapFEC negotiated.
	if m.fecEnabled {
		if extra := m.fecTryAdvance(); len(extra) > 0 {
			delivered = append(delivered, extra...)
		}
	}

	return delivered, gapDetected
}

// gapAge exposes the reorder buffer's current frontier-gap age (0 if the stream
// is contiguous). Used by the route group's fast data-progress prune.
func (m *routeMux) gapAge() time.Duration {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.GapAge()
}

// reorderPending reports how many packets are currently buffered out-of-order
// on the receive side (0 when the stream is contiguous). A climbing value while
// a gap stays open is the head-of-line-blocking signal for a stalled leg.
func (m *routeMux) reorderPending() int {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.Pending()
}

// reorderNextSeq returns the sequence number the receive-side reorder buffer is
// waiting on (the frontier). When a gap is stuck this is the missing seq whose
// leg has stalled — the key datum for diagnosing a reorder wedge.
func (m *routeMux) reorderNextSeq() uint32 {
	if m.reorderBuf == nil {
		return 0
	}
	return m.reorderBuf.NextSeq()
}

// writeSeqValue returns the count of DATA frames this mux has emitted (the next
// outgoing sequence number). A cheap aggregate outbound-progress counter.
func (m *routeMux) writeSeqValue() uint32 {
	return atomic.LoadUint32(&m.writeSeq)
}

// retxStats exposes the sender-side retx buffer's occupancy and the sequence
// range it still holds (0,0,0 when SACK/retx is not in play). The receiver
// naming a stuck frontier seq is only half a wedge diagnosis; this says whether
// the sender can still honor a retransmit request for it.
func (m *routeMux) retxStats() (held int, minSeq, maxSeq uint32) {
	if m.retxBuf == nil {
		return 0, 0, 0
	}
	return m.retxBuf.Stats()
}

// msSinceNano is the age in milliseconds of an atomic UnixNano stamp, or -1
// when the stamp was never set (never vs "just now" are opposite diagnoses for
// a SACK feedback path, so they must not both render as 0).
func msSinceNano(p *int64) float64 {
	nano := atomic.LoadInt64(p)
	if nano == 0 {
		return -1
	}
	return float64(time.Since(time.Unix(0, nano))) / float64(time.Millisecond)
}

// distributionMode returns the selector's current weight mode (how packets are
// spread across the legs). WeightModeAuto when the selector is absent.
func (m *routeMux) distributionMode() WeightMode {
	if m.tpSelector == nil {
		return WeightModeAuto
	}
	return m.tpSelector.Mode()
}

// sackMinInterval is the minimum spacing between receiver-side SACKs. It is
// well under retxMinAge so a genuine loss is still signaled several times
// before the sender's retransmit timer fires, while collapsing the flood of
// per-packet SACKs that latency-skew reordering would otherwise produce.
const sackMinIntervalDefault = 25 * time.Millisecond

// shouldSendSACK reports whether enough time has elapsed since the last SACK to
// send another, rate-limiting SACK feedback under heavy cross-leg reordering.
// Concurrency-safe: only the goroutine that wins the CAS returns true.
func (m *routeMux) shouldSendSACK() bool {
	now := time.Now().UnixNano()
	prev := atomic.LoadInt64(&m.lastSACKNano)
	if now-prev < int64(m.knDur(routersettings.SackMinInterval)) {
		return false
	}
	return atomic.CompareAndSwapInt64(&m.lastSACKNano, prev, now)
}

// shouldSendHolSACK reports whether enough time has elapsed since the last
// PROACTIVE HoL SACK to send another, rate-limited to about one fast-leg RTT
// (interval) so a persistent frontier stall re-nudges the sender roughly once
// per round-trip rather than on every arriving out-of-order packet. Uses its own
// holSACKNano clock, independent of the window-ack SACK limiter (shouldSendSACK),
// so the two never rate-limit each other out. Concurrency-safe: only the
// goroutine that wins the CAS returns true.
func (m *routeMux) shouldSendHolSACK(interval time.Duration) bool {
	now := time.Now().UnixNano()
	prev := atomic.LoadInt64(&m.holSACKNano)
	if now-prev < int64(interval) {
		return false
	}
	return atomic.CompareAndSwapInt64(&m.holSACKNano, prev, now)
}

// generateSACK returns the current SACK state for sending to the peer:
// the last contiguous sequence plus a full-window received bitmap.
func (m *routeMux) generateSACK() (lastContig uint32, words []uint64) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, nil
	}
	return m.sackTracker.GenerateSACK()
}

// processSACK processes a received SACK and returns sequences that need
// retransmission. Retained for callers/tests that don't carry the DSACK/ack-edge
// side effects; the live receive path uses onSACKReceived (see rack_tlp.go).
func (m *routeMux) processSACK(lastContig uint32, words []uint64) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.ProcessSACK(lastContig, words, m.rackThreshold())
}

// takeDSACK returns a pending DSACK sequence (a duplicate the receiver saw) to
// attach to the next outgoing SACK, clearing it so it is reported once. Returns
// (0, false) when SACK is off or no duplicate is pending.
func (m *routeMux) takeDSACK() (uint32, bool) {
	if !m.sackEnabled || m.sackTracker == nil {
		return 0, false
	}
	return m.sackTracker.takeDSACK()
}

// RACK-TLP retransmit-threshold bounds (RFC 8985 in spirit). Replaces the fixed
// 750ms retxMinAge with a value derived from the live per-leg RTTs, so loss on a
// fast path is recovered in tens of ms instead of always waiting ~750ms, while a
// genuinely slow leg still isn't declared lost prematurely.
const (
	rackReorderFactorDefault = 1.25                    // slow-leg RTT × this = the reordering tolerance
	rackFloorDefault         = 60 * time.Millisecond   // never retransmit sooner than this (anti-storm)
	rackCeilDefault          = 1500 * time.Millisecond // never wait longer than this
	rackDefaultNoRTTDefault  = 300 * time.Millisecond  // before any leg RTT is measured
)

// rackThreshold computes the current reorder-tolerant retransmit threshold from
// the live ACTIVE-leg RTTs: a sequence is presumed lost (not merely reordered on
// a slower leg) once it has been outstanding longer than this. The reordering
// window on a multi-leg mux IS the inter-leg RTT skew, so we bound the threshold
// at the SLOWEST active leg's RTT × rackReorderFactor — a frame striped onto any
// leg should arrive within the slow leg's RTT plus a jitter margin; past that it
// is lost. Adapts per-SACK to the measured path (RFC 8985's RTT-derived expiry),
// floored/capped to avoid a self-amplifying early-retransmit storm or an
// unbounded wait. Falls back to a conservative default until RTTs are known.
func (m *routeMux) rackThreshold() time.Duration {
	maxRtt := m.maxActiveLegRTTms()
	if maxRtt <= 0 {
		return m.knDur(routersettings.RackDefaultNoRTT)
	}
	// The reordering/feedback window is the LARGER of the idle-ping RTT and the
	// measured send→ack delay (ackDelayMs): a saturated leg's queue delays acks
	// by seconds while its ping RTT stays at wire level, and presuming loss
	// inside the real feedback delay is spurious by construction.
	if ad := m.ackDelayMs(); ad > maxRtt {
		maxRtt = ad
	}
	th := time.Duration(maxRtt*m.rackFactor()) * time.Millisecond
	if th < m.knDur(routersettings.RackFloor) {
		th = m.knDur(routersettings.RackFloor)
	}
	// The absolute ceiling bounds the wait for a genuine loss, but it must
	// never undercut one measured RTT: when queueing delay inflates the
	// slow-leg EWMA past rackCeil (bufferbloat under load can push it to many
	// seconds), presuming loss at a fixed 1.5s declares EVERY in-flight packet
	// lost forever — the observed retransmit storm, which the DSACK-widened
	// factor could not counter because this same clamp overrode it. Floor the
	// ceiling at one measured RTT: waiting less than one RTT for an ack is
	// definitionally spurious, and the wait stays bounded (max(rackCeil, RTT))
	// rather than unbounded.
	ceil := m.knDur(routersettings.RackCeil)
	if rttDur := time.Duration(maxRtt) * time.Millisecond; rttDur > ceil {
		ceil = rttDur
	}
	if th > ceil {
		th = ceil
	}
	return th
}

// recordAckDelay folds one measured send→ack delay sample into the ackDelay
// EWMA. Asymmetric on purpose: a sample ABOVE the running value takes it over
// half-way (α=0.5) so the threshold tracks a queue building in a few SACKs
// (each spurious retransmit before it catches up is pure waste), while a
// sample below decays it gently (α=0.125) so one lucky fast ack doesn't
// re-arm eager retransmission while the queue is still draining.
func (m *routeMux) recordAckDelay(d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	for {
		cur := atomic.LoadInt64(&m.ackDelayMilli)
		curMs := float64(cur) / 1000
		alpha := 0.125
		if ms > curMs {
			alpha = 0.5
		}
		next := int64((curMs + alpha*(ms-curMs)) * 1000)
		if atomic.CompareAndSwapInt64(&m.ackDelayMilli, cur, next) {
			return
		}
	}
}

// ackDelayMs returns the EWMA send→ack delay in milliseconds (0 = no sample).
func (m *routeMux) ackDelayMs() float64 {
	return float64(atomic.LoadInt64(&m.ackDelayMilli)) / 1000
}

// ackDelayEst is one leg's send→ack delay estimate: the asymmetric EWMA the
// loss detectors judge holes against, and when the last sample landed.
//
// The EWMA rises fast, falls slowly, and had no time decay at all, so the
// queue one transfer built was still the basis when the next one started:
// measured live 2026-09-17, five consecutive 50 MB uploads over ONE leg
// through a 470 ms intermediate ran 3.90, 3.05, 2.80, 1.59, 0.62 MB/s with
// ZERO loss (retx 6→28, send_window_waits 0), the leg's reported delay
// ratcheting to 13435 ms while sacks_recv per row went 125→476 at constant
// frames. lastNano is what ends that: the estimate expires with the transfer.
type ackDelayEst struct {
	ms       float64
	lastNano int64
}

// ackDelayStale is how long a leg's send→ack estimate survives without a new
// sample. Past it the leg reads as unsampled again — the first-hop transport
// latency is the basis until the next transfer proves otherwise — so an idle
// gap between transfers resets both the loss threshold and the window's
// feedback delay instead of carrying the previous transfer's queue into the
// next one.
const ackDelayStale = 5 * time.Second

// recordAckDelayTp folds one send→ack delay sample into the leg's own EWMA
// (same asymmetric α as recordAckDelay: fast up, slow down).
func (m *routeMux) recordAckDelayTp(tpID uuid.UUID, d time.Duration) {
	ms := float64(d) / float64(time.Millisecond)
	now := time.Now().UnixNano()
	m.ackDelayByTpMu.Lock()
	if m.ackDelayByTp == nil {
		m.ackDelayByTp = make(map[uuid.UUID]*ackDelayEst)
	}
	cur := m.ackDelayByTp[tpID]
	if cur == nil {
		cur = new(ackDelayEst)
		m.ackDelayByTp[tpID] = cur
	}
	if cur.ms == 0 || now-cur.lastNano > int64(ackDelayStale) {
		// First sample — or the first after an idle gap — seeds the estimate
		// whole: an EWMA from zero would halve it, a freshly added leg is judged
		// against this very number while its first packets are still in flight,
		// and a stale estimate describes a queue that has since drained.
		cur.ms = ms
	} else {
		alpha := 0.125
		if ms > cur.ms {
			alpha = 0.5
		}
		cur.ms += alpha * (ms - cur.ms)
	}
	cur.lastNano = now
	// One sample per leg per SBDSampleInterval is also the shared-bottleneck
	// detector's delay series while data flows (see bottleneck.go): the SACK
	// feedback is the only per-leg delay signal fast enough for SBD to rule
	// before a transfer is over. The decision is taken under this lock (so two
	// concurrent SACK handlers cannot both pass it) and the hook is called after
	// the unlock (it takes the route group's legLivenessMu).
	fold := false
	if m.onLegDelaySample != nil {
		if iv := int64(SBDSampleInterval()); now-m.sbdFoldNano[tpID] >= iv {
			if m.sbdFoldNano == nil {
				m.sbdFoldNano = make(map[uuid.UUID]int64)
			}
			m.sbdFoldNano[tpID] = now
			fold = true
		}
	}
	m.ackDelayByTpMu.Unlock()
	if fold {
		m.onLegDelaySample(tpID, ms)
	}
}

// setLegE2ERTT records one leg's smoothed end-to-end round-trip latency (ms),
// as measured by the leg-liveness pong. Non-positive values are ignored.
func (m *routeMux) setLegE2ERTT(tpID uuid.UUID, ms float64) {
	if ms <= 0 || tpID == uuid.Nil {
		return
	}
	m.legE2EMu.Lock()
	if m.legE2EByTp == nil {
		m.legE2EByTp = make(map[uuid.UUID]float64)
	}
	m.legE2EByTp[tpID] = ms
	m.legE2EMu.Unlock()
}

// legE2ERttMsTp returns the leg's smoothed end-to-end round-trip latency in ms
// (0 = no pong sample for that transport yet). Unlike ackDelayMsTp it does not
// expire: the liveness pong keeps measuring an idle leg, and the route latency
// it reports is a property of the path, not of a transfer.
func (m *routeMux) legE2ERttMsTp(tpID uuid.UUID) float64 {
	m.legE2EMu.RLock()
	defer m.legE2EMu.RUnlock()
	return m.legE2EByTp[tpID]
}

// legDelayBasisMs is the delay a frame in flight on this leg is judged against:
// the larger of the leg's measured send→ack delay (the real feedback delay
// under load, when fresh) and its end-to-end pong RTT (the path's latency,
// always current). Either alone under-reports — the first expires between
// transfers, the second does not see a queue — and judging a frame against a
// number smaller than its leg's own delay declares it lost while it is in
// ordinary flight. 0 when the leg has neither.
func (m *routeMux) legDelayBasisMs(tpID uuid.UUID) float64 {
	basis := m.ackDelayMsTp(tpID)
	if e2e := m.legE2ERttMsTp(tpID); e2e > basis {
		basis = e2e
	}
	return basis
}

// ackDelayMsTp returns the leg's EWMA send→ack delay in milliseconds (0 = no
// sample yet for that transport, or none for ackDelayStale).
func (m *routeMux) ackDelayMsTp(tpID uuid.UUID) float64 {
	now := time.Now().UnixNano()
	m.ackDelayByTpMu.Lock()
	defer m.ackDelayByTpMu.Unlock()
	e := m.ackDelayByTp[tpID]
	if e == nil || now-e.lastNano > int64(ackDelayStale) {
		return 0
	}
	return e.ms
}

// rackThresholdFor is rackThreshold judged for one leg: when the leg's own
// measured delay exceeds the group-wide basis, its holes wait for that delay
// (× the reorder factor, the ceiling raised to it) before they are presumed
// lost. Never below the group-wide threshold, which stays the floor for a leg
// with no delay sample of its own.
func (m *routeMux) rackThresholdFor(tpID uuid.UUID) time.Duration {
	return m.rackThresholdForWith(m.rackThreshold(), tpID)
}

// rackThresholdForWith is rackThresholdFor with the group threshold already
// computed. It touches no leg-table lock, so the SACK handler can call it
// while it holds the retx buffer's lock (rackThreshold reads the leg table,
// and the window refresh reads the buffer under that table's lock — the
// inversion that froze a group).
func (m *routeMux) rackThresholdForWith(th time.Duration, tpID uuid.UUID) time.Duration {
	// The leg's own delay is max(send→ack delay, end-to-end pong RTT). Judged on
	// the send→ack delay alone this was per-leg in form only: that estimate is
	// empty until a never-retransmitted frame is acked and expires
	// ackDelayStale after the last one, so the common case fell back to the
	// group threshold — built from ecfRttMs, whose floor is the FIRST-HOP
	// latency. On the measured 2026-09-17 compositions (legs at 151/216 ms and
	// 137/410 ms end-to-end over near first hops) that put every leg's holes on
	// the fast leg's clock and declared the slow leg's in-flight frames lost.
	ad := m.legDelayBasisMs(tpID)
	if ad <= 0 {
		return th
	}
	own := time.Duration(ad*m.rackFactor()) * time.Millisecond
	// The ceiling bounds how long we wait for a frame that really is lost, but
	// it must never pull the wait BELOW the leg's own basis plus the reordering
	// margin. Clamped at one bare basis (the previous max(rackCeil, basis)),
	// a queue-deep leg — basis past rackCeil is exactly the loaded case — waited
	// for the MEAN of its own delay distribution: every frame slower than that
	// mean was SACK-retransmitted while in perfectly ordinary flight on its own
	// leg, and the original then landed as a duplicate. Measured live
	// (2026-09-16 mux-legs-2, legs at 128/152 ms): a 10 MB upload that actually
	// striped 51/49 put 14.6 MB on the wire, retx_sent +337 of which
	// retx_req_sack +306, the duplicates saturating both send windows until the
	// writer parked (send_window_timeouts +6). A row that happened to keep 100 %
	// on one leg sent 10.02 MB with one retransmit.
	//
	// There is no separate cap left to apply: rackCeil is below basis×factor
	// exactly when the clamp used to bite, and the wait is already bounded by
	// the leg's own measured delay, which decays as the queue drains.
	if own < th {
		return th
	}
	return own
}

// maxActiveLegRTTms returns the slowest active (non-standby) leg's EWMA RTT in
// milliseconds, or 0 when no leg has a measured RTT yet. It is the reordering-
// window basis: a frame striped onto any active leg should arrive within the
// slowest leg's RTT plus a margin, so both the RACK loss threshold and the TLP
// probe timeout are derived from it.
//
// ecfRttMs is the leg's END-TO-END feedback delay (refreshLegWindows folds
// max(first-hop RTT, send→ack delay) into it), which is the delay a frame's
// acknowledgement actually has to survive. Judged on the first-hop RTT alone
// this returned ~95 ms while real feedback took ~170 ms, so RACK presumed loss
// inside one round trip and retransmitted everything in flight.
func (m *routeMux) maxActiveLegRTTms() float64 {
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	maxRtt := 0.0
	for i, lc := range m.legs {
		if lc == nil {
			continue
		}
		if i < len(m.standby) && m.standby[i] {
			continue // active legs only
		}
		if lc.ecfRttMs > maxRtt {
			maxRtt = lc.ecfRttMs
		}
	}
	return maxRtt
}

// getRetxPayload retrieves a stored payload for retransmission.
func (m *routeMux) getRetxPayload(seq uint32) []byte {
	if m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Get(seq)
}

// heldRetxSeqs returns every sequence currently held unACKed in the sender's
// retx buffer, ascending. Nil when SACK/retx is not in play. Used by the
// demote-time forced retx flush to re-send a parked leg's in-flight range onto
// an active leg (see RouteGroup.rotationServiceFn).
func (m *routeMux) heldRetxSeqs() []uint32 {
	if !m.sackEnabled || m.retxBuf == nil {
		return nil
	}
	return m.retxBuf.Seqs()
}

// heldRetxSeqsOnTps returns the held sequences whose LAST send rode one of the
// given transports (unknown-tag entries included conservatively), ascending.
// The demote-time flush uses this to rescue only the sequences the demoted
// leg(s) actually strand, instead of duplicating the whole in-flight window
// onto the surviving legs. Keyed by transport UUID, never leg index — indices
// shift on slice compaction and a stale index tag wedged live sessions.
func (m *routeMux) heldRetxSeqsOnTps(tpIDs []uuid.UUID) []uint32 {
	if !m.sackEnabled || m.retxBuf == nil || len(tpIDs) == 0 {
		return nil
	}
	set := make(map[uuid.UUID]bool, len(tpIDs))
	for _, id := range tpIDs {
		if id != uuid.Nil {
			set[id] = true
		}
	}
	return m.retxBuf.HeldSeqsOnTps(set)
}

// retxSetTp re-tags a held sequence's last-send transport after a retransmit
// moved it to a different leg, keeping the demote-flush attribution honest.
func (m *routeMux) retxSetTp(seq uint32, tpID uuid.UUID) {
	if m.retxBuf == nil {
		return
	}
	m.retxBuf.SetTpID(seq, tpID)
}

// rebuildWeights updates transport selection weights based on current latency.
func (m *routeMux) rebuildWeights(tps []*transport.ManagedTransport) {
	if m.tpSelector == nil {
		return
	}
	// Capacity mode: feed the selector each leg's throughput since
	// the last rebuild (bytes sent+recv delta) so it can weight the
	// schedule toward the legs actually moving data. Computed here
	// (not in the selector) because the mux owns the per-leg byte
	// counters. The delta resets each rebuild, so the weights track
	// RECENT throughput, not lifetime totals (which would entrench
	// whichever leg carried first).
	if m.tpSelector.Mode() == WeightModeCapacity {
		m.legMu.Lock()
		// Per-leg recent throughput (bytes moved since the last rebuild).
		deltas := make([]float64, len(m.legs))
		active := make([]bool, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			total := atomic.LoadUint64(&lc.sentBytes) + atomic.LoadUint64(&lc.recvBytes)
			delta := total - lc.lastTotalBytes
			lc.lastTotalBytes = total
			deltas[i] = float64(delta)
			// A warm-standby leg carries no send traffic — it must get zero weight
			// so the scheduler never steers a packet onto a parked leg (which the
			// receiver isn't expecting on that route and would stall the reorder
			// frontier on). Its byte counter was still sampled above so a later
			// promotion starts from a fresh delta, not a stale backlog.
			active[i] = !(i < len(m.standby) && m.standby[i])
		}
		// SHARED-BOTTLENECK COLLAPSE (RFC 8382): legs that funnel through the same
		// physical pipe (groupOf()) must be counted as ONE unit of capacity, not N
		// competing pipes — otherwise a 3-leg shared group out-weighs a lone
		// independent leg 3:1 and over-subscribes the one pipe while starving the
		// distinct route. For each group, ONE representative (the active member
		// moving the most bytes; ties → lowest index) carries the group's AGGREGATE
		// throughput (the pipe's real rate = sum of its active members' deltas); the
		// other members get zero send weight so the scheduler stops striping the same
		// pipe across redundant legs (which only adds reorder cost). Independent legs
		// are their own singleton group, so this is a no-op for them and the
		// pre-grouping behavior is unchanged when no bottleneck is detected.
		rep := make(map[int]int, len(m.legs)) // group id -> representative leg index
		groupSum := make(map[int]float64, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil || !active[i] {
				continue
			}
			g := m.groupOf(i)
			groupSum[g] += deltas[i]
			cur, ok := rep[g]
			if !ok || deltas[i] > deltas[cur] {
				rep[g] = i
			}
		}
		weights := make([]float64, len(m.legs))
		var maxW float64
		for g, r := range rep {
			weights[r] = groupSum[g]
			if weights[r] > maxW {
				maxW = weights[r]
			}
		}
		// Cold-leg floor (the weighted-RAMP): a just-promoted representative has
		// moved ~no bytes yet, so its raw delta is ~0 — under pure capacity weighting
		// it would get ~no traffic and thus never accumulate the goodput it needs to
		// earn a real share (a starvation deadlock). Give every active group's
		// REPRESENTATIVE a floor share = capacityColdFloorFrac of the fastest group,
		// so a fresh pipe carries a THIN trickle, measures its goodput, and ramps up.
		// Applied per group (one floor unit per distinct pipe, never per redundant
		// co-bottlenecked leg). Skipped when every group is idle (maxW == 0) so an
		// idle mux doesn't manufacture phantom weight.
		if maxW > 0 {
			floor := maxW * capacityColdFloorFrac
			for _, r := range rep {
				if weights[r] < floor {
					weights[r] = floor
				}
			}
		}
		m.legMu.Unlock()
		m.tpSelector.SetCapacityWeights(weights)
	}
	// ECF mode: build the per-leg {rate, RTT, jitter, ready, BDP} snapshot the
	// predictive scheduler reasons over. Rate is the sent-byte delta over the
	// refresh window (computed here, not from snapshotLegs, so it works even
	// when nothing is observing the telemetry page). RTT is the leg's END-TO-END
	// feedback delay — max(first-hop tp.GetLatency(), the leg's measured
	// send→ack delay) — not the near-edge hop alone. Jitter is an EWMA of
	// |RTT-mean|, the ECF sigma margin.
	//
	// OTIAS and STMS reason over the SAME ecfLegState snapshot (rate + RTT +
	// jitter + BDP + the selector-tracked in-flight estimate), so this one
	// branch feeds all three predictive schedulers; only the per-frame pick in
	// the selector differs (ecfPick vs otiasPick vs stmsPick).
	if m.tpSelector.Mode().isPredictive() || (m.sackEnabled && m.retxBuf != nil) {
		m.refreshLegWindows(tps)
	}
	m.tpSelector.Rebuild(tps)
}

// refreshLegWindows recomputes each leg's ECF state — RTT/jitter EWMAs, the
// SACK-proven delivery rate and the send window it sizes — and hands it to
// the selector. Every mode with SACK accounting gets a window (the download
// sender on the rig ran the capacity mode and had none). Run from
// rebuildWeights and, so a window can grow between rebuilds, from the route
// group's send-window loop every windowRefreshInterval.
func (m *routeMux) refreshLegWindows(tps []*transport.ManagedTransport) {
	if m.tpSelector == nil {
		return
	}
	// Acked bytes are read BEFORE legMu is taken: the retx buffer's lock is
	// held by the SACK handler while it asks for per-leg thresholds, which
	// read the leg table — taking the two in the opposite order here deadlocked
	// the group (measured live 2026-09-16: a three-leg session froze mid-upload
	// with the refresh waiting on the buffer and the SACK handler on legMu).
	ackedByIdx := make([]uint64, len(tps))
	if m.retxBuf != nil {
		for i, tp := range tps {
			if tp != nil {
				ackedByIdx[i] = m.retxBuf.AckedBytes(tp.Entry.ID)
			}
		}
	}
	// Per-leg send→ack delay, read once here (outside legMu) and used for BOTH
	// the leg's RTT basis and the window's feedback delay below — one lock trip
	// instead of one per leg per use, and the same number in both places.
	adByIdx := make([]float64, len(tps))
	for i, tp := range tps {
		if tp != nil {
			adByIdx[i] = m.ackDelayMsTp(tp.Entry.ID)
		}
	}
	{
		m.legMu.Lock()
		now := time.Now().UnixNano()
		var elapsed float64
		if m.ecfLastRebuildNano != 0 {
			elapsed = float64(now-m.ecfLastRebuildNano) / float64(time.Second)
		}
		states := make([]ecfLegState, len(m.legs))
		for i, lc := range m.legs {
			if lc == nil {
				continue
			}
			// Send rate over the refresh window (bytes/sec).
			sent := atomic.LoadUint64(&lc.sentBytes)
			var rate float64
			if elapsed > 0 {
				rate = float64(byteDelta(sent, lc.ecfLastSentBytes)) / elapsed
			}
			lc.ecfLastSentBytes = sent
			// RTT EWMA + jitter (sigma) EWMA, over the leg's END-TO-END feedback
			// delay: max(first-hop transport RTT, measured send→ack delay).
			//
			// The first-hop RTT alone is the wrong basis for every consumer of
			// this field. Measured live 2026-09-16 (exit sending, two pinned
			// legs): first-hop 10 ms via Amsterdam vs 95 ms via Atlanta, while
			// the real send→ack delay was ~170 ms on BOTH. ECF's hold-back rule
			// (n*rttF < hyst*(rttS+d)) then needed n≈9.5 frames queued before it
			// would spill to the second leg (split 41.5/9.5 MB of a 50 MB
			// download), and RACK's threshold — derived from the same short
			// basis via maxActiveLegRTTms — declared the in-flight frames lost
			// when it finally did (199 retransmit bursts / 1824 packets over
			// five rows, wire/goodput up to 1.19). The window sizing below
			// already used max(baseline, ack delay); this makes the whole leg
			// state agree with it. The first-hop value stays the FLOOR, so a
			// leg with no ack sample yet behaves exactly as before.
			var rttMs, hopMs float64
			if i < len(tps) && tps[i] != nil {
				hopMs = tps[i].GetLatency()
				rttMs = hopMs
				if ad := adByIdx[i]; ad > rttMs {
					rttMs = ad
				}
			}
			// The first-hop minimum is tracked on its own, with the same creep:
			// it is the BDP baseline's ceiling and must not be reachable from
			// the combined value.
			if hopMs > 0 {
				switch {
				case lc.ecfHopRttMinMs == 0 || hopMs < lc.ecfHopRttMinMs:
					lc.ecfHopRttMinMs = hopMs
				default:
					lc.ecfHopRttMinMs += routersettings.EcfRttMinCreep.Ratio() * (hopMs - lc.ecfHopRttMinMs)
				}
			}
			if rttMs > 0 {
				if lc.ecfRttMs == 0 {
					lc.ecfRttMs = rttMs
					lc.ecfRttMinMs = rttMs
				} else {
					dev := rttMs - lc.ecfRttMs
					if dev < 0 {
						dev = -dev
					}
					lc.ecfJitterMs = routersettings.EcfJitterAlpha.Ratio()*dev + (1-routersettings.EcfJitterAlpha.Ratio())*lc.ecfJitterMs
					lc.ecfRttMs = routersettings.EcfRttAlpha.Ratio()*rttMs + (1-routersettings.EcfRttAlpha.Ratio())*lc.ecfRttMs
					// Baseline RTT = running minimum of the SAME end-to-end basis,
					// with a slow upward creep: a transient congestion spike never
					// raises it, but a leg whose true latency rose for good is
					// eventually tracked. It must track the combined value, not the
					// first hop — ecfSaturated judges the live rtt against it, and a
					// first-hop baseline beside an end-to-end live value would read
					// as permanent congestion.
					if rttMs < lc.ecfRttMinMs {
						lc.ecfRttMinMs = rttMs
					} else {
						lc.ecfRttMinMs += routersettings.EcfRttMinCreep.Ratio() * (lc.ecfRttMs - lc.ecfRttMinMs)
					}
				}
			}
			// BDP latency = the baseline (uncongested) RTT, never the live RTT,
			// so a stalling leg's inflating RTT cannot grow its own cwnd and pull
			// more traffic onto itself.
			// ...and it is the FIRST-HOP running minimum, not the combined
			// end-to-end one #4970 put on ecfRttMinMs. The combined baseline
			// tracks the send→ack EWMA, which rises with a queue THIS window
			// created and (having no time decay) carried that queue into the next
			// transfer: measured live 2026-09-17, five consecutive 50 MB uploads
			// over ONE leg through a 470 ms intermediate fell 3.90 → 3.05 → 2.80
			// → 1.59 → 0.62 MB/s with ZERO loss (retx 6→28, send_window_waits 0)
			// while the leg's combined delay reading ratcheted to 13435 ms.
			//
			// The first-hop latency is the one delay on this leg our own window
			// cannot inflate — the transport measures it out of band — so it is
			// the stable floor the window is anchored to. ecfRttMs / rttMinMs keep
			// the combined value for ecfPick, the RACK threshold and the latency
			// band, which is what #4970 was for; only the BDP anchor comes back.
			bdpRttMs := lc.ecfHopRttMinMs
			if bdpRttMs <= 0 {
				bdpRttMs = lc.ecfRttMinMs
			}
			if bdpRttMs <= 0 {
				bdpRttMs = lc.ecfRttMs
			}
			// The send window is what the peer's SACKs PROVE the leg delivers per
			// baseline RTT (× a growth margin), not what we managed to hand the
			// transport: the send rate counts bytes queued into a bloated leg as
			// capacity, which is how a slow leg was fed seconds deep. Cold legs
			// (no acked bytes yet) keep the send-rate BDP and the probe budget.
			cwnd := rate * bdpRttMs / 1000.0
			if m.retxBuf != nil && i < len(tps) && tps[i] != nil {
				acked := ackedByIdx[i]
				switch {
				case acked == 0:
					// cold: nothing acknowledged yet, send-rate BDP + probe budget
				case lc.ecfLastAckedBytes == 0:
					lc.ecfLastAckedBytes = acked
					lc.ecfLastAckedNano = now
				default:
					// The window changes only on EVIDENCE: the delivery rate is the
					// bytes acknowledged since the last refresh that saw an ack, over
					// the time since that refresh. A refresh with no new ack (the
					// SACK cadence is coarser than the refresh at low rates) keeps
					// the last proven window instead of falling back to the send-rate
					// estimate, which would have counted queued bytes as capacity.
					delta := byteDelta(acked, lc.ecfLastAckedBytes)
					if dt := float64(now-lc.ecfLastAckedNano) / float64(time.Second); delta > 0 && dt > 0 {
						deliv := float64(delta) / dt
						// The delivery rate is measured per FEEDBACK delay (send→SACK,
						// which the delayed ack and the SACK cadence stretch past the
						// ping RTT), so the window must be sized over that delay too:
						// sized over the shorter ping RTT it shrank every refresh
						// under load (measured: uploads on a 2-tunnel session fell
						// from 9.7 to 4.5 MB/s with 750 writer parks, and clamping it
						// to a first-hop multiple instead collapsed a 470 ms path to
						// the 128 KiB floor: 0.23 MB/s on a 50 MB upload, wire/
						// goodput 1.00, the starved window shrinking the delivery
						// that sizes it). The ack delay only ever WIDENS the window
						// here, and ackDelayStale expires it once the transfer ends,
						// so the queue one transfer built is not the next one's
						// basis — which is the ratchet, not this max.
						fbMs := bdpRttMs
						if ad := adByIdx[i]; ad > fbMs {
							fbMs = ad
						}
						cwnd = deliv * fbMs / 1000.0 * EcfWindowMargin()
						lc.ecfCwndBytes = cwnd
						lc.ecfLastAckedBytes = acked
						lc.ecfLastAckedNano = now
						lc.ecfDelivBps = foldDeliv(lc.ecfDelivBps, deliv)
					} else if lc.ecfCwndBytes > 0 {
						cwnd = lc.ecfCwndBytes
					}
					// A leg that has gone quiet folds a ZERO into its delivery
					// EWMA once the silence passes a second, so the gate reads
					// "delivering nothing now" instead of the last rate it proved
					// before it stalled. The WINDOW is deliberately left alone —
					// it changes only on ack evidence (above).
					if now-lc.ecfLastAckedNano > int64(time.Second) {
						lc.ecfDelivBps = foldDeliv(lc.ecfDelivBps, 0)
					}
				}
			}
			// Clamp EVERY path to the window bounds, not just the evidence branch
			// above: a cold leg (nothing acked yet), the first-ack seeding branch
			// and a group with no retx buffer all reached here with a raw
			// rate×BDP window and no ceiling, so a just-promoted leg was handed an
			// unbounded send window and over-subscribed the no-skip frontier.
			if lo := float64(EcfMinWindowBytes()); cwnd < lo {
				cwnd = lo
			}
			if hi := float64(EcfMaxWindowBytes()); cwnd > hi {
				cwnd = hi
			}
			ready := true
			if i < len(m.standby) && m.standby[i] {
				ready = false
			}
			if i < len(m.ready) && !m.ready[i] {
				ready = false
			}
			states[i] = ecfLegState{
				rttMs:      lc.ecfRttMs,
				rttMinMs:   lc.ecfRttMinMs,
				jitterMs:   lc.ecfJitterMs,
				rateBps:    rate,
				cwndBytes:  cwnd,
				ready:      ready,
				delivBps:   lc.ecfDelivBps,
				delivKnown: lc.ecfLastAckedNano != 0,
			}
		}
		rulings := m.ruleProbeOnlyLegsLocked(states)
		m.ecfLastRebuildNano = now
		m.legMu.Unlock()
		m.tpSelector.SetECFState(states)
		m.reportProbeRulings(tps, rulings)
	}
}

// legProbeRuling is one leg crossing into — or back out of — the probe-only
// state, carried out of the window refresh so the event is recorded with legMu
// dropped.
type legProbeRuling struct {
	idx       int
	probeOnly bool
	reason    string
}

// ruleProbeOnlyLegsLocked decides, once per window refresh, which legs are so
// far behind the group's best ACTIVE leg that they may carry no more than a
// probe per window.
//
// The download on a two-leg group is not scheduled by ECF at all: under
// CapUniDir the reverse direction is picked by selectByDirection, whose tier 1
// is the mirrored round-robin schedule (transportSelector.Rebuild puts the live
// legs in ts.schedule unweighted for every predictive mode), so a leg keeps
// taking every other frame no matter what its delay basis says. Measured live
// 2026-09-18 (bench/2026-09-18/1008cc8e5, compose 2x2): group rg49220 paired a
// 7.4 MB/s leg with one whose own reference ran at 0.06 MB/s, the scheduler put
// 0.5-2.2 MB of each 10 MB download on the slow one, and the five trials took
// 8.3-37.4 s — each one's duration is the bytes placed on the slow leg divided
// by its ~60 KB/s. The group's own detector had the number: an sbd_ruling in
// the same run read "mean 755.9 vs 9291.3 ms over 8/8 samples".
//
// A DELAY reading alone is not enough to rule on, and the first live run
// (bench/2026-09-18/9f4848dfa-smoke) proved it: on legs-2 the healthy 166 ms leg
// beside the 44 ms one was cut five times — "322 ms against 53 ms", "270 vs 43",
// "613 vs 86", "833 vs 122", "843 vs 125" — and the set fell to x0.854 on a
// 50 MB download from x1.0-1.08. Under ECF a slower leg carrying its share sits
// at 6-7x on the send→ack basis routinely, because that basis is window/rate:
// the queue is OUR OWN and the leg was delivering 27-65 % of the bytes while it
// read that way (carrier rows 11-13: 18.5/37.2, 10.7/39.4, 33.7/18.0 MB).
//
// So the ruling takes TWO readings, and a leg has to fail both:
//
//	DELAY — ecfRttMs, the END-TO-END feedback delay (max of first-hop RTT and
//	   measured send→ack delay), above legProbeMinBasisMs in absolute terms and
//	   more than LegStarveRatio times the best ready leg's.
//	GOODPUT — ecfDelivBps, what the peer's SACKs PROVE the leg delivered, below
//	   1/LegStarveRatio of the best ready leg's. The same ratio serves both ends:
//	   a leg that is slow but PRODUCTIVE keeps its share.
//
// The AG case clears both by a wide margin: 9291 ms against 756 ms (12.3x) while
// delivering ~60 KB/s against ~7 MB/s (0.9 % — the goodput test wants under
// 16.7 %). The healthy legs-2 pair fails the second: 6-7x on delay, but 27 % of
// the bytes at worst. A leg with no ack of its own yet (delivKnown false) is
// never ruled — cold is not the same as unproductive.
//
// A probe-only leg is not parked: it keeps its rules, keeps being measured by
// its probe, and its basis decays back toward the first-hop RTT once we stop
// queueing on it (ackDelayStale expires the send→ack term), so the ruling lifts
// by itself when the leg recovers.
//
// Every reading here comes from THIS group's own leg table — states is built
// from m.legs, one routeMux per RouteGroup — so a leg is only ever judged
// against its siblings in the same route group.
//
// Caller holds legMu; returns the transitions for reportProbeRulings to record.
func (m *routeMux) ruleProbeOnlyLegsLocked(states []ecfLegState) []legProbeRuling {
	ratio := LegStarveRatio()
	best, bestDeliv := 0.0, 0.0
	for i := range states {
		if !states[i].ready {
			continue
		}
		if states[i].rttMs > 0 && (best == 0 || states[i].rttMs < best) {
			best = states[i].rttMs
		}
		if states[i].delivKnown && states[i].delivBps > bestDeliv {
			bestDeliv = states[i].delivBps
		}
	}
	var out []legProbeRuling
	for i, lc := range m.legs {
		if lc == nil || i >= len(states) {
			continue
		}
		basis, deliv := states[i].rttMs, states[i].delivBps
		outclassedByDelay := basis >= m.knRatio(routersettings.LegProbeMinBasisMs) && basis > ratio*best
		unproductive := states[i].delivKnown && bestDeliv > 0 && deliv*ratio < bestDeliv
		probeOnly := ratio > 1 && best > 0 && states[i].ready &&
			outclassedByDelay && unproductive
		was := math.Float64frombits(atomic.LoadUint64(&lc.probeBasisBits)) > 0
		switch {
		case probeOnly:
			atomic.StoreUint64(&lc.probeBasisBits, math.Float64bits(basis))
		default:
			atomic.StoreUint64(&lc.probeBasisBits, 0)
		}
		if probeOnly == was {
			continue
		}
		r := legProbeRuling{idx: i, probeOnly: probeOnly}
		if probeOnly {
			r.reason = fmt.Sprintf("delay basis %.0f ms against the best active leg's %.0f ms (more than %.1fx) AND delivering %.0f B/s against its %.0f B/s (under 1/%.1f) — capped at %d bytes per %.0f ms window instead of a proportional share; not parked, the probe keeps measuring it",
				basis, best, ratio, deliv, bestDeliv, ratio, LegProbeBytes(), m.probeWindowMs(basis))
		} else {
			why := fmt.Sprintf("delay basis %.0f ms is back within %.1fx of the best active leg's %.0f ms", basis, ratio, best)
			if outclassedByDelay {
				why = fmt.Sprintf("delivering %.0f B/s against the best active leg's %.0f B/s — slow but productive", deliv, bestDeliv)
			}
			r.reason = why + " — full share restored"
		}
		out = append(out, r)
	}
	return out
}

// reportProbeRulings records each probe-only transition as a mux event. Called
// with legMu dropped; the hook is the route group's noteLegEvent, which takes no
// locks of its own.
func (m *routeMux) reportProbeRulings(tps []*transport.ManagedTransport, rulings []legProbeRuling) {
	if len(rulings) == 0 || m.onLegProbeRuling == nil {
		return
	}
	for _, r := range rulings {
		var tp *transport.ManagedTransport
		if r.idx < len(tps) {
			tp = tps[r.idx]
		}
		m.onLegProbeRuling(r.idx, len(tps), tp, r.probeOnly, r.reason)
	}
}

// SetLegProbeRulingFn wires the callback the mux fires when a leg is ruled
// probe-only or restored to a full share, so the route group can record a
// leg_probe_only / leg_full_share mux event. Called once by the route group
// when the mux is built.
func (m *routeMux) SetLegProbeRulingFn(fn func(idx, legs int, tp *transport.ManagedTransport, probeOnly bool, reason string)) {
	m.onLegProbeRuling = fn
}

// foldDeliv folds one delivery-rate sample into a leg's EWMA. Smoothed because
// the ruling reads it against a sibling's: an instantaneous rate over one
// ~100 ms refresh swings far enough for a productive leg to look idle for a
// tick, and the cost of a wrong ruling is a starved leg.
func foldDeliv(prev, sample float64) float64 {
	if prev <= 0 {
		return sample
	}
	a := LegDelivAlpha()
	return a*sample + (1-a)*prev
}

// probeWindowMs is how long one probe budget lasts on a leg with this delay
// basis: the leg's own basis, floored at legProbeMinWindow so a fast-but-thin
// leg is not re-probed thousands of times a second.
func (m *routeMux) probeWindowMs(basisMs float64) float64 {
	if lo := float64(m.knDur(routersettings.LegProbeMinWindow)) / float64(time.Millisecond); basisMs < lo {
		return lo
	}
	return basisMs
}

// legProbeExhausted reports whether leg idx is ruled probe-only AND has already
// carried its probe budget in the current window. It is the selection gate: a
// leg it answers true for is skipped while any other leg can take the frame,
// so an outclassed leg is fed a probe's worth per window rather than a
// proportional share. False for every leg that is not ruled probe-only, so a
// group of comparable legs picks exactly as it did before.
//
// The window rolls here (the first pick past its end resets the byte count)
// rather than on the refresh tick, so the budget is paced by the leg's own
// delay basis and not by windowRefreshInterval.
func (m *routeMux) legProbeExhausted(idx int) bool {
	if idx < 0 {
		return false
	}
	m.legMu.RLock()
	defer m.legMu.RUnlock()
	if idx >= len(m.legs) || m.legs[idx] == nil {
		return false
	}
	lc := m.legs[idx]
	basis := math.Float64frombits(atomic.LoadUint64(&lc.probeBasisBits))
	if basis <= 0 {
		return false
	}
	now := time.Now().UnixNano()
	win := int64(m.probeWindowMs(basis) * float64(time.Millisecond))
	start := atomic.LoadInt64(&lc.probeWinNano)
	if now-start >= win && atomic.CompareAndSwapInt64(&lc.probeWinNano, start, now) {
		atomic.StoreUint64(&lc.probeWinBytes, 0)
	}
	return atomic.LoadUint64(&lc.probeWinBytes) >= uint64(LegProbeBytes()) //nolint:gosec // LegProbeBytes is refused unless positive
}

// firstProbeReadyLeg returns the lowest-indexed live, ready leg that is not out
// of probe budget, or -1 when every ready leg is. Used to move a schedule pick
// off an outclassed leg.
func (m *routeMux) firstProbeReadyLeg(tps []*transport.ManagedTransport) int {
	for idx, tp := range tps {
		if tp == nil || tp.IsClosed() || !m.legReadyAt(idx) {
			continue
		}
		if !m.legProbeExhausted(idx) {
			return idx
		}
	}
	return -1
}

// Per-leg send window (test plan §3.1). ecfWindowMargin scales the SACK-proven
// delivery per baseline RTT into the window so a leg can grow: delivery
// measured under a window is at most window/RTT, and ×2 lets it double each
// refresh, the slow-start ratio. ecfMinWindowBytes keeps a thin leg probing.
// sendWindowWaitMax bounds how long a writer parks when every ready leg is at
// its window before it sends anyway — progress is guaranteed even if feedback
// stops (the frame then queues as before and TLP / RACK recover), and
// sendWindowPoll re-checks in case a wake-up was coalesced.
// windowRefreshInterval paces the growth: a leg's window can double per
// refresh, so the ramp from the floor to a 3 MB BDP takes ~5 refreshes
// (measured at 250 ms: 10 MB uploads over two legs ran at half the single-route
// rate, the ramp alone costing more than a second).
const (
	ecfWindowMarginDefault       = 2.0
	ecfMinWindowBytesDefault     = 128 * 1024
	ecfMaxWindowBytesDefault     = 8 * 1024 * 1024
	sendWindowWaitMaxDefault     = 250 * time.Millisecond
	sendWindowPollDefault        = 20 * time.Millisecond
	windowRefreshIntervalDefault = 100 * time.Millisecond
)

// The outclassed-leg gate (ruleProbeOnlyLegsLocked / legProbeExhausted).
//
// legStarveRatio serves BOTH halves of the ruling: a leg's delay basis must
// exceed the best ready leg's by more than this AND its proven delivery rate
// must be under 1/this of the best leg's. One number because the two tests are
// the same judgement from either end, and the live margins are wide on both:
// the leg that had to be cut read 12.3x on delay and 0.9 % on goodput, while the
// healthy legs-2 pair that must NOT be cut read 6-7x on delay but 27-65 % on
// goodput. legProbeBytes is what a ruled leg may carry per window; one frame is
// at most ~64 KiB, so the budget is about one frame per window.
// legProbeMinBasisMs is the absolute floor under which no leg is ever cut, so a
// pair of fast legs whose ratio happens to be large (5 ms against 40 ms) is left
// alone; legProbeMinWindow floors the window for a leg whose basis is short; and
// legDelivAlpha weights the newest sample in the per-leg delivery EWMA the
// goodput half reads.
const (
	legStarveRatioDefault     = 6.0
	legProbeBytesDefault      = 64 * 1024
	legProbeMinBasisMsDefault = 250.0
	legProbeMinWindowDefault  = 250 * time.Millisecond
	legDelivAlphaDefault      = 0.3
)

// feedInflight hands the predictive selector each leg's REAL unacknowledged
// bytes (from the retx buffer, attributed per transport) before a pick, so
// ecfSaturated judges true backlog rather than a rate-drain estimate.
func (m *routeMux) feedInflight(tps []*transport.ManagedTransport) {
	if m.retxBuf == nil || !m.sackEnabled || m.tpSelector == nil {
		return
	}
	ids := make([]uuid.UUID, len(tps))
	for i, tp := range tps {
		if tp != nil {
			ids[i] = tp.Entry.ID
		}
	}
	m.tpSelector.SetInflight(m.retxBuf.HeldBytes(ids))
}

// signalWindow wakes a writer parked in waitSendWindow (coalescing: one pending
// wake-up at a time). Called when a SACK purged entries or a leg's standby
// state changed.
func (m *routeMux) signalWindow() {
	if m.windowCh == nil {
		return
	}
	select {
	case m.windowCh <- struct{}{}:
	default:
	}
}

// sendWindowBlocked reports whether the next frame has nowhere to go under the
// send windows in force. For a FORWARD-confined direction that is one question
// — is the CONFINED leg at its window — because the frame is going on that leg
// and no other: with --forward-spill off (the default) a full window is a
// reason to wait, not a reason to spray the upload onto a slower leg. Every
// other case keeps the original test, every ready leg saturated.
//
// Called from waitSendWindow, which runs on the writer with the route group's
// mu DROPPED, so it reads the confined leg from the atomic mirror.
func (m *routeMux) sendWindowBlocked() bool {
	if !ForwardSpill() {
		if idx := m.confinedForwardIdx(); idx >= 0 && m.forwardSender() {
			return m.tpSelector.Saturated(idx)
		}
	}
	return m.tpSelector.AllReadySaturated()
}

// waitSendWindow parks a writer while the leg(s) it may use are at their
// in-flight window, until a SACK frees capacity, the group closes, or
// sendWindowWaitMax elapses. A no-op unless SACK accounting and a predictive
// scheduler are on.
func (m *routeMux) waitSendWindow(tps []*transport.ManagedTransport, closed <-chan struct{}) {
	if m.retxBuf == nil || !m.sackEnabled || m.tpSelector == nil {
		return
	}
	deadline := time.Now().Add(SendWindowWaitMax())
	waited := false
	for {
		m.feedInflight(tps)
		if !m.sendWindowBlocked() {
			return
		}
		if !waited {
			atomic.AddUint64(&m.sendWindowWaits, 1)
			waited = true
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			atomic.AddUint64(&m.sendWindowTimeouts, 1)
			return
		}
		if remaining > m.knDur(routersettings.SendWindowPoll) {
			remaining = m.knDur(routersettings.SendWindowPoll)
		}
		t := time.NewTimer(remaining)
		select {
		case <-m.windowCh:
		case <-t.C:
		case <-closed:
			t.Stop()
			return
		}
		t.Stop()
	}
}
