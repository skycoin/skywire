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
)

const (
	readChBufSize = 1024
)

var (
	// ErrNoTransports is returned when RouteGroup has no transports.
	ErrNoTransports = errors.New("no transports")
	// ErrNoTransport is returned when RouteGroup has no rules.
	ErrNoRules = errors.New("no rules")
	// ErrNoTransport is returned when transport is nil.
	ErrBadTransport = errors.New("bad transport")
)

// RouteGroup should implement 'io.ReadWriteCloser'.
// It implements 'net.Conn'.
type RouteGroup struct {
	mu sync.RWMutex

	logger *logging.Logger

	desc routing.RouteDescriptor // describes the route group
	rt   routing.Table

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
}

func NewRouteGroup(rt routing.Table, desc routing.RouteDescriptor) *RouteGroup {
	rg := &RouteGroup{
		logger:  logging.MustGetLogger(fmt.Sprintf("RouteGroup %v", desc)),
		desc:    desc,
		fwd:     make([]routing.Rule, 0),
		rvs:     make([]routing.Rule, 0),
		tps:     make([]*transport.ManagedTransport, 0),
		readCh:  make(chan []byte, readChBufSize),
		readBuf: bytes.Buffer{},
		rt:      rt,
	}

	go rg.keepAliveLoop()

	return rg
}

// Read reads the next packet payload of a RouteGroup.
// The Router, via transport.Manager, is responsible for reading incoming packets and pushing it to the appropriate RouteGroup via (*RouteGroup).readCh.
// To help with implementing the read logic, within the dmsg repo, we have ioutil.BufRead, just in case the read buffer is short.
func (r *RouteGroup) Read(p []byte) (n int, err error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if r.readBuf.Len() > 0 {
		return r.readBuf.Read(p)
	}

	data, ok := <-r.readCh
	if !ok {
		return 0, io.ErrClosedPipe
	}

	return ioutil.BufRead(&r.readBuf, data, p)
}

// Write writes payload to a RouteGroup
// For the first version, only the first ForwardRule (fwd[0]) is used for writing.
func (r *RouteGroup) Write(p []byte) (n int, err error) {
	if r.isClosing() {
		return 0, io.ErrClosedPipe
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
	if err := tp.WritePacket(context.Background(), packet); err != nil {
		return 0, err
	}

	atomic.StoreInt64(&r.lastSent, time.Now().UnixNano())

	return len(p), nil
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
		close(r.readCh)
	})

	return nil
}

func (r *RouteGroup) LocalAddr() net.Addr {
	return r.desc.Src()
}

func (r *RouteGroup) RemoteAddr() net.Addr {
	return r.desc.Dst()
}

// TODO(nkryuchkov): implement
func (r *RouteGroup) SetDeadline(t time.Time) error {
	return nil
}

// TODO(nkryuchkov): implement
func (r *RouteGroup) SetReadDeadline(t time.Time) error {
	return nil
}

// TODO(nkryuchkov): implement
func (r *RouteGroup) SetWriteDeadline(t time.Time) error {
	return nil
}

func (r *RouteGroup) keepAliveLoop() {
	keepAlive := 1 * time.Minute // TODO: proper value

	ticker := time.NewTicker(keepAlive / 2)
	defer ticker.Stop()

	for range ticker.C {
		lastSent := time.Unix(0, atomic.LoadInt64(&r.lastSent))

		if time.Since(lastSent) < keepAlive/2 {
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

	if len(r.tps) == 0 {
		return ErrNoTransports
	}
	if len(r.fwd) == 0 {
		return ErrNoRules
	}

	tp := r.tps[0]
	rule := r.fwd[0]

	if tp == nil {
		return errors.New("unknown transport")
	}

	packet := routing.MakeKeepAlivePacket(rule.KeyRouteID())
	if err := tp.WritePacket(context.Background(), packet); err != nil {
		return err
	}
	return nil
}

func (r *RouteGroup) isClosing() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}
