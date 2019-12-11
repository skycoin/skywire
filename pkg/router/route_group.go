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
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

type RouteGroupConfig struct {
	ReadChBufSize     int
	KeepAliveInterval time.Duration
}

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
	readCh  chan []byte  // push reads from Router
	readBuf bytes.Buffer // for read overflow
	done    chan struct{}
	once    sync.Once

	readDeadline  deadline.PipeDeadline
	writeDeadline deadline.PipeDeadline
}

func NewRouteGroup(cfg *RouteGroupConfig, rt routing.Table, desc routing.RouteDescriptor) *RouteGroup {
	if cfg == nil {
		cfg = DefaultRouteGroupConfig()
	}

	rg := &RouteGroup{
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
// To help with implementing the read logic, within the dmsg repo, we have ioutil.BufRead,
// just in case the read buffer is short.
func (r *RouteGroup) Read(p []byte) (n int, err error) {
	if r.isClosed() {
		return 0, io.ErrClosedPipe
	}

	if r.readDeadline.Closed() {
		r.logger.Infoln("TIMEOUT ERROR?")
		return 0, timeoutError{}
	}

	if len(p) == 0 {
		return 0, nil
	}

	// TODO: use readBuf
	// r.mu.Lock()
	// if r.readBuf.Len() > 0 {
	// 	data, err := r.readBuf.Read(p)
	// 	r.mu.Unlock()
	//
	// 	return data, err
	// }
	// r.mu.Unlock()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	var data []byte
	select {
	case <-r.readDeadline.Wait():
		return 0, timeoutError{}
	case <-timeout.C:
		return 0, io.EOF
	case data = <-r.readCh:
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// return ioutil.BufRead(&r.readBuf, data, p)

	n = copy(p, data)

	return n, nil
}

// Write writes payload to a RouteGroup
// For the first version, only the first ForwardRule (fwd[0]) is used for writing.
func (r *RouteGroup) Write(p []byte) (n int, err error) {
	if r.isClosed() {
		return 0, io.ErrClosedPipe
	}

	if r.writeDeadline.Closed() {
		return 0, timeoutError{}
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.tps) == 0 {
		return 0, ErrNoTransports
	}

	if len(r.fwd) == 0 {
		return 0, ErrNoRules
	}

	tp := r.tps[0]
	rule := r.fwd[0]

	if tp == nil {
		return 0, ErrBadTransport
	}

	packet := routing.MakeDataPacket(rule.KeyRouteID(), p)

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error)

	go func() {
		select {
		case <-ctx.Done():
		case errCh <- tp.WritePacket(context.Background(), packet):
		}
		close(errCh)
		return
	}()

	timeout := time.NewTimer(5 * time.Second)
	defer timeout.Stop()

	select {
	case <-r.writeDeadline.Wait():
		cancel()
		return 0, timeoutError{}
	case <-timeout.C:
		cancel()
		return 0, io.EOF
	case err := <-errCh:
		if err != nil {
			return 0, err
		}

		atomic.StoreInt64(&r.lastSent, time.Now().UnixNano())

		return len(p), nil
	}
}

func (r *RouteGroup) writePacketAsync(tp *transport.ManagedTransport, packet routing.Packet) error {
	var err error
	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()
		err = tp.WritePacket(context.Background(), packet)
	}()

	wg.Wait()

	return err
}

// Close closes a RouteGroup:
// - Send Close packet for all ForwardRules.
// - Delete all rules (ForwardRules and ConsumeRules) from routing table.
// - Close all go channels.
func (r *RouteGroup) Close() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.fwd) != len(r.tps) {
		return errors.New("len(r.fwd) != len(r.tps)")
	}

	for i := 0; i < len(r.tps); i++ {
		packet := routing.MakeClosePacket(r.fwd[i].KeyRouteID(), routing.CloseRequested)
		if err := r.tps[i].WritePacket(context.Background(), packet); err != nil {
			return err
		}
	}

	rules := r.rt.RulesWithDesc(r.desc)
	routeIDs := make([]routing.RouteID, 0, len(rules))

	for _, rule := range rules {
		routeIDs = append(routeIDs, rule.KeyRouteID())
	}

	r.rt.DelRules(routeIDs)

	r.once.Do(func() {
		close(r.done)
		// close(r.readCh) // TODO: uncomment
	})

	return nil
}

func (r *RouteGroup) LocalAddr() net.Addr {
	return r.desc.Dst()
}

func (r *RouteGroup) RemoteAddr() net.Addr {
	return r.desc.Src()
}

// https://golang.org/src/internal/poll/fd_plan9.go#L103
// https://golang.org/src/internal/poll/fd_poll_runtime.go#L126
func (r *RouteGroup) SetDeadline(t time.Time) error {
	if err := r.SetReadDeadline(t); err != nil {
		return err
	}

	return r.SetWriteDeadline(t)
}

func (r *RouteGroup) SetReadDeadline(t time.Time) error {
	r.readDeadline.Set(t)
	return nil
}

func (r *RouteGroup) SetWriteDeadline(t time.Time) error {
	r.writeDeadline.Set(t)
	return nil
}

func (r *RouteGroup) keepAliveLoop(interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for range ticker.C {
		lastSent := time.Unix(0, atomic.LoadInt64(&r.lastSent))

		if time.Since(lastSent) < interval {
			continue
		}

		if err := r.sendKeepAlive(); err != nil {
			r.logger.Warnf("Failed to send keepalive: %v", err)
		}
	}
}

func (r *RouteGroup) sendKeepAlive() error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if len(r.tps) == 0 || len(r.fwd) == 0 {
		// if no transports, no rules, then no keepalive
		return nil
	}

	tp := r.tps[0]
	rule := r.fwd[0]

	if tp == nil {
		return ErrBadTransport
	}

	packet := routing.MakeKeepAlivePacket(rule.KeyRouteID())
	if err := tp.WritePacket(context.Background(), packet); err != nil {
		return err
	}

	return nil
}

func (r *RouteGroup) isClosed() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
