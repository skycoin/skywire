// Package router pkg/router/route_mux.go c2-net-routing
package router

import (
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
	sentBytes   atomic.Uint64 // atomic
	sentPackets atomic.Uint64 // atomic
	recvBytes   atomic.Uint64 // atomic
	recvPackets atomic.Uint64 // atomic
	// lastRecvNano is when this leg last delivered an inbound frame (UnixNano,
	// 0 = never): the evidence sackLeg reads to keep SACKs off a dead leg.
	lastRecvNano atomic.Int64
	retransmits  atomic.Uint64 // atomic
	// payloadBytes is the UNIQUE in-order payload this leg delivered: each
	// sequence credited once, to the leg it FIRST arrived on. Unlike recvBytes
	// (every inbound frame, incl. retransmits/duplicates), a retransmit of a seq
	// already seen on another leg is NOT counted, so the per-leg payloadBytes sum
	// equals the transfer size and cleanly attributes which legs carried a
	// direction's data — the confound-free basis for per-direction leg telemetry.
	payloadBytes atomic.Uint64 // atomic
	// dupBytes is inbound DUPLICATE data on this leg (a seq already delivered
	// or buffered when it arrived here) — the peer's spurious-retransmit waste,
	// which rides the fastest leg and otherwise masquerades as payload in
	// recvBytes. repairBytes is inbound FEC repair frames — deliberate overhead,
	// counted apart from waste. Both atomic.
	dupBytes    atomic.Uint64 // atomic
	repairBytes atomic.Uint64 // atomic
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
	probeBasisBits atomic.Uint64 // atomic (float64 bits)
	probeWinNano   atomic.Int64  // atomic
	probeWinBytes  atomic.Uint64 // atomic
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

	// ctrlSeq numbers the IN-BAND leg control frames (mux_control_frame.go),
	// which ride the reserved top band of the sequence space and so must not
	// draw from writeSeq: a control frame neither consumes nor skips a data
	// sequence, and never enters the reorder/SACK space.
	ctrlSeq atomic.Uint32

	// RACK-TLP tail-loss probe + DSACK reorder-window adaptation (see rack_tlp.go).
	// lastSendNano stamps the last DATA/retransmit frame put on the wire (the TLP
	// idle timer reads it). tlpProbeCount is the number of consecutive tail probes
	// fired since the last ack progress — bounded so a truly dead tail doesn't probe
	// forever. rackFactorMilli is the DSACK-adapted reorder tolerance ×1000: a
	// duplicate report from the receiver widens it (we retransmitted too eagerly),
	// clean acks decay it back toward the static baseline. All atomic.
	lastSendNano    atomic.Int64
	tlpProbeCount   int32
	rackFactorMilli atomic.Int64
	lastAckedContig uint32 // atomic: highest SACK lastContiguous seen (ack-progress edge for TLP reset)
	// ackDelayMilli is an EWMA (×1000) of the measured send→ack delay of
	// never-retransmitted frames (fed by retxBuf.onAckDelay). Under load it is
	// the queue, not the wire, that dominates feedback delay: a saturated leg
	// holds seconds of in-flight data while the transport's idle-ping RTT still
	// reads ~25ms, and a RACK threshold built only on the latter declares every
	// queued frame lost (measured: 899 spurious retransmits on a ~1250-frame
	// upload). rackThreshold takes the max of both signals. Atomic; 0 = no
	// sample yet.
	ackDelayMilli atomic.Int64
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
	sendWindowWaits    atomic.Uint64
	sendWindowTimeouts atomic.Uint64

	// reorderDrops counts RECEIVE-side packets the reorder buffer dropped at
	// maxGap (see reorderBuffer.InsertOrDrop). Previously invisible: the drop
	// was silent and the packet was SACKed anyway, so a wedge caused by it had
	// no witness at all. Non-zero here means the frontier gap grew past the
	// whole reorder window — a leg died mid-stream — and the dropped sequences
	// are waiting on the sender's retransmit. atomic.
	reorderDrops atomic.Uint64

	// lastSACKNano rate-limits receiver-side SACK feedback. Cross-leg
	// reordering from latency skew makes nearly every packet arrive
	// out-of-order, so firing a SACK per out-of-order packet would spawn a
	// goroutine and emit a control packet thousands of times per second.
	// atomic: UnixNano of the last SACK we sent.
	lastSACKNano atomic.Int64

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
	retxSkippedMissing atomic.Uint64
	// retxReqSACK / retxReqHOL / retxReqFlush count the sequences each
	// retransmit path ASKED to resend (reactive SACK holes past the RACK
	// threshold, the proactive head-of-line nudge, the demote-time flush), so a
	// retransmit storm names its source from visor state.
	retxReqSACK  atomic.Uint64
	retxReqHOL   atomic.Uint64
	retxReqFlush atomic.Uint64
	// retxDeferredYoung counts the holes a SACK named that were NOT resent
	// because the frame is still younger than the delay basis of the leg it was
	// sent on (legDelayBasisMs × the reorder factor) — the duplicates that used
	// to leave on the group's clock. Rising here while retx_req_sack stays low
	// is the fix working; both rising together is a genuinely lossy leg.
	retxDeferredYoung atomic.Uint64
	retxSendErrors    atomic.Uint64
	tlpProbes         atomic.Uint64
	sacksRecv         atomic.Uint64
	lastSACKRecvNano  atomic.Int64
	sacksSent         atomic.Uint64
	sackSendErrors    atomic.Uint64
	lastSACKSentNano  atomic.Int64

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
	holSACKNano    atomic.Int64 // atomic: UnixNano of the last proactive HoL SACK we sent

	// legStateEnabled is true when both peers advertised CapLegState. When set, a
	// park/promote of a leg is signaled to the remote (LegStatePacket) so it
	// mirrors the active/standby set on its send side — otherwise standby is a
	// send-side-only decision and the bulk-sending peer stripes across every
	// established leg, head-of-line-stalling the reorder frontier (the wide-mux
	// download stall). See handleLegStatePacket / sendLegState in route_group.go.
	legStateEnabled bool

	// legRehomeEnabled is true when both peers advertised CapLegRehome. When set,
	// a STANDBY route group's whole built chain can be moved into this group as a
	// mux leg in place, with no setup-node dial (see leg_rehome.go and
	// docs/design/leg-rehome.md). Unset, RehomeStandbyLeg reports
	// ErrRehomeUnsupported and the caller dials a pool-sourced leg instead.
	legRehomeEnabled bool

	// deliveryCRC is true when both peers advertised CapDeliveryCRC (and our own
	// mux.delivery_crc knob is on). When set, wrapPayload stamps every data frame
	// with a CRC32C over (seq ‖ payload) and deliverData verifies + strips it on
	// the in-order delivery path. See delivery_crc.go for the framing.
	// groupPort/crcWarnOnce only serve the mismatch log: the group's local port
	// identifies it, and the warning is emitted once per group so a corrupting
	// leg cannot flood the log (every mismatch is still counted).
	deliveryCRC bool
	groupPort   routing.Port
	crcWarnOnce sync.Once

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
	fecRepairBytesSent atomic.Uint64
	fecRepairBytesRecv atomic.Uint64
	fecReconstructs    atomic.Uint64

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

	// fwdFan latches the FORWARD direction's load-triggered fan-out: the
	// confined leg keeps priority, and its sibling legs carry the overflow only
	// while a sustained upload holds its send window full (unidir.go).
	fwdFan forwardFanout

	// onForwardFanout, when wired (SetForwardFanoutFn), is called when that
	// latch moves, so the route group can record a forward_fanout /
	// forward_confined mux event. Called both from the writer (legMu and rg.mu
	// dropped) and from the send path, so the callback must take no locks.
	onForwardFanout func(on bool, idx int, reason string)
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
		// No leg held by the forward confinement yet, and no challenger.
		confinedFwdIdx:     -1,
		confinedFwdChalIdx: -1,
	}
	m.confinedFwdCur.Store(-1)
	// RACK reorder factor starts at the static baseline; DSACK feedback widens
	// it and clean acks decay it back (see rack_tlp.go). Set after construction
	// because atomic.Int64 carries a noCopy and cannot go in a struct literal.
	m.rackFactorMilli.Store(int64(routersettings.RackReorderFactor.Ratio() * 1000))
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

// msSinceNano is the age in milliseconds of an atomic UnixNano stamp, or -1
// when the stamp was never set (never vs "just now" are opposite diagnoses for
// a SACK feedback path, so they must not both render as 0).
func msSinceNano(p *atomic.Int64) float64 {
	nano := p.Load()
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
