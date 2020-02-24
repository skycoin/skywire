package router

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/SkycoinProject/dmsg/ioutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
	"github.com/SkycoinProject/skywire-mainnet/pkg/transport"
	"github.com/SkycoinProject/skywire-mainnet/pkg/util/deadline"
)

const (
	defaultRouteGroupKeepAliveInterval = 1 * time.Minute
	defaultReadChBufSize               = 1024
	closeRoutineTimeout                = 2 * time.Second
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
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

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
	mu sync.Mutex

	cfg    *RouteGroupConfig
	logger *logging.Logger
	desc   routing.RouteDescriptor // describes the route group
	rt     routing.Table

	// 'tps' is transports used for writing/forward rules.
	// It should have the same number of elements as 'fwd'
	// where each element corresponds with the adjacent element in 'fwd'.
	tps []*transport.ManagedTransport

	// The following fields are used for writing:
	// - fwd/tps should have the same number of elements.
	// - the corresponding element of tps should have tpID of the corresponding rule in fwd.
	// - fwd references 'ForwardRule' rules for writes.
	fwd                  []routing.Rule // forward rules (for writing)
	rvs                  []routing.Rule // reverse rules (for reading)
	rvsRouteLastActivity time.Time

	lastSent int64

	// 'readCh' reads in incoming packets of this route group.
	// - Router should serve call '(*transport.Manager).ReadPacket' in a loop,
	//      and push to the appropriate '(RouteGroup).readCh'.
	readCh   chan []byte // push reads from Router
	readChMu sync.Mutex
	readBuf  bytes.Buffer // for read overflow
	once     sync.Once

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline

	// used as a bool to indicate if this particular route group initiated close loop
	closeInitiated   int32
	remoteClosedOnce sync.Once
	remoteClosed     chan struct{}
	closed           chan struct{}
	// used to wait for all the `Close` packets to run through the loop and come back
	closeDone sync.WaitGroup
}

// NewRouteGroup creates a new RouteGroup.
func NewRouteGroup(cfg *RouteGroupConfig, rt routing.Table, desc routing.RouteDescriptor) *RouteGroup {
	if cfg == nil {
		cfg = DefaultRouteGroupConfig()
	}

	rg := &RouteGroup{
		cfg:           cfg,
		logger:        logging.MustGetLogger(fmt.Sprintf("RouteGroup %s", desc.String())),
		desc:          desc,
		rt:            rt,
		tps:           make([]*transport.ManagedTransport, 0),
		fwd:           make([]routing.Rule, 0),
		rvs:           make([]routing.Rule, 0),
		readCh:        make(chan []byte, cfg.ReadChBufSize),
		readBuf:       bytes.Buffer{},
		remoteClosed:  make(chan struct{}),
		closed:        make(chan struct{}),
		readDeadline:  deadline.MakePipeDeadline(),
		writeDeadline: deadline.MakePipeDeadline(),
	}

	go rg.keepAliveLoop(cfg.KeepAliveInterval)

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
	tp, err := rg.tp()
	if err != nil {
		rg.mu.Unlock()
		return 0, err
	}

	rule, err := rg.rule()
	if err != nil {
		rg.mu.Unlock()
		return 0, err
	}
	// we don't need to keep holding mutex from this point on
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
	packet := routing.MakeDataPacket(rule.NextRouteID(), data)

	rg.logger.Infof("WRITING PACKET OF TYPE %s AND ROUTE ID %d AND NEXT ID %d", packet.Type(),
		rule.KeyRouteID(), rule.NextRouteID())

	ctx, cancel := context.WithCancel(context.Background())

	errCh := rg.writePacketAsync(ctx, tp, packet)
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

func (rg *RouteGroup) writePacketAsync(ctx context.Context, tp *transport.ManagedTransport, packet routing.Packet) chan error {
	errCh := make(chan error)
	go func() {
		defer close(errCh)
		err := tp.WritePacket(ctx, packet)
		select {
		case <-ctx.Done():
			return
		case errCh <- err:
			return
		}
	}()

	return errCh
}

// rule fetches first available forward rule.
// NOTE: not thread-safe.
func (rg *RouteGroup) rule() (routing.Rule, error) {
	if len(rg.fwd) == 0 {
		return nil, ErrNoRules
	}

	rule := rg.fwd[0]

	return rule, nil
}

// tp fetches first available transport.
// NOTE: not thread-safe.
func (rg *RouteGroup) tp() (*transport.ManagedTransport, error) {
	if len(rg.tps) == 0 {
		return nil, ErrNoTransports
	}

	tp := rg.tps[0]

	if tp == nil {
		return nil, ErrBadTransport
	}

	return tp, nil
}

func (rg *RouteGroup) keepAliveLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-rg.remoteClosed:
			rg.logger.Infoln("Remote got closed, stopping keep-alive loop")
			return
		case <-ticker.C:
			lastSent := time.Unix(0, atomic.LoadInt64(&rg.lastSent))

			if time.Since(lastSent) < interval {
				continue
			}

			if err := rg.sendKeepAlive(); err != nil {
				rg.logger.Warnf("Failed to send keepalive: %v", err)
			}
		}
	}
}

func (rg *RouteGroup) sendKeepAlive() error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.tps) == 0 || len(rg.fwd) == 0 {
		// if no transports, no rules, then no keepalive
		return nil
	}

	tp := rg.tps[0]
	rule := rg.fwd[0]

	if tp == nil {
		return ErrBadTransport
	}

	packet := routing.MakeKeepAlivePacket(rule.NextRouteID())
	if err := tp.WritePacket(context.Background(), packet); err != nil {
		return err
	}

	return nil
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
		if err := rg.waitForCloseLoop(closeRoutineTimeout); err != nil {
			rg.logger.Errorf("Error during close loop: %v", err)
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

		rg.readChMu.Lock()
		close(rg.readCh)
		rg.readChMu.Unlock()
	})

	return nil
}

func (rg *RouteGroup) handlePacket(packet routing.Packet) error {
	rg.mu.Lock()
	// no need to check rule expiry, since router won't allow packet in
	// in case it's expired, so simply update the activity
	rg.rvsRouteLastActivity = time.Now()
	rg.mu.Unlock()

	switch packet.Type() {
	case routing.ClosePacket:
		return rg.handleClosePacket(routing.CloseCode(packet.Payload()[0]))
	case routing.DataPacket:
		return rg.handleDataPacket(packet)
	}

	return nil
}

func (rg *RouteGroup) handleDataPacket(packet routing.Packet) error {
	select {
	case <-rg.closed:
		return io.ErrClosedPipe
	case rg.readCh <- packet.Payload():
	}

	return nil
}

func (rg *RouteGroup) handleClosePacket(code routing.CloseCode) error {
	rg.logger.Infof("Got close packet with code %d", code)

	if rg.isCloseInitiator() {
		// this route group initiated close loop and got response
		rg.logger.Debugf("Handling response close packet with code %d", code)

		rg.closeDone.Done()
		return nil
	}

	// TODO: use `close` with some close code if we decide that it should be different from the current one
	return rg.close(code)
}

func (rg *RouteGroup) broadcastClosePackets(code routing.CloseCode) {
	for i := 0; i < len(rg.tps); i++ {
		packet := routing.MakeClosePacket(rg.fwd[i].NextRouteID(), code)
		if err := rg.tps[i].WritePacket(context.Background(), packet); err != nil {
			rg.logger.WithError(err).Errorf("Failed to send close packet to %s", rg.tps[i].Remote())
		}
	}
}

func (rg *RouteGroup) waitForCloseLoop(waitTimeout time.Duration) error {
	closeCtx, closeCancel := context.WithTimeout(context.Background(), waitTimeout)
	defer closeCancel()

	closeDoneCh := make(chan struct{})
	go func() {
		// wait till all remotes respond to close procedure
		rg.closeDone.Wait()
		close(closeDoneCh)
	}()

	select {
	case <-closeCtx.Done():
		return fmt.Errorf("close loop timed out: %v", closeCtx.Err())
	case <-closeDoneCh:
	}

	return nil
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

func chanClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
	}

	return false
}
