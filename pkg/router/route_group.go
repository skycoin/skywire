// Package router pkg/router/route_group.go c2-net-routing
package router

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/ioutil"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
	"github.com/skycoin/skywire/pkg/util/deadline"
)

const (
	defaultRouteGroupKeepAliveInterval = DefaultRouteKeepAlive / 2
	defaultReadChBufSize               = 1024
	closeRoutineTimeout                = 2 * time.Second
	// maxConsecutiveWriteFailures is the number of consecutive transport write failures
	// before the RouteGroup closes itself to stop spamming logs.
	maxConsecutiveWriteFailures = 5

	// legLivenessInterval is how often each multiplexed leg is probed
	// end-to-end. pruneDeadTransports only sees a dead LOCAL (first-hop)
	// transport; a transport dying BEYOND the first hop leaves the local tp
	// open, so the leg silently black-holes. This probe sends a Ping down each
	// leg that the destination echoes (Pong) — purely source-side, no wire or
	// remote-version change (every visor already echoes pings).
	legLivenessInterval = 30 * time.Second
	// legPongMissThreshold is the number of consecutive liveness probes with no
	// echo after which a leg is treated as black-holing and dropped (mirrors
	// transport-level pongMissThreshold). The leg is removed from the mux but
	// its (possibly shared) transport is NOT closed, so a false positive simply
	// triggers a self-heal re-dial — never an outage, and never the last leg.
	legPongMissThreshold = 3
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
	lastSent int64

	// consecutiveWriteFailures tracks repeated transport write errors.
	// After maxConsecutiveWriteFailures, the RouteGroup closes itself.
	consecutiveWriteFailures int32

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

	handshakeProcessed     chan struct{}
	handshakeProcessedOnce sync.Once
	encrypt                bool

	// forwardHops stores the complete route path as originally calculated.
	// This is the full multi-hop route, not just local transports.
	forwardHops []routing.Hop

	// initiator is true when this visor dialed the remote end (called
	// router.DialRoutes); false when this visor accepted the route via
	// AcceptRoutes / saveRouteGroupRules from a setup-node request.
	// Set once at construction in saveRouteGroupRules from nsConf.Initiator
	// and never mutated afterwards.
	initiator bool

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

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline

	networkStats *networkStats

	// used as a bool to indicate if this particular route group initiated close loop
	closeInitiated   int32
	remoteClosedOnce sync.Once
	remoteClosed     chan struct{}
	closed           chan struct{}
	// used to wait for all the `Close` packets to run through the loop and come back.
	// Atomic counter + channel instead of sync.WaitGroup to avoid
	// "WaitGroup reused before previous Wait returned" panics.
	closeDonePending int32         // atomic counter of outstanding close acks
	closeDoneCh      chan struct{} // closed when closeDonePending reaches 0
	once             sync.Once     // guards the readCh/remoteClosed cleanup
	closedOnce       sync.Once     // guards close(rg.closed) — closable from two paths

	errorMu    sync.RWMutex
	closeError error

	// For synchronous latency measurement
	pendingPongCh chan float64 // Receives measured latency when pong arrives
	pendingPongMu sync.Mutex

	// Route multiplexing layer (nil when mux not negotiated).
	// Encapsulates sequencing, reordering, SACK, and transport selection.
	mux *routeMux

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
	rotationInterval time.Duration

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

	// Per-leg end-to-end liveness (issue #2). Keyed by leg transport ID so the
	// state survives index shifts from prune/append. legPongSeen records
	// whether the echo for a leg's probe came back since the last tick;
	// legMissed counts consecutive missed ticks; inflightPings maps a probe's
	// send-timestamp (the verbatim-echoed correlator) back to the leg it
	// tested. Guarded by legLivenessMu (NEVER held while taking rg.mu).
	legLivenessMu sync.Mutex
	legPongSeen   map[uuid.UUID]bool
	legMissed     map[uuid.UUID]int
	inflightPings map[int64]uuid.UUID
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
		readBuf:            bytes.Buffer{},
		remoteClosed:       make(chan struct{}),
		closed:             make(chan struct{}),
		readDeadline:       deadline.MakePipeDeadline(),
		writeDeadline:      deadline.MakePipeDeadline(),
		handshakeProcessed: make(chan struct{}),
		networkStats:       newNetworkStats(),
		legPongSeen:        make(map[uuid.UUID]bool),
		legMissed:          make(map[uuid.UUID]int),
		inflightPings:      make(map[int64]uuid.UUID),
	}

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
	rg.logger = &logging.Logger{FieldLogger: rg.logger.WithField("app_name", name)}
}

// AppName returns the rg's stored app name (set via SetAppName).
// Empty when the rg was created without app context (accept-side).
func (rg *RouteGroup) AppName() string {
	return rg.appName
}

// MuxInfo is a point-in-time snapshot of one route group's
// multiplexing state — per-leg byte/packet counters plus the
// transport identity each leg maps to. Returned by Router.MuxInfo
// for 'cli proxy mux-info'.
type MuxInfo struct {
	// Desc identifies the route group.
	Desc routing.RouteDescriptor
	// MuxEnabled is false for non-mux'd rg's; the per-leg counters
	// are still populated for the single transport in that case.
	MuxEnabled bool
	// SACKEnabled is true when the peers negotiated SACK retx.
	SACKEnabled bool
	// Legs is in tps[] order. One entry per active mux leg.
	Legs []MuxLeg
}

// MuxLeg pairs the per-leg counters with the transport identity.
// The transport's own Latency / SentBytes / RecvBytes counters are
// the *transport-level* totals (across all rg's that share the
// transport); the LegStats here are *rg-scoped* (only what this
// rg sent through this leg).
type MuxLeg struct {
	LegStats
	TransportID string `json:"transport_id"`
	TpType      string `json:"tp_type"`
	RemotePK    string `json:"remote_pk"`
	// LatencyMS is the transport-level smoothed RTT in ms (the same
	// value 'tp ls' shows). Zero when no measurement yet.
	LatencyMS float64 `json:"latency_ms"`
}

// MuxStats returns a point-in-time snapshot of the rg's per-leg
// counters paired with each leg's transport identity.
func (rg *RouteGroup) MuxStats() MuxInfo {
	info := MuxInfo{Desc: rg.desc}
	rg.mu.Lock()
	if rg.mux != nil {
		info.MuxEnabled = true
		info.SACKEnabled = rg.mux.sackEnabled
	}
	tpsCopy := append([]*transport.ManagedTransport(nil), rg.tps...)
	rg.mu.Unlock()

	var stats []LegStats
	if rg.mux != nil {
		stats = rg.mux.snapshotLegs()
	}

	info.Legs = make([]MuxLeg, 0, len(tpsCopy))
	for i, tp := range tpsCopy {
		leg := MuxLeg{}
		if i < len(stats) {
			leg.LegStats = stats[i]
		} else {
			leg.Index = i
		}
		if tp != nil {
			leg.TransportID = tp.Entry.ID.String()
			leg.TpType = string(tp.Entry.Type)
			leg.RemotePK = tp.Remote().String()
			leg.LatencyMS = tp.GetLatency()
		}
		info.Legs = append(info.Legs, leg)
	}
	return info
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

// Write writes payload to a RouteGroup
// For the first version, only the first ForwardRule (fwd[0]) is used for writing.
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

	rg.mu.Lock()
	tp, rule, leg, err := rg.nextTransport(p)
	if err != nil {
		rg.mu.Unlock()
		return 0, err
	}
	rg.mu.Unlock()

	return rg.write(p, tp, rule, leg)
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

	rg.mu.Lock()
	defer rg.mu.Unlock()

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
		packet, _, err = rg.mux.wrapPayload(rule.NextRouteID(), data)
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

		atomic.StoreInt64(&rg.lastSent, time.Now().UnixNano())

		// Per-mux-leg sent counter. The aggregate networkStats is
		// already updated inside writePacket (and gates on packet
		// type); this records the same packet against the specific
		// leg that carried it so 'proxy mux-info' can show
		// where bandwidth is going across the mux'd routes.
		if rg.mux != nil && leg >= 0 {
			rg.mux.recordSent(leg, uint64(packet.Size()))
		}

		return len(data), nil
	}
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

// SetLegChangeHook attaches a leg-change callback to the route
// group. Called once at construction time (from saveRouteGroupRules)
// by the dialing side; accept-side route groups are unaffected
// because they don't run policies.
func (rg *RouteGroup) SetLegChangeHook(hook LegChangeHook, info DialInfo) {
	rg.mu.Lock()
	rg.legChangeHook = hook
	rg.legChangeInfo = info
	rg.mu.Unlock()
}

// SetRotation attaches the rotation hook + apply-add callback +
// tick interval. Called once at construction time when the
// policy's decide_route returned a non-zero
// RotationIntervalSeconds. The actual rotation goroutine starts
// via startOffServiceLoops; calling SetRotation after that point
// is racy and unsupported.
//
// applyAdd is the router-side callback that dials one more aux
// forward leg with the policy's ExcludeHops as the disjoint-
// intermediate filter. It runs in the rotation goroutine's
// own context so a slow setup-node dial doesn't block other
// route groups' rotation.
func (rg *RouteGroup) SetRotation(hook RotationHook, applyAdd func(excludeHops []string), interval time.Duration) {
	rg.mu.Lock()
	rg.rotationHook = hook
	rg.rotationApplyAdd = applyAdd
	rg.rotationInterval = interval
	rg.mu.Unlock()
	// Start the rotation goroutine now (startOffServiceLoops fired
	// during initial setup before SetRotation was called, so the
	// conditional in startOffServiceLoops saw zero values and didn't
	// spawn this loop). Safe to call here because the loop checks
	// the same close channels (rg.closed / rg.remoteClosed) that
	// startOffServiceLoops' loops do — a Close() racing with
	// SetRotation just terminates the loop immediately.
	if interval > 0 && hook != nil {
		go rg.servicePacketLoop("rotation", interval, rg.rotationServiceFn)
	}
}

// SetSelfHeal wires the background degree-restoration callback for a
// multiplexed route group. When a leg is pruned and the live degree drops
// below target, the route group dials replacement aux legs (via applyAdd)
// without blocking traffic — surviving legs carry the load meanwhile. target
// is the requested mux degree (legs the group should maintain). A target of
// 0 or 1 disables self-heal (a single-leg group has nothing to spread to).
func (rg *RouteGroup) SetSelfHeal(applyAdd func(excludeHops []string), target int) {
	rg.mu.Lock()
	rg.selfHealAdd = applyAdd
	rg.selfHealTarget = target
	rg.mu.Unlock()
}

// aliveLegCount returns the number of live legs (non-nil, unclosed
// transports). Caller must NOT hold rg.mu.
func (rg *RouteGroup) aliveLegCount() int {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	n := 0
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			n++
		}
	}
	return n
}

// maybeSelfHeal restores the multiplexed degree after a leg drop. If the live
// leg count fell below target and no replacement is already in flight, it
// dials replacement aux legs in the background until the degree is restored
// (or a bounded number of attempts is exhausted, so a destination with no
// remaining disjoint paths can't loop forever). Non-blocking: surviving legs
// keep carrying traffic while replacements set up. healInFlight bounds this to
// one concurrent heal so a flapping leg can't spawn a storm of setup dials.
//
// Must NOT be called while holding rg.mu.
func (rg *RouteGroup) maybeSelfHeal() {
	rg.mu.Lock()
	add := rg.selfHealAdd
	target := rg.selfHealTarget
	rg.mu.Unlock()
	if add == nil || target <= 1 || rg.isClosed() {
		return
	}
	if rg.aliveLegCount() >= target {
		return
	}
	if !rg.healInFlight.CompareAndSwap(false, true) {
		return // a replacement is already dialing
	}
	go func() {
		defer rg.healInFlight.Store(false)
		// Each add(nil) blocks ~one setup-node dial and, on success,
		// appends one leg. Re-check the live count between attempts and
		// stop as soon as the degree is restored, the group closes, or we
		// hit the attempt cap (target+1 gives a little headroom for dials
		// that fail on a bad intermediate before one lands).
		for attempt := 0; attempt < target+1; attempt++ {
			if rg.isClosed() || rg.aliveLegCount() >= target {
				return
			}
			if rg.logger != nil {
				rg.logger.WithField("alive", rg.aliveLegCount()).
					WithField("target", target).
					Debug("Mux self-heal: dialing replacement leg to restore degree")
			}
			add(nil)
		}
	}()
}

// snapshotLegs builds a []LegInfo from the current rg.tps. Caller
// must hold rg.mu. Used by the leg-change fire path to give the
// policy script a snapshot it can iterate.
func (rg *RouteGroup) snapshotLegs() []LegInfo {
	// Intermediate-PK chain shared by all legs of this route group:
	// the forward path's hop To-PKs minus the final destination. The
	// primary leg (index 0) reflects the originally-calculated path;
	// aux legs added by rotation use disjoint intermediates of their
	// own, but the route group only records the primary forwardHops,
	// so this is a best-effort hint the policy can use to build an
	// ExcludeHops set. Empty for a direct (0-intermediate) route.
	var sharedHops []string
	if len(rg.forwardHops) > 1 {
		sharedHops = make([]string, 0, len(rg.forwardHops)-1)
		for _, h := range rg.forwardHops[:len(rg.forwardHops)-1] {
			sharedHops = append(sharedHops, h.To.String())
		}
	}

	legs := make([]LegInfo, 0, len(rg.tps))
	for i, tp := range rg.tps {
		l := LegInfo{Index: i, Alive: false}
		if tp != nil {
			l.Kind = string(tp.Entry.Type)
			l.TransportID = tp.Entry.ID.String()
			l.Alive = !tp.IsClosed()
			if stats := tp.GetLatencyStats(); stats.Avg > 0 {
				l.LatencyMs = int(stats.Avg)
			}
			if bw := tp.GetBandwidth(); bw != nil {
				l.SentBytes = bw.SentBytes
				l.RecvBytes = bw.RecvBytes
			}
			if rg.mux != nil {
				l.Retransmits = rg.mux.retransmitsAt(i)
			}
			l.Hops = sharedHops
		}
		legs = append(legs, l)
	}
	return legs
}

// fireLegChange calls the policy's OnLegChange hook (if any) and
// applies the returned DistributionConfig. Must NOT be called
// while holding rg.mu — applyDistribution acquires it.
//
// Called from leg-mutation paths: appendForwardLeg, appendRules
// (added events), and the leg-prune path inside the read loop
// (dropped events).
func (rg *RouteGroup) fireLegChange(event string, legIdx int) {
	hook := rg.legChangeHook
	info := rg.legChangeInfo
	if hook == nil {
		return
	}
	rg.mu.Lock()
	legs := rg.snapshotLegs()
	rg.mu.Unlock()
	change := LegChange{Event: event, LegIndex: legIdx}
	cfg := hook.OnLegChange(info, legs, change)
	if cfg.Mode == DistributionUnset {
		return
	}
	if rg.logger != nil {
		rg.logger.
			WithField("event", event).
			WithField("leg_index", legIdx).
			WithField("new_distribution", cfg.Mode.String()).
			Debug("Routing policy on_leg_change reconfigured distribution.")
	}
	rg.applyDistribution(cfg)
}

// applyDistribution configures the route group's transport
// selector from a DistributionConfig (typically supplied by a
// routing-policy script via DialAdjustment.Distribution). No-op
// when mux is unset or Mode is DistributionUnset.
//
// Idempotent — calling with the same config is cheap. Called
// after establishMuxRoutes so the distribution applies to every
// leg, including ones added by the mux loop.
func (rg *RouteGroup) applyDistribution(cfg DistributionConfig) {
	if cfg.Mode == DistributionUnset {
		return
	}
	if rg.mux == nil || rg.mux.tpSelector == nil {
		// Policy asked for a distribution but the route group is
		// single-leg (peer didn't negotiate CapMux, or mux loop
		// failed). Distribution is meaningless without multiple
		// legs; log at debug so operators can correlate "I set
		// a distribution and nothing happened" with the actual
		// reason.
		if rg.logger != nil {
			rg.logger.
				WithField("distribution", cfg.Mode.String()).
				Debug("Route group distribution skipped: not mux-enabled (single leg).")
		}
		return
	}
	var wm WeightMode
	switch cfg.Mode {
	case DistributionRoundRobin:
		wm = WeightModeEqual
	case DistributionAuto:
		wm = WeightModeAuto
	case DistributionWeighted:
		wm = WeightModeExplicit
		rg.mux.tpSelector.SetExplicitWeights(cfg.Weights)
	case DistributionSizeThreshold:
		wm = WeightModeSizeThreshold
		rg.mux.tpSelector.SetSizeThreshold(cfg.SizeThreshold)
	case DistributionSticky5Tuple:
		wm = WeightModeSticky5Tuple
	case DistributionLatencyAdaptive:
		wm = WeightModeLatencyAdaptive
	case DistributionDSCPPriority:
		wm = WeightModeDSCPPriority
		rg.mux.tpSelector.SetDSCPThreshold(cfg.DSCPThreshold)
	default:
		return
	}
	rg.mu.Lock()
	rg.mux.tpSelector.SetMode(wm)
	rg.mux.tpSelector.Rebuild(rg.tps)
	legs := len(rg.tps)
	rg.mu.Unlock()
	// Weighted mode silently substitutes weight=1 for missing
	// entries when len(weights) < legs and ignores trailing
	// weights when len(weights) > legs. Surface the mismatch
	// at warn so operators can correlate "my schedule isn't
	// what I asked for" with a script/leg-count discrepancy.
	if rg.logger != nil && cfg.Mode == DistributionWeighted && len(cfg.Weights) != legs {
		rg.logger.
			WithField("weights_len", len(cfg.Weights)).
			WithField("legs", legs).
			Warn("Routing policy weighted distribution: weight count != leg count. " +
				"Missing weights default to 1; trailing weights are ignored.")
	}
	if rg.logger != nil {
		entry := rg.logger.
			WithField("distribution", cfg.Mode.String()).
			WithField("legs", legs)
		if cfg.Mode == DistributionWeighted {
			entry = entry.WithField("weights", cfg.Weights)
		}
		if cfg.Mode == DistributionSizeThreshold {
			entry = entry.WithField("size_threshold", cfg.SizeThreshold)
		}
		entry.Debug("Route group distribution applied.")
	}
}

// nextTransport selects the next transport/rule pair. When mux is enabled and
// multiple transports exist, uses latency-weighted selection. Falls back to
// round-robin when latency data is unavailable, and to index 0 for single
// transport (legacy behavior).
//
// payload is the upcoming packet's payload bytes (or nil for
// control / retx paths). Used by payload-inspecting modes
// (size-threshold, sticky:5tuple, latency-adaptive,
// dscp-priority); other modes ignore it.
//
// Returns the leg index (position in rg.tps) so the caller can record
// per-leg byte counts after a successful write; -1 for the
// single-transport / legacy path.
// NOTE: not thread-safe, caller must hold rg.mu.
func (rg *RouteGroup) nextTransport(payload []byte) (*transport.ManagedTransport, routing.Rule, int, error) {
	if len(rg.tps) == 0 {
		return nil, nil, -1, ErrNoTransports
	}
	if len(rg.fwd) == 0 {
		return nil, nil, -1, ErrNoRules
	}
	if len(rg.tps) != len(rg.fwd) {
		return nil, nil, -1, ErrRuleTransportMismatch
	}

	if rg.mux != nil && len(rg.tps) > 1 {
		return rg.mux.selectTransport(rg.tps, rg.fwd, payload)
	}

	if rg.tps[0] == nil {
		return nil, nil, -1, ErrBadTransport
	}
	return rg.tps[0], rg.fwd[0], 0, nil
}

// RouteHops returns the list of visor public keys that form the route path.
// The first element is the first hop from the source, and the last element
// is the destination visor.
func (rg *RouteGroup) RouteHops() []cipher.PubKey {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	hops := make([]cipher.PubKey, 0, len(rg.tps)+1)
	for _, tp := range rg.tps {
		if tp != nil {
			hops = append(hops, tp.Remote())
		}
	}
	// Add destination from the route descriptor
	hops = append(hops, rg.desc.DstPK())
	return hops
}

// RouteHopInfo contains detailed information about a single hop in a route.
type RouteHopInfo struct {
	TpID   string `json:"tp_id"`   // Transport ID
	From   string `json:"from"`    // Source public key
	To     string `json:"to"`      // Destination public key
	TpType string `json:"tp_type"` // Transport type (stcpr, sudph, dmsg)
}

// SetForwardHops sets the complete forward route hops.
// This should be called after route setup to store the full route path.
func (rg *RouteGroup) SetForwardHops(hops []routing.Hop) {
	rg.mu.Lock()
	defer rg.mu.Unlock()
	rg.forwardHops = hops
}

// Initiator reports whether this visor dialed the remote end of the route
// (true) or accepted the route from a setup-node request (false).
func (rg *RouteGroup) Initiator() bool {
	return rg.initiator
}

// RouteHopDetails returns detailed information about each hop in the route,
// including transport IDs and types.
func (rg *RouteGroup) RouteHopDetails() []RouteHopInfo {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	// Use stored forward hops if available (preferred - has complete route)
	if len(rg.forwardHops) > 0 {
		hops := make([]RouteHopInfo, len(rg.forwardHops))
		for i, hop := range rg.forwardHops {
			// Derive transport type from the transport ID
			// The ID is deterministically generated from (keyA, keyB, type)
			tpType := transport.TypeFromTransportID(hop.TpID, hop.From, hop.To)
			hops[i] = RouteHopInfo{
				TpID:   hop.TpID.String(),
				From:   hop.From.String(),
				To:     hop.To.String(),
				TpType: string(tpType),
			}
		}
		return hops
	}

	// Fallback: reconstruct from local transports (may be incomplete for multi-hop)
	srcPK := rg.desc.SrcPK()
	hops := make([]RouteHopInfo, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil {
			continue
		}
		var fromPK cipher.PubKey
		if i == 0 {
			fromPK = srcPK
		} else if i > 0 && rg.tps[i-1] != nil {
			fromPK = rg.tps[i-1].Remote()
		}
		hops = append(hops, RouteHopInfo{
			TpID:   tp.Entry.ID.String(),
			From:   fromPK.String(),
			To:     tp.Remote().String(),
			TpType: string(tp.Type()),
		})
	}
	return hops
}

func (rg *RouteGroup) startOffServiceLoops() {
	go rg.servicePacketLoop("keep-alive", rg.cfg.KeepAliveInterval, rg.keepAliveServiceFn)
	// Per-leg end-to-end liveness (issue #2): detect mux legs that black-hole
	// BEYOND the first hop (invisible to pruneDeadTransports' local tp.IsClosed
	// check) and drop them so self-heal re-dials a live replacement.
	go rg.servicePacketLoop("leg-liveness", legLivenessInterval, rg.legLivenessServiceFn)
	// Note: Automatic ping loop removed. Latency is now measured once at transport creation.
	// Rotation loop is NOT started here — startOffServiceLoops runs
	// during initial route-group setup, before the router-side
	// SetRotation call wires the hook + interval. SetRotation spawns
	// the rotation goroutine itself.
}

// rotationServiceFn fires the policy's on_tick hook against the
// current leg snapshot and applies the returned action: drop
// the listed leg indices, then optionally request one fresh aux
// leg via the router-supplied applyAdd callback.
//
// Drops apply before the add so a "drop oldest + add new" policy
// produces a clean rotation (one in, one out). The add runs
// synchronously here — a slow setup-node dial blocks the next
// tick for THIS route group only (other groups have their own
// rotation goroutines).
func (rg *RouteGroup) rotationServiceFn(_ time.Duration) {
	rg.mu.Lock()
	hook := rg.rotationHook
	applyAdd := rg.rotationApplyAdd
	info := rg.legChangeInfo
	legs := rg.snapshotLegs()
	rg.mu.Unlock()
	if hook == nil {
		return
	}
	action := hook.OnTick(info, legs)
	if len(action.DropLegs) == 0 && !action.AddLeg {
		return
	}

	if len(action.DropLegs) > 0 {
		rg.dropLegsByIndex(action.DropLegs)
	}

	if action.AddLeg && applyAdd != nil {
		applyAdd(action.ExcludeHops)
	}
}

// dropLegsByIndex closes the transports at the policy-supplied
// indices (re-mapped to current live legs) then triggers a prune
// so the slice compacts and OnLegChange fires. Bounded: never
// drops the last alive leg — the prune itself enforces that
// invariant, but we skip the close too so we don't briefly
// orphan a route group with no transports.
//
// Indices come from the policy's view of the leg slice at tick
// time; the snapshot the policy saw might be stale by the time
// we mutate (concurrent prune from keep-alive loop), so we treat
// out-of-range indices as no-ops rather than errors.
func (rg *RouteGroup) dropLegsByIndex(indices []int) {
	rg.mu.Lock()
	aliveCount := 0
	for _, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			aliveCount++
		}
	}
	closed := make([]int, 0, len(indices))
	for _, idx := range indices {
		if idx < 0 || idx >= len(rg.tps) {
			continue
		}
		tp := rg.tps[idx]
		if tp == nil || tp.IsClosed() {
			continue
		}
		if aliveCount <= 1 {
			// Refuse to drop the last alive leg — pruneDeadTransports
			// also refuses, but skipping the close avoids the brief
			// window where the leg is closed but not yet pruned.
			break
		}
		_ = tp.Close() //nolint:errcheck
		aliveCount--
		closed = append(closed, idx)
	}
	droppedIdx := rg.pruneDeadTransports()
	rg.mu.Unlock()
	for _, idx := range droppedIdx {
		rg.fireLegChange("dropped", idx)
	}
	if len(droppedIdx) > 0 {
		rg.maybeSelfHeal()
	}
	if rg.logger != nil && len(closed) > 0 {
		rg.logger.
			WithField("dropped_indices", closed).
			Debug("Rotation hook dropped legs.")
	}
}

func (rg *RouteGroup) sendPing() error {
	rg.mu.Lock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		// if no transports, no rules, then no latency probe
		return nil
	}

	tp := rg.tps[0]
	rule := rg.fwd[0]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	throughput := rg.networkStats.RemoteThroughput()
	timestamp := time.Now().UTC().UnixNano() / int64(time.Millisecond)
	rg.networkStats.SetDownloadSpeed(uint32(throughput)) //nolint: gosec

	packet := routing.MakePingPacket(rule.NextRouteID(), timestamp, throughput)

	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
}

func (rg *RouteGroup) sendPong(timestamp int64) error {
	rg.mu.Lock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		// if no transports, no rules, then no latency probe
		return nil
	}

	tp := rg.tps[0]
	rule := rg.fwd[0]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	packet := routing.MakePongPacket(rule.NextRouteID(), timestamp)

	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
}

// legLivenessServiceFn probes each multiplexed leg end-to-end once per tick and
// drops any leg that has missed legPongMissThreshold consecutive echoes. It
// exists because pruneDeadTransports only detects a dead LOCAL (first-hop)
// transport; a transport dying beyond the first hop leaves the local tp open,
// so the leg silently black-holes and the mux keeps selecting it. The probe
// reuses the existing Ping→Pong echo (the destination echoes the send-timestamp
// verbatim), so it is purely source-side: no wire-format or remote-version
// dependency. Single-leg groups are skipped (no failover target, never drop the
// last leg), so the common case carries zero overhead.
func (rg *RouteGroup) legLivenessServiceFn(_ time.Duration) {
	if rg.isClosed() {
		return
	}

	type legProbe struct {
		tp   *transport.ManagedTransport
		rule routing.Rule
		id   uuid.UUID
	}
	rg.mu.Lock()
	probes := make([]legProbe, 0, len(rg.tps))
	for i, tp := range rg.tps {
		if tp == nil || tp.IsClosed() || i >= len(rg.fwd) {
			continue
		}
		probes = append(probes, legProbe{tp: tp, rule: rg.fwd[i], id: tp.Entry.ID})
	}
	rg.mu.Unlock()

	if len(probes) < 2 {
		return
	}

	live := make(map[uuid.UUID]struct{}, len(probes))
	for _, p := range probes {
		live[p.id] = struct{}{}
	}

	// Tally the previous cycle's echoes; a leg already probed that missed
	// legPongMissThreshold consecutive cycles is declared black-holing.
	var dead []uuid.UUID
	rg.legLivenessMu.Lock()
	for _, p := range probes {
		if rg.legPongSeen[p.id] {
			rg.legMissed[p.id] = 0
		} else if _, probed := rg.legMissed[p.id]; probed {
			rg.legMissed[p.id]++
			if rg.legMissed[p.id] >= legPongMissThreshold {
				dead = append(dead, p.id)
			}
		}
		rg.legPongSeen[p.id] = false
	}
	for id := range rg.legMissed {
		if _, ok := live[id]; !ok {
			delete(rg.legMissed, id)
			delete(rg.legPongSeen, id)
		}
	}
	staleBeforeMs := time.Now().UTC().UnixNano()/int64(time.Millisecond) -
		int64(legPongMissThreshold+2)*legLivenessInterval.Milliseconds()
	for ts := range rg.inflightPings {
		if ts < staleBeforeMs {
			delete(rg.inflightPings, ts)
		}
	}
	rg.legLivenessMu.Unlock()

	if len(dead) > 0 {
		rg.pruneLivenessDeadLegs(dead)
	}

	// Probe the surviving legs for the next cycle. Each send-ts is unique
	// (ms clock + per-leg offset) so two legs probed in the same millisecond
	// don't collide as inflightPings keys; the destination echoes it verbatim.
	deadSet := make(map[uuid.UUID]struct{}, len(dead))
	for _, d := range dead {
		deadSet[d] = struct{}{}
	}
	baseMs := time.Now().UTC().UnixNano() / int64(time.Millisecond)
	for offset, p := range probes {
		if _, isDead := deadSet[p.id]; isDead {
			continue
		}
		ts := baseMs + int64(offset)
		rg.legLivenessMu.Lock()
		rg.inflightPings[ts] = p.id
		if _, ok := rg.legMissed[p.id]; !ok {
			rg.legMissed[p.id] = 0 // mark probed so the next cycle counts misses
		}
		rg.legLivenessMu.Unlock()
		throughput := rg.networkStats.RemoteThroughput()
		packet := routing.MakePingPacket(p.rule.NextRouteID(), ts, throughput)
		if err := rg.writePacket(context.Background(), p.tp, packet, p.rule.KeyRouteID()); err != nil {
			rg.logger.WithError(err).Debugf("leg-liveness: probe write failed on leg %s", p.id)
		}
	}
}

// pruneLivenessDeadLegs drops the given black-holing legs (by transport ID) from
// the mux WITHOUT closing their (possibly shared) transports, deletes their
// local rules, compacts the mux, then triggers self-heal. It never drops below
// one leg, so a false positive at worst causes a self-heal re-dial — never an
// outage. Mirrors pruneDeadTransports' removal, selecting by liveness instead
// of tp.IsClosed().
func (rg *RouteGroup) pruneLivenessDeadLegs(deadIDs []uuid.UUID) {
	deadSet := make(map[uuid.UUID]struct{}, len(deadIDs))
	for _, id := range deadIDs {
		deadSet[id] = struct{}{}
	}

	rg.mu.Lock()
	if len(rg.tps) <= 1 {
		rg.mu.Unlock()
		return
	}
	aliveTps := make([]*transport.ManagedTransport, 0, len(rg.tps))
	aliveFwd := make([]routing.Rule, 0, len(rg.fwd))
	aliveRvs := make([]routing.Rule, 0, len(rg.rvs))
	var deadRuleIDs []routing.RouteID
	var droppedIdx []int
	remaining := len(rg.tps)
	for i, tp := range rg.tps {
		isDead := false
		if tp != nil {
			_, isDead = deadSet[tp.Entry.ID]
		}
		if isDead && remaining > 1 {
			if i < len(rg.fwd) {
				deadRuleIDs = append(deadRuleIDs, rg.fwd[i].KeyRouteID())
			}
			if i < len(rg.rvs) {
				deadRuleIDs = append(deadRuleIDs, rg.rvs[i].KeyRouteID())
			}
			if tp != nil {
				rg.logger.Infof("leg-liveness: pruning black-holing leg %v (no echo for %d probes; transport stays up)",
					tp.Entry.ID, legPongMissThreshold)
			}
			droppedIdx = append(droppedIdx, i)
			remaining--
			continue
		}
		aliveTps = append(aliveTps, tp)
		if i < len(rg.fwd) {
			aliveFwd = append(aliveFwd, rg.fwd[i])
		}
		if i < len(rg.rvs) {
			aliveRvs = append(aliveRvs, rg.rvs[i])
		}
	}
	if len(droppedIdx) == 0 {
		rg.mu.Unlock()
		return
	}
	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}
	rg.tps = aliveTps
	rg.fwd = aliveFwd
	rg.rvs = aliveRvs
	if rg.mux != nil {
		rg.mux.removeLegs(droppedIdx...)
	}
	rg.mu.Unlock()

	for _, idx := range droppedIdx {
		rg.fireLegChange("liveness-dead", idx)
	}
	rg.maybeSelfHeal()
}

func (rg *RouteGroup) servicePacketLoop(name string, interval time.Duration, f sendServicePacketFn) {
	if interval <= 0 {
		// No keep-alive — routes persist indefinitely. Just wait for close.
		select {
		case <-rg.remoteClosed:
		case <-rg.closed:
		}
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rg.remoteClosed:
			rg.logger.Debugf("Remote got closed, stopping %s loop", name)
			return
		case <-rg.closed:
			rg.logger.Debugf("RouteGroup closed, stopping %s loop", name)
			return
		case <-ticker.C:
			f(interval)
		}
	}
}

func (rg *RouteGroup) keepAliveServiceFn(interval time.Duration) {
	lastSent := time.Unix(0, atomic.LoadInt64(&rg.lastSent))

	if time.Since(lastSent) < interval {
		return
	}

	if err := rg.sendKeepAlive(); err != nil {
		failures := atomic.AddInt32(&rg.consecutiveWriteFailures, 1)
		if failures >= maxConsecutiveWriteFailures {
			rg.logger.Warnf("Closing RouteGroup after %d consecutive write failures: %v", failures, err)
			go func() { rg.Close() }() //nolint:errcheck,gosec
			return
		}
		rg.logger.Warnf("Failed to send keepalive: %v", err)
	} else {
		atomic.StoreInt32(&rg.consecutiveWriteFailures, 0)
	}

	// Prune dead transports and rebuild weights
	var droppedLegs []int
	if rg.mux != nil {
		rg.mu.Lock()
		droppedLegs = rg.pruneDeadTransports()
		if len(rg.tps) > 1 {
			rg.mux.rebuildWeights(rg.tps)
		}
		rg.mu.Unlock()
	}
	// Fire the policy hook outside the lock so the script can
	// safely call back into rg methods (the on_leg_change script
	// returning a new distribution causes applyDistribution which
	// re-takes rg.mu).
	for _, idx := range droppedLegs {
		rg.fireLegChange("dropped", idx)
	}
	if len(droppedLegs) > 0 {
		rg.maybeSelfHeal()
	}
}

// pruneDeadTransports removes closed transports from the mux.
// Cleans up the corresponding forward/reverse rules from the routing table.
// Must be called with rg.mu held. Returns the original indexes
// of legs that were pruned, so the caller can fire OnLegChange
// events for them after releasing the lock.
func (rg *RouteGroup) pruneDeadTransports() []int {
	if len(rg.tps) <= 1 {
		return nil // Don't prune the last transport
	}

	aliveTps := make([]*transport.ManagedTransport, 0, len(rg.tps))
	aliveFwd := make([]routing.Rule, 0, len(rg.fwd))
	aliveRvs := make([]routing.Rule, 0, len(rg.rvs))
	var deadRuleIDs []routing.RouteID
	var droppedIdx []int

	for i, tp := range rg.tps {
		if tp != nil && !tp.IsClosed() {
			aliveTps = append(aliveTps, tp)
			if i < len(rg.fwd) {
				aliveFwd = append(aliveFwd, rg.fwd[i])
			}
			if i < len(rg.rvs) {
				aliveRvs = append(aliveRvs, rg.rvs[i])
			}
		} else {
			if i < len(rg.fwd) {
				deadRuleIDs = append(deadRuleIDs, rg.fwd[i].KeyRouteID())
			}
			if i < len(rg.rvs) {
				deadRuleIDs = append(deadRuleIDs, rg.rvs[i].KeyRouteID())
			}
			if tp != nil {
				rg.logger.Infof("Pruning dead mux transport %v", tp.Entry.ID)
			}
			droppedIdx = append(droppedIdx, i)
		}
	}

	if len(aliveTps) == len(rg.tps) {
		return nil // Nothing was pruned
	}

	// Clean up dead rules from routing table
	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}

	rg.tps = aliveTps
	rg.fwd = aliveFwd
	rg.rvs = aliveRvs

	// Compact the per-leg counter/readiness arrays in lockstep so they stay
	// aligned with the pruned tps[]/fwd[]/rvs[]. droppedIdx are the original
	// pre-compaction indices.
	if rg.mux != nil {
		rg.mux.removeLegs(droppedIdx...)
	}

	rg.logger.Infof("Pruned dead transports: %d alive, %d removed", len(aliveTps), len(deadRuleIDs)/2)
	return droppedIdx
}

// pruneLegByConsumeRule removes the single mux leg whose consume (reverse) rule
// matches routeID, WITHOUT closing the route group — the endpoint counterpart to
// a remote peer retiring one leg (routing.CloseLegRetired). It reclaims that
// leg's forward+consume rules and drops it from the mux, leaving the group and
// its other legs live. Returns true if a leg was pruned; false if this is the
// group's last leg (or the rule wasn't found), in which case the caller falls
// back to a normal whole-group close. Mirrors pruneDeadTransports' slice/rule
// bookkeeping; tolerates pruning index 0 exactly as the dead-transport path does
// (the mux selector is rebuilt and tps[0]-hardcoded probes re-target the new
// primary leg).
func (rg *RouteGroup) pruneLegByConsumeRule(routeID routing.RouteID) bool {
	rg.mu.Lock()
	if len(rg.tps) <= 1 || len(rg.rvs) <= 1 {
		rg.mu.Unlock()
		return false // last leg — let the caller do a full close
	}
	idx := -1
	for i, rv := range rg.rvs {
		if rv != nil && rv.KeyRouteID() == routeID {
			idx = i
			break
		}
	}
	if idx < 0 {
		rg.mu.Unlock()
		return false
	}

	var deadRuleIDs []routing.RouteID
	if idx < len(rg.fwd) {
		deadRuleIDs = append(deadRuleIDs, rg.fwd[idx].KeyRouteID())
	}
	if idx < len(rg.rvs) {
		deadRuleIDs = append(deadRuleIDs, rg.rvs[idx].KeyRouteID())
	}

	if idx < len(rg.tps) {
		rg.tps = append(rg.tps[:idx], rg.tps[idx+1:]...)
	}
	if idx < len(rg.fwd) {
		rg.fwd = append(rg.fwd[:idx], rg.fwd[idx+1:]...)
	}
	if idx < len(rg.rvs) {
		rg.rvs = append(rg.rvs[:idx], rg.rvs[idx+1:]...)
	}

	if len(deadRuleIDs) > 0 {
		rg.rt.DelRules(deadRuleIDs)
	}
	if rg.mux != nil {
		rg.mux.removeLegs(idx)
		rg.mux.rebuildWeights(rg.tps)
	}
	rg.logger.Infof("Retired remote mux leg %d (consume rule %d); %d legs remain", idx, routeID, len(rg.tps))
	rg.mu.Unlock()

	rg.fireLegChange("retired-remote", idx)
	return true
}

func (rg *RouteGroup) sendKeepAlive() error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	// Track successes — in mux mode, only fail if ALL transports are dead.
	// A single dead transport in a mux group should not kill the entire connection.
	anySuccess := false
	var lastErr error

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]
		rule := rg.fwd[i]

		if tp == nil || tp.IsClosed() {
			continue
		}

		packet := routing.MakeKeepAlivePacket(rule.NextRouteID())

		if err := rg.writePacket(ctx, tp, packet, rule.KeyRouteID()); err != nil {
			lastErr = err
			rg.logger.Debugf("Keepalive failed on transport %v: %v", tp.Entry.ID, err)
			continue
		}
		anySuccess = true
	}

	if !anySuccess {
		if lastErr != nil {
			return lastErr
		}
		return ErrNoSuitableTransport
	}

	return nil
}

func (rg *RouteGroup) sendHandshake(encrypt bool) error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]

		if tp == nil {
			continue
		}

		rule := rg.fwd[i]
		packet := routing.MakeHandshakePacket(rule.NextRouteID(), encrypt, routing.CapMux|routing.CapSACK)

		err := rg.writePacket(ctx, tp, packet, rule.KeyRouteID())
		if err == nil {
			rg.logger.Debugf("Sent handshake via transport %v", tp.Entry.ID)
			return nil
		}

		rg.logger.Debugf("Failed to send handshake via transport %v: %v [%v/%v]",
			tp.Entry.ID, err, i+1, len(rg.tps))
	}

	return ErrNoSuitableTransport
}

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

	if len(rg.fwd) != len(rg.tps) {
		return ErrRuleTransportMismatch
	}

	closeInitiator := rg.isCloseInitiator()

	if closeInitiator {
		// will wait for close response from all the transports
		atomic.StoreInt32(&rg.closeDonePending, int32(len(rg.tps))) //nolint:gosec
		rg.closeDoneCh = make(chan struct{})
	}

	rg.broadcastClosePackets(code)

	if closeInitiator {
		// if this visor initiated closing, we need to wait for close packets
		// to come back, or to exit with a timeout if anything goes wrong in
		// the network
		if err := rg.waitForCloseRouteGroup(closeRoutineTimeout); err != nil {
			rg.logger.Errorf("Error during close route group: %v", err)
		}
	}

	rules := make([]routing.RouteID, 0, len(rg.fwd)+len(rg.rvs))
	for _, r := range rg.fwd {
		rules = append(rules, r.KeyRouteID())
	}
	// Also delete the reverse/consume rules. Without this they linger in the
	// routing table until the keep-alive GC reaps them, leaking N stale rules
	// per close (N legs for a mux group) and opening the window where a packet
	// resolves the orphaned consume rule but finds no route group
	// (errRouteDescNotExist log-spam).
	for _, r := range rg.rvs {
		rules = append(rules, r.KeyRouteID())
	}

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

func (rg *RouteGroup) handlePacket(packet routing.Packet) error {
	switch packet.Type() {
	case routing.ClosePacket:
		closeCode, err := closeCodeFromPacket(packet)
		if err != nil {
			return err
		}
		return rg.handleClosePacket(closeCode)
	case routing.DataPacket:
		rg.handshakeProcessedOnce.Do(func() {
			// first packet is data packet, so we're communicating with the old visor
			rg.encrypt = false
			close(rg.handshakeProcessed)
		})
		return rg.handleDataPacket(packet)
	case routing.HandshakePacket:
		// A handshake on an aux leg proves the peer registered that leg's
		// rule, so it is safe to start sending on it. The primary leg's
		// handshake (which creates the mux below) needs no marking — leg 0
		// is ready from growLegs. This matters most for download-heavy
		// flows: the bulk-sending side would otherwise never receive data
		// on its aux legs and so never learn it may spread onto them.
		if rg.mux != nil {
			rg.mu.Lock()
			rid := packet.RouteID()
			for i, rule := range rg.rvs {
				if rule != nil && rule.KeyRouteID() == rid {
					rg.mux.markLegReady(i)
					break
				}
			}
			rg.mu.Unlock()
		}
		rg.handshakeProcessedOnce.Do(func() {
			// first packet is handshake packet, so we're communicating with the new visor
			rg.encrypt = true
			if packet.Payload()[0] == 0 {
				rg.encrypt = false
			}

			// Extended capabilities negotiation
			remoteCaps := packet.HandshakeCapabilities()
			if remoteCaps&routing.CapMux != 0 {
				sack := remoteCaps&routing.CapSACK != 0
				rg.mux = newRouteMux(rg.logger, sack)
				// rg.tps already has the primary transport at this
				// point; size the leg counters to match. Subsequent
				// AppendRoute calls (mux 2/N+) extend the slice via
				// appendRules.
				rg.mux.growLegs(len(rg.tps))
				rg.logger.Debug("Route multiplexing enabled (both peers support CapMux)")
				if sack {
					rg.logger.Debug("SACK retransmission enabled (both peers support CapSACK)")
					go rg.servicePacketLoop("sack", rg.cfg.KeepAliveInterval/2, rg.sackServiceFn)
				}
			}

			close(rg.handshakeProcessed)
		})
	case routing.PingPacket:
		return rg.handlePingPacket(packet)
	case routing.PongPacket:
		return rg.handlePongPacket(packet)
	case routing.ErrorPacket:
		return rg.handleErrorPacket(packet)
	case routing.SACKPacket:
		return rg.handleSACKPacket(packet)
	}

	return nil
}

func (rg *RouteGroup) handleDataPacket(packet routing.Packet) (err error) {
	// in this case remote is already closed, and `readCh` is closed too,
	// but some packets may still reach the rg causing panic on writing
	// to `readCh`, so we simple omit such packets
	if rg.isRemoteClosed() {
		return nil
	}

	// Defensive recover. readCh is no longer closed on route-group close (closure
	// is signaled via rg.closed / rg.remoteClosed — see close()), so the old
	// "send on closed channel" panic can no longer occur here. The recover is kept
	// purely as a safety net: an unexpected panic in this hot packet-dispatch path
	// drops the packet and keeps the router goroutine serving other route groups
	// rather than crashing the whole visor.
	defer func() {
		if r := recover(); r != nil {
			rg.logger.WithField("recover", r).Debug("handleDataPacket: recovered from panic")
			err = io.ErrClosedPipe
		}
	}()

	rg.networkStats.AddBandwidthReceived(uint64(packet.Size()))

	// Per-mux-leg recv counter. We resolve the leg from the
	// packet's route ID (which matches one of rg.rvs[].KeyRouteID,
	// each leg has its own consume rule). The lookup is O(legs)
	// but legs is small (typically 1-16) and this only runs for
	// data packets after we've already paid for noise decryption.
	if rg.mux != nil {
		rg.mu.Lock()
		rid := packet.RouteID()
		for i, rule := range rg.rvs {
			if rule != nil && rule.KeyRouteID() == rid {
				rg.mux.recordRecv(i, uint64(packet.Size()))
				// Inbound traffic on leg i proves the peer registered its
				// rule for this leg, so it is now safe to send on it.
				rg.mux.markLegReady(i)
				break
			}
		}
		rg.mu.Unlock()
	}

	if rg.mux != nil {
		seq := packet.SequenceNumber()
		data := packet.DataPayloadAfterSeq()

		delivered, gapDetected := rg.mux.deliverData(seq, data)

		// A gap is the normal case for mux (legs interleave), so rate-limit the
		// SACK: without this, latency-skew reordering fires a SACK goroutine per
		// out-of-order packet — thousands per second under load.
		if gapDetected && rg.mux.shouldSendSACK() {
			go rg.sendSACK() //nolint:errcheck
		}

		for _, d := range delivered {
			// Both rg.closed (local-initiated close) and
			// rg.remoteClosed (remote-initiated close) must be
			// watched here; the latter was previously missing
			// and caused the send-on-closed-channel panic
			// documented in the function-level defer.
			select {
			case <-rg.closed:
				return io.ErrClosedPipe
			case <-rg.remoteClosed:
				return io.ErrClosedPipe
			case rg.readCh <- d:
			case <-time.After(30 * time.Second):
				rg.logger.Warn("Dropping packet: readCh full for 30s (application not reading)")
			}
		}
		return nil
	}

	// Legacy path: deliver payload directly
	select {
	case <-rg.closed:
		return io.ErrClosedPipe
	case <-rg.remoteClosed:
		return io.ErrClosedPipe
	case rg.readCh <- packet.Payload():
	case <-time.After(30 * time.Second):
		rg.logger.Warn("Dropping packet: readCh full for 30s (application not reading)")
	}

	return nil
}

func (rg *RouteGroup) handleErrorPacket(packet routing.Packet) error {

	// in this case remote is already closed, and `readCh` is closed too,
	// but some packets may still reach the rg causing panic on writing
	// to `readCh`, so we simple omit such packets
	if rg.isRemoteClosed() {
		return nil
	}

	rg.SetError(errors.New((string(packet.Payload()))))
	return nil
}

// sendSACK sends a SACK packet with the current receiver state.
func (rg *RouteGroup) sendSACK() error {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return nil
	}

	rg.mu.Lock()
	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		rg.mu.Unlock()
		return nil
	}
	// Use first available transport for SACK (control channel)
	tp := rg.tps[0]
	rule := rg.fwd[0]
	rg.mu.Unlock()

	if tp == nil {
		return nil
	}

	lastContig, bitmap := rg.mux.generateSACK()
	packet := routing.MakeSACKPacket(rule.NextRouteID(), lastContig, bitmap)
	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
}

// handleSACKPacket processes a received SACK and retransmits missing packets.
func (rg *RouteGroup) handleSACKPacket(packet routing.Packet) error {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return nil
	}

	lastContig := packet.SACKLastContiguousSeq()
	bitmap := packet.SACKBitmap()

	retxSeqs := rg.mux.processSACK(lastContig, bitmap)
	if len(retxSeqs) == 0 {
		return nil
	}

	rg.logger.Debugf("SACK: retransmitting %d packets", len(retxSeqs))

	for _, seq := range retxSeqs {
		data := rg.mux.getRetxPayload(seq)
		if data == nil {
			continue
		}

		rg.mu.Lock()
		tp, rule, leg, err := rg.nextTransport(data)
		rg.mu.Unlock()
		if err != nil {
			return err
		}

		retxPacket, err := routing.MakeSequencedDataPacket(rule.NextRouteID(), seq, data)
		if err != nil {
			return err
		}

		if err := rg.writePacket(context.Background(), tp, retxPacket, rule.KeyRouteID()); err != nil {
			rg.logger.WithError(err).Warnf("SACK: failed to retransmit seq %d", seq)
		} else if leg >= 0 {
			// Count retransmits against the leg that carried them
			// (which may differ from the original send leg — the
			// retx selector picks fresh, mirroring the data path).
			rg.mux.recordSent(leg, uint64(retxPacket.Size()))
			rg.mux.recordRetransmit(leg) // separate loss signal for leg health
		}
	}
	return nil
}

// sackServiceFn is the periodic SACK sender, run as a service loop.
func (rg *RouteGroup) sackServiceFn(_ time.Duration) {
	if rg.mux == nil || !rg.mux.sackEnabled {
		return
	}
	if err := rg.sendSACK(); err != nil {
		rg.logger.WithError(err).Warn("Failed to send periodic SACK")
	}
}

func (rg *RouteGroup) handleClosePacket(code routing.CloseCode) error {
	rg.logger.Debugf("Got close packet with code %d", code)

	if rg.isCloseInitiator() {
		// this route group initiated close loop and got response
		rg.logger.Debugf("Handling response close packet with code %d", code)

		if atomic.AddInt32(&rg.closeDonePending, -1) <= 0 {
			select {
			case <-rg.closeDoneCh:
			default:
				close(rg.closeDoneCh)
			}
		}
		return nil
	}

	return rg.close(code)
}

func (rg *RouteGroup) handlePingPacket(packet routing.Packet) error {
	payload := packet.Payload()

	timestamp := binary.BigEndian.Uint64(payload)
	throughput := binary.BigEndian.Uint64(payload[8:])

	rg.logger.WithField("func", "RouteGroup.handlePingPacket").Tracef("Throughput is around %d", throughput)

	rg.networkStats.SetUploadSpeed(uint32(throughput)) //nolint: gosec

	return rg.sendPong(int64(timestamp)) //nolint: gosec
}

func (rg *RouteGroup) handlePongPacket(packet routing.Packet) error {
	payload := packet.Payload()

	sentAtMs := binary.BigEndian.Uint64(payload)

	// Per-leg liveness (issue #2): if this pong echoes one of our outstanding
	// leg-liveness probes, mark that leg alive. A pong arriving AT ALL proves
	// the leg's end-to-end round trip works, independent of the latency value
	// (so this runs before the latency sanity-reject below).
	rg.legLivenessMu.Lock()
	if legID, ok := rg.inflightPings[int64(sentAtMs)]; ok { //nolint: gosec
		rg.legPongSeen[legID] = true
		delete(rg.inflightPings, int64(sentAtMs)) //nolint: gosec
	}
	rg.legLivenessMu.Unlock()

	ms := sentAtMs % 1000
	sentAt := time.Unix(int64(sentAtMs/1000), int64(ms)*int64(time.Millisecond)).UTC() //nolint: gosec

	// Use fractional milliseconds for sub-ms precision (e.g. 1.2 ms)
	latencyMs := float64(time.Now().UTC().Sub(sentAt).Microseconds()) / 1000.0

	rg.logger.WithField("func", "RouteGroup.handlePongPacket").Tracef("Latency is around %.1f ms", latencyMs)

	// A pong correlates to no specific ping (no in-flight tracking), so
	// a long-delayed pong arriving after the host woke / queue drained
	// produces a 30+ second sample. Reject up front so neither
	// networkStats, the synchronous MeasureLatency consumer, nor the
	// underlying transport see the bogus value.
	if latencyMs <= 0 || latencyMs > transport.MaxReasonableRTTMs {
		return nil
	}

	rg.networkStats.SetLatency(uint32(latencyMs)) //nolint: gosec

	// If there's a pending synchronous measurement, send the result
	rg.pendingPongMu.Lock()
	if rg.pendingPongCh != nil {
		select {
		case rg.pendingPongCh <- latencyMs:
		default:
			// Channel full or closed, ignore
		}
	}
	rg.pendingPongMu.Unlock()

	// Propagate ping latency to the underlying transport so it gets
	// reported to TPD during re-registration.
	rg.mu.Lock()
	if len(rg.tps) > 0 && rg.tps[0] != nil {
		rg.tps[0].SetLatency(latencyMs)
	}
	rg.mu.Unlock()

	return nil
}

// MeasureLatency performs multiple ping/pong measurements and returns statistics.
// It sends 'count' pings, waits for pongs, and calculates min/max/avg latency.
// Returns the stats and any error. Partial results are returned if some pings fail.
func (rg *RouteGroup) MeasureLatency(ctx context.Context, count int) (min, max, avg float64, err error) {
	if count <= 0 {
		count = 5 // Default to 5 measurements
	}

	// Set up channel for receiving pong responses
	pongCh := make(chan float64, count)
	rg.pendingPongMu.Lock()
	rg.pendingPongCh = pongCh
	rg.pendingPongMu.Unlock()

	defer func() {
		rg.pendingPongMu.Lock()
		rg.pendingPongCh = nil
		rg.pendingPongMu.Unlock()
		close(pongCh)
	}()

	var measurements []float64
	timeout := 5 * time.Second // Timeout per ping

	for i := 0; i < count; i++ {
		// Send ping
		if err := rg.sendPing(); err != nil {
			rg.logger.WithError(err).Debugf("Failed to send ping %d/%d", i+1, count)
			continue
		}

		// Wait for pong with timeout
		select {
		case latencyMs := <-pongCh:
			// Drop samples outside (0, transport.MaxReasonableRTTMs]:
			//   - 0 seeds min/max at 0, producing {min:0, max:X, avg:Y}
			//     that downstream consumers reject or display oddly.
			//   - Stale pongs from earlier rounds (or from the periodic
			//     pingLoop) can land in this buffered channel after a
			//     long delay and produce 30+ second readings that
			//     would pin Max indefinitely on the underlying tp.
			if latencyMs <= 0 {
				rg.logger.Debugf("Ping %d/%d: dropped non-positive sample (%.6f ms)", i+1, count, latencyMs)
				continue
			}
			if latencyMs > transport.MaxReasonableRTTMs {
				rg.logger.Debugf("Ping %d/%d: dropped outlier sample (%.0f ms) — likely stale pong", i+1, count, latencyMs)
				continue
			}
			measurements = append(measurements, latencyMs)
			rg.logger.Debugf("Ping %d/%d: %.2f ms", i+1, count, latencyMs)
		case <-time.After(timeout):
			rg.logger.Debugf("Ping %d/%d timed out", i+1, count)
		case <-ctx.Done():
			return 0, 0, 0, ctx.Err()
		case <-rg.closed:
			return 0, 0, 0, errors.New("route group closed")
		}

		// Small delay between pings to avoid overwhelming the connection
		if i < count-1 {
			time.Sleep(100 * time.Millisecond)
		}
	}

	if len(measurements) == 0 {
		return 0, 0, 0, errors.New("no successful ping measurements")
	}

	// Calculate statistics
	min = measurements[0]
	max = measurements[0]
	var sum float64
	for _, m := range measurements {
		if m < min {
			min = m
		}
		if m > max {
			max = m
		}
		sum += m
	}
	avg = sum / float64(len(measurements))

	return min, max, avg, nil
}

func (rg *RouteGroup) broadcastClosePackets(code routing.CloseCode) {
	// Use a timeout context to prevent blocking forever on dead transports.
	// Without this, a dead transport causes writePacket to block indefinitely,
	// holding rg.mu and deadlocking the GC goroutine.
	ctx, cancel := context.WithTimeout(context.Background(), closeRoutineTimeout)
	defer cancel()

	for i := 0; i < len(rg.tps) && i < len(rg.fwd); i++ {
		if rg.tps[i] == nil || rg.fwd[i] == nil {
			continue
		}

		if err := rg.sendError(ctx, rg.fwd[i], rg.tps[i]); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send error packet to %s", rg.tps[i].Remote())
		}

		packet := routing.MakeClosePacket(rg.fwd[i].NextRouteID(), code)
		if err := rg.writePacket(ctx, rg.tps[i], packet, rg.fwd[i].KeyRouteID()); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send close packet to %s", rg.tps[i].Remote())
		}
	}
}

func (rg *RouteGroup) waitForCloseRouteGroup(waitTimeout time.Duration) error {
	select {
	case <-rg.closeDoneCh:
		return nil
	case <-time.After(waitTimeout):
		// Force-complete: zero the counter and signal the channel.
		atomic.StoreInt32(&rg.closeDonePending, 0)
		select {
		case <-rg.closeDoneCh:
		default:
			close(rg.closeDoneCh)
		}
		return fmt.Errorf("close route group timed out after %v", waitTimeout)
	}
}

func (rg *RouteGroup) isCloseInitiator() bool {
	return atomic.LoadInt32(&rg.closeInitiated) == 1
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

func (rg *RouteGroup) appendRules(forward, reverse routing.Rule, tp *transport.ManagedTransport) {
	rg.mu.Lock()

	rg.fwd = append(rg.fwd, forward)
	rg.rvs = append(rg.rvs, reverse)
	rg.tps = append(rg.tps, tp)
	newIdx := len(rg.tps) - 1

	// Rebuild transport weights when transports change
	if rg.mux != nil && len(rg.tps) > 1 {
		rg.mux.rebuildWeights(rg.tps)
	}
	// Keep per-leg counters parallel to tps[].
	if rg.mux != nil {
		rg.mux.growLegs(len(rg.tps))
	}
	// Initial single-leg rg's get their first leg via appendRules
	// too, but the policy doesn't need on_leg_change to fire for
	// that — the BeforeDial/SelectRoute knobs already handled it.
	// Only fire on subsequent appends (mux growth).
	hookSet := rg.legChangeHook != nil && newIdx > 0
	rg.mu.Unlock()

	if hookSet {
		rg.fireLegChange("added", newIdx)
	}
}

// appendForwardLeg adds a forward leg (rule + transport) WITHOUT a
// matching reverse rule. Used by asymmetric mux setups where the
// ForwardMuxRoutes count exceeds ReverseMuxRoutes — e.g. a workload
// with bulk upstream but tiny downstream. The reverse-direction
// rule from the same setup-node call is discarded by the caller
// (router.appendRouteAsymmetric) so it doesn't leak in the routing
// table.
//
// Mirrors appendRules' tps[]+fwd[] parallel maintenance but leaves
// rvs[] untouched. Mux weight + per-leg counter bookkeeping treats
// this as a new forward leg.
func (rg *RouteGroup) appendForwardLeg(forward routing.Rule, tp *transport.ManagedTransport) {
	rg.mu.Lock()

	rg.fwd = append(rg.fwd, forward)
	rg.tps = append(rg.tps, tp)
	newIdx := len(rg.tps) - 1

	if rg.mux != nil && len(rg.tps) > 1 {
		rg.mux.rebuildWeights(rg.tps)
	}
	if rg.mux != nil {
		rg.mux.growLegs(len(rg.tps))
	}
	hookSet := rg.legChangeHook != nil
	rg.mu.Unlock()

	if hookSet {
		rg.fireLegChange("added", newIdx)
	}
}

// appendReverseLeg adds a reverse rule WITHOUT a paired forward
// rule + transport. Used by asymmetric mux setups where
// ReverseMuxRoutes exceeds ForwardMuxRoutes — the operator's
// canonical bandwidth-asymmetric case (1 forward + N reverse for
// download-heavy workloads).
//
// rvs[] grows independently of tps[]/fwd[]; the read path is by
// route-id lookup (not slice-indexed) so this works without further
// data-plane changes. Per-leg counter slots (mux.legs) are NOT
// extended here — they are parallel to tps[] and the reverse-only
// leg doesn't add a forward transport. Per-direction recv-byte
// attribution for reverse-only legs is a separate refinement.
func (rg *RouteGroup) appendReverseLeg(reverse routing.Rule) {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	rg.rvs = append(rg.rvs, reverse)
}

func chanClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
