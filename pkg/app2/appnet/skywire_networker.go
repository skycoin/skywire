package appnet

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/skycoin/dmsg/netutil"
	"github.com/skycoin/skycoin/src/util/logging"

	"github.com/skycoin/skywire/pkg/app2"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

// SkywireNetworker implements `Networker` for skynet.
type SkywireNetworker struct {
	log       *logging.Logger
	r         router.Interface
	porter    *netutil.Porter
	isServing int32
}

// NewSkywireNetworker constructs skywire networker.
func NewSkywireNetworker(l *logging.Logger, r router.Interface) Networker {
	return &SkywireNetworker{
		log:    l,
		r:      r,
		porter: netutil.NewPorter(netutil.PorterMinEphemeral),
	}
}

// Dial dials remote `addr` via `skynet`.
func (r *SkywireNetworker) Dial(addr Addr) (net.Conn, error) {
	return r.DialContext(context.Background(), addr)
}

// Dial dials remote `addr` via `skynet` with context.
func (r *SkywireNetworker) DialContext(ctx context.Context, addr Addr) (net.Conn, error) {
	localPort, freePort, err := r.porter.ReserveEphemeral(ctx, nil)
	if err != nil {
		return nil, err
	}

	_, err = r.r.DialRoutes(ctx, addr.PubKey, routing.Port(localPort), addr.Port, router.DefaultDialOptions)
	if err != nil {
		return nil, err
	}
	rg := &app2.MockConn{}

	return &skywireConn{
		Conn:     rg,
		freePort: freePort,
	}, nil
}

// Listen starts listening on local `addr` in the skynet.
func (r *SkywireNetworker) Listen(addr Addr) (net.Listener, error) {
	return r.ListenContext(context.Background(), addr)
}

// Listen starts listening on local `addr` in the skynet with context.
func (r *SkywireNetworker) ListenContext(ctx context.Context, addr Addr) (net.Listener, error) {
	lis := &skywireListener{
		addr: addr,
		// TODO: pass buf size
		connsCh:  make(chan net.Conn, 1000000),
		freePort: nil,
	}

	ok, freePort := r.porter.Reserve(uint16(addr.Port), lis)
	if !ok {
		return nil, ErrPortAlreadyBound
	}

	lis.freePortMx.Lock()
	lis.freePort = freePort
	lis.freePortMx.Unlock()

	if atomic.CompareAndSwapInt32(&r.isServing, 0, 1) {
		go func() {
			if err := r.serve(); err != nil {
				r.log.WithError(err).Error("error serving")
			}
		}()
	}

	return lis, nil
}

// serve accepts and serves routes.
func (r *SkywireNetworker) serve() error {
	for {
		_, err := r.r.AcceptRoutes()
		if err != nil {
			return err
		}
		rg := &app2.MockConn{}

		go r.serveRG(rg)
	}
}

// TODO: change to `*router.RouterGroup`
// serveRG passes accepted router group to the corresponding listener.
func (r *SkywireNetworker) serveRG(rg net.Conn) {
	localAddr, ok := rg.LocalAddr().(routing.Addr)
	if !ok {
		r.closeRG(rg)
		r.log.Error("wrong type of addr in accepted conn")
		return
	}

	lisIfc, ok := r.porter.PortValue(uint16(localAddr.Port))
	if !ok {
		r.closeRG(rg)
		r.log.Errorf("no listener on port %d", localAddr.Port)
		return
	}

	lis, ok := lisIfc.(*skywireListener)
	if !ok {
		r.closeRG(rg)
		r.log.Errorf("wrong type of listener on port %d", localAddr.Port)
		return
	}

	lis.putConn(rg)
}

// TODO: change to `*router.RouterGroup`
// closeRG closes router group and logs error if any.
func (r *SkywireNetworker) closeRG(rg net.Conn) {
	if err := rg.Close(); err != nil {
		r.log.Error(err)
	}
}

// skywireListener is a listener for skynet.
// Implements net.Listener.
type skywireListener struct {
	addr       Addr
	connsCh    chan net.Conn
	freePort   func()
	freePortMx sync.RWMutex
}

// Accept accepts incoming connection.
func (l *skywireListener) Accept() (net.Conn, error) {
	return <-l.connsCh, nil
}

// Close closes listener.
func (l *skywireListener) Close() error {
	l.freePortMx.RLock()
	defer l.freePortMx.RUnlock()
	l.freePort()

	return nil
}

// Addr returns local address.
func (l *skywireListener) Addr() net.Addr {
	return l.addr
}

// putConn puts accepted conn to the listener to be later retrieved
// via `Accept`.
func (l *skywireListener) putConn(conn net.Conn) {
	l.connsCh <- conn
}

// skywireConn is a connection wrapper for skynet.
type skywireConn struct {
	// TODO: change to `*router.RouterGroup`
	net.Conn
	freePort   func()
	freePortMx sync.RWMutex
}

// Close closes connection.
func (c *skywireConn) Close() error {
	defer func() {
		c.freePortMx.RLock()
		defer c.freePortMx.RUnlock()
		if c.freePort != nil {
			c.freePort()
		}
	}()

	return c.Conn.Close()
}
