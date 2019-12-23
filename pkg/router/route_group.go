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
	fwd []routing.Rule // forward rules (for writing)
	rvs []routing.Rule // reverse rules (for reading)

	lastSent int64

	// 'readCh' reads in incoming packets of this route group.
	// - Router should serve call '(*transport.Manager).ReadPacket' in a loop,
	//      and push to the appropriate '(RouteGroup).readCh'.
	readCh   chan []byte // push reads from Router
	readChMu sync.Mutex
	readBuf  bytes.Buffer // for read overflow
	done     chan struct{}
	once     sync.Once

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline
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
		done:          make(chan struct{}),
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
		rg.logger.Infoln("TIMEOUT ERROR?")
		return 0, timeoutError{}
	}

	if len(p) == 0 {
		return 0, nil
	}

	// In case the read buffer is short.
	rg.mu.Lock()
	if rg.readBuf.Len() > 0 {
		data, err := rg.readBuf.Read(p)
		rg.mu.Unlock()

		return data, err
	}
	rg.mu.Unlock()

	var data []byte
	select {
	case <-rg.readDeadline.Wait():
		return 0, timeoutError{}
	case data = <-rg.readCh:
	}

	rg.mu.Lock()
	defer rg.mu.Unlock()

	return ioutil.BufRead(&rg.readBuf, data, p)
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
	defer rg.mu.Unlock()

	tp, err := rg.tp()
	if err != nil {
		return 0, err
	}

	rule, err := rg.rule()
	if err != nil {
		return 0, err
	}

	packet := routing.MakeDataPacket(rule.KeyRouteID(), p)

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

		return len(p), nil
	}
}

func (rg *RouteGroup) writePacketAsync(ctx context.Context, tp *transport.ManagedTransport, packet routing.Packet) chan error {
	errCh := make(chan error)
	go func() {
		errCh <- tp.WritePacket(ctx, packet)
		close(errCh)
	}()

	return errCh
}

func (rg *RouteGroup) rule() (routing.Rule, error) {
	if len(rg.fwd) == 0 {
		return nil, ErrNoRules
	}

	rule := rg.fwd[0]

	return rule, nil
}

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

// Close closes a RouteGroup:
// - Send Close packet for all ForwardRules.
// - Delete all rules (ForwardRules and ConsumeRules) from routing table.
// - Close all go channels.
func (rg *RouteGroup) Close() error {
	rg.mu.Lock()
	defer rg.mu.Unlock()

	if len(rg.fwd) != len(rg.tps) {
		return ErrRuleTransportMismatch
	}

	for i := 0; i < len(rg.tps); i++ {
		packet := routing.MakeClosePacket(rg.fwd[i].KeyRouteID(), routing.CloseRequested)
		if err := rg.tps[i].WritePacket(context.Background(), packet); err != nil {
			return err
		}
	}

	rules := rg.rt.RulesWithDesc(rg.desc)
	routeIDs := make([]routing.RouteID, 0, len(rules))

	for _, rule := range rules {
		routeIDs = append(routeIDs, rule.KeyRouteID())
	}

	rg.rt.DelRules(routeIDs)

	rg.once.Do(func() {
		close(rg.done)
		rg.readChMu.Lock()
		close(rg.readCh)
		rg.readChMu.Unlock()
	})

	return nil
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

func (rg *RouteGroup) keepAliveLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		lastSent := time.Unix(0, atomic.LoadInt64(&rg.lastSent))

		if time.Since(lastSent) < interval {
			continue
		}

		if err := rg.sendKeepAlive(); err != nil {
			rg.logger.Warnf("Failed to send keepalive: %v", err)
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

	packet := routing.MakeKeepAlivePacket(rule.KeyRouteID())
	if err := tp.WritePacket(context.Background(), packet); err != nil {
		return err
	}

	return nil
}

func (rg *RouteGroup) isClosed() bool {
	select {
	case <-rg.done:
		return true
	default:
		return false
	}
}
