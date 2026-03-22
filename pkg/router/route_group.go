// Package router pkg/router/route_group.go
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

	"github.com/skycoin/dmsg/pkg/ioutil"

	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
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

	handshakeProcessed     chan struct{}
	handshakeProcessedOnce sync.Once
	encrypt                bool

	// forwardHops stores the complete route path as originally calculated.
	// This is the full multi-hop route, not just local transports.
	forwardHops []routing.Hop

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
	// used to wait for all the `Close` packets to run through the loop and come back
	closeDone sync.WaitGroup
	once      sync.Once

	errorMu    sync.RWMutex
	closeError error

	// For synchronous latency measurement
	pendingPongCh chan float64 // Receives measured latency when pong arrives
	pendingPongMu sync.Mutex

	// Route multiplexing layer (nil when mux not negotiated).
	// Encapsulates sequencing, reordering, SACK, and transport selection.
	mux *routeMux
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
	}

	return rg
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
	tp, rule, err := rg.nextTransport()
	if err != nil {
		rg.mu.Unlock()
		return 0, err
	}
	rg.mu.Unlock()

	return rg.write(p, tp, rule)
}

// Close closes a RouteGroup.
func (rg *RouteGroup) Close() error {
	if rg.isClosed() {
		return io.ErrClosedPipe
	}

	if rg.isRemoteClosed() {
		// remote already closed, everything is cleaned up,
		// we just need to close signal channel at this point
		close(rg.closed)
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

	select {
	case <-rg.readDeadline.Wait():
		return 0, timeoutError{}
	case <-rg.closed:
		return 0, io.ErrClosedPipe
	case data, ok := <-rg.readCh:
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
}

func (rg *RouteGroup) write(data []byte, tp *transport.ManagedTransport, rule routing.Rule) (int, error) {
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
				rg.logger.WithError(err).Errorf("error updating activity of rule %d", ruleID)
			}
		}
	}

	return err
}

// nextTransport selects the next transport/rule pair. When mux is enabled and
// multiple transports exist, uses latency-weighted selection. Falls back to
// round-robin when latency data is unavailable, and to index 0 for single
// transport (legacy behavior).
// NOTE: not thread-safe, caller must hold rg.mu.
func (rg *RouteGroup) nextTransport() (*transport.ManagedTransport, routing.Rule, error) {
	if len(rg.tps) == 0 {
		return nil, nil, ErrNoTransports
	}
	if len(rg.fwd) == 0 {
		return nil, nil, ErrNoRules
	}
	if len(rg.tps) != len(rg.fwd) {
		return nil, nil, ErrRuleTransportMismatch
	}

	if rg.mux != nil && len(rg.tps) > 1 {
		return rg.mux.selectTransport(rg.tps, rg.fwd)
	}

	if rg.tps[0] == nil {
		return nil, nil, ErrBadTransport
	}
	return rg.tps[0], rg.fwd[0], nil
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
	// Note: Automatic ping loop removed. Latency is now measured once at transport creation.
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

func (rg *RouteGroup) servicePacketLoop(name string, interval time.Duration, f sendServicePacketFn) {
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

	// Rebuild transport weights based on current latency measurements
	if rg.mux != nil && len(rg.tps) > 1 {
		rg.mu.Lock()
		rg.mux.rebuildWeights(rg.tps)
		rg.mu.Unlock()
	}
}

func (rg *RouteGroup) sendKeepAlive() error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		// if no transports, no rules, then no keepalive
		return nil
	}

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]
		rule := rg.fwd[i]

		if tp == nil {
			continue
		}

		packet := routing.MakeKeepAlivePacket(rule.NextRouteID())

		if err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID()); err != nil {
			return err
		}
	}

	return nil
}

func (rg *RouteGroup) sendHandshake(encrypt bool) error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		// if no transports, no rules, then no keepalive
		return nil
	}

	for i := 0; i < len(rg.tps); i++ {
		tp := rg.tps[i]

		if tp == nil {
			continue
		}

		rule := rg.fwd[i]
		packet := routing.MakeHandshakePacket(rule.NextRouteID(), encrypt, routing.CapMux|routing.CapSACK)

		err := rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
		if err == nil {
			rg.logger.Debugf("Sent handshake via transport %v", tp.Entry.ID)
			return nil
		}

		rg.logger.Debugf("Failed to send handshake via transport %v: %v [%v/%v]",
			tp.Entry.ID, err, i+1, len(rg.tps))
	}

	return ErrNoSuitableTransport
}

func (rg *RouteGroup) sendError(rule routing.Rule, tp *transport.ManagedTransport) error {
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

	return rg.writePacket(context.Background(), tp, packet, rule.KeyRouteID())
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
		rg.closeDone.Add(len(rg.tps))
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

	rules := make([]routing.RouteID, 0, len(rg.fwd))
	for _, r := range rg.fwd {
		rules = append(rules, r.KeyRouteID())
	}

	rg.rt.DelRules(rules)

	rg.once.Do(func() {
		if closeInitiator {
			close(rg.closed)
		}
		rg.setRemoteClosed()
		close(rg.readCh)
	})

	return nil
}

func (rg *RouteGroup) handlePacket(packet routing.Packet) error {
	switch packet.Type() {
	case routing.ClosePacket:
		return rg.handleClosePacket(routing.CloseCode(packet.Payload()[0]))
	case routing.DataPacket:
		rg.handshakeProcessedOnce.Do(func() {
			// first packet is data packet, so we're communicating with the old visor
			rg.encrypt = false
			close(rg.handshakeProcessed)
		})
		return rg.handleDataPacket(packet)
	case routing.HandshakePacket:
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

func (rg *RouteGroup) handleDataPacket(packet routing.Packet) error {

	// in this case remote is already closed, and `readCh` is closed too,
	// but some packets may still reach the rg causing panic on writing
	// to `readCh`, so we simple omit such packets
	if rg.isRemoteClosed() {
		return nil
	}
	rg.networkStats.AddBandwidthReceived(uint64(packet.Size()))

	if rg.mux != nil {
		seq := packet.SequenceNumber()
		data := packet.DataPayloadAfterSeq()

		delivered, gapDetected := rg.mux.deliverData(seq, data)

		if gapDetected {
			go rg.sendSACK() //nolint:errcheck
		}

		for _, d := range delivered {
			select {
			case <-rg.closed:
				return io.ErrClosedPipe
			case rg.readCh <- d:
			}
		}
		return nil
	}

	// Legacy path: deliver payload directly
	select {
	case <-rg.closed:
		return io.ErrClosedPipe
	case rg.readCh <- packet.Payload():
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
		tp, rule, err := rg.nextTransport()
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

		rg.closeDone.Done()
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

	ms := sentAtMs % 1000
	sentAt := time.Unix(int64(sentAtMs/1000), int64(ms)*int64(time.Millisecond)).UTC() //nolint: gosec

	// Use fractional milliseconds for sub-ms precision (e.g. 1.2 ms)
	latencyMs := float64(time.Now().UTC().Sub(sentAt).Microseconds()) / 1000.0

	rg.logger.WithField("func", "RouteGroup.handlePongPacket").Tracef("Latency is around %.1f ms", latencyMs)

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

		if err := rg.sendError(rg.fwd[i], rg.tps[i]); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send error packet to %s", rg.tps[i].Remote())
		}

		packet := routing.MakeClosePacket(rg.fwd[i].NextRouteID(), code)
		if err := rg.writePacket(ctx, rg.tps[i], packet, rg.fwd[i].KeyRouteID()); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send close packet to %s", rg.tps[i].Remote())
		}
	}
}

func (rg *RouteGroup) waitForCloseRouteGroup(waitTimeout time.Duration) error {
	closeDoneCh := make(chan struct{})
	go func() {
		rg.closeDone.Wait()
		close(closeDoneCh)
	}()

	select {
	case <-time.After(waitTimeout):
		// Force-complete outstanding WaitGroup entries so the goroutine above
		// can exit. Without this, each timed-out close leaks a goroutine
		// permanently blocked on closeDone.Wait().
		rg.forceCompleteCloseDone()
		// Wait briefly for the goroutine to notice and exit
		select {
		case <-closeDoneCh:
		case <-time.After(100 * time.Millisecond):
		}
		return fmt.Errorf("close route group timed out after %v", waitTimeout)
	case <-closeDoneCh:
		return nil
	}
}

// forceCompleteCloseDone drains the closeDone WaitGroup by calling Done()
// for each outstanding entry. Uses recover to catch panics from calling
// Done() more times than Add() (in case some responses arrived).
func (rg *RouteGroup) forceCompleteCloseDone() {
	for i := 0; i < len(rg.tps); i++ {
		func() {
			defer func() { recover() }() //nolint:errcheck
			rg.closeDone.Done()
		}()
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
	defer rg.mu.Unlock()

	rg.fwd = append(rg.fwd, forward)
	rg.rvs = append(rg.rvs, reverse)
	rg.tps = append(rg.tps, tp)

	// Rebuild transport weights when transports change
	if rg.mux != nil && len(rg.tps) > 1 {
		rg.mux.rebuildWeights(rg.tps)
	}
}

func chanClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
