package appnet

import (
	"context"
	"net"
	"sync"
	"sync/atomic"

	"github.com/SkycoinProject/dmsg/netutil"
	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/pkg/errors"

	"github.com/SkycoinProject/skywire-mainnet/pkg/router"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

// SkywireNetworker implements `Networker` for skynet.
type SkywireNetworker struct {
	log       *logging.Logger
	r         router.Router
	porter    *netutil.Porter
	isServing int32
}

// NewSkywireNetworker constructs skywire networker.
func NewSkywireNetworker(l *logging.Logger, r router.Router) Networker {
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

	rg, err := r.r.DialRoutes(ctx, addr.PubKey, routing.Port(localPort), addr.Port, router.DefaultDialOptions())
	if err != nil {
		return nil, err
	}

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
			if err := r.serve(ctx); err != nil {
				r.log.WithError(err).Error("error serving")
			}
		}()
	}

	return lis, nil
}

// serve accepts and serves routes.
func (r *SkywireNetworker) serve(ctx context.Context) error {
	for {
		r.log.Infoln("Trying to accept routing group...")
		rg, err := r.r.AcceptRoutes(ctx)
		if err != nil {
			r.log.Infof("Error accepting routing group: %v", err)
			return err
		}

		r.log.Infoln("Accepted routing group")

		go r.serveRG(rg)
	}
}

// serveRG passes accepted router group to the corresponding listener.
func (r *SkywireNetworker) serveRG(rg *router.RouteGroup) {
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

// closeRG closes router group and logs error if any.
func (r *SkywireNetworker) closeRG(rg *router.RouteGroup) {
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
	once       sync.Once
}

// Accept accepts incoming connection.
func (l *skywireListener) Accept() (net.Conn, error) {
	conn, ok := <-l.connsCh
	if !ok {
		return nil, errors.New("listening on closed connection")
	}

	return conn, nil
}

// Close closes listener.
func (l *skywireListener) Close() error {
	l.once.Do(func() {
		l.freePortMx.RLock()
		defer l.freePortMx.RUnlock()
		l.freePort()
		close(l.connsCh)
	})

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
	net.Conn
	freePort   func()
	freePortMx sync.RWMutex
	once       sync.Once
}

// Close closes connection.
func (c *skywireConn) Close() error {
	var err error
	c.once.Do(func() {
		defer func() {
			c.freePortMx.RLock()
			defer c.freePortMx.RUnlock()
			if c.freePort != nil {
				c.freePort()
			}
		}()

		err = c.Conn.Close()
	})

	return err
}
