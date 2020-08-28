package appnet

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/dmsg/netutil"

	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
)

var (
	// ErrPortAlreadyBound is being returned when the desired port is already bound to.
	ErrPortAlreadyBound = errors.New("port already bound")
)

// SkywireNetworker implements `Networker` for skynet.
type SkywireNetworker struct {
	log       logrus.FieldLogger
	r         router.Router
	porter    *netutil.Porter
	isServing int32
}

// NewSkywireNetworker constructs skywire networker.
func NewSkywireNetworker(l logrus.FieldLogger, r router.Router) Networker {
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

// DialContext dials remote `addr` via `skynet` with context.
func (r *SkywireNetworker) DialContext(ctx context.Context, addr Addr) (conn net.Conn, err error) {
	localPort, freePort, err := r.porter.ReserveEphemeral(ctx, nil)
	if err != nil {
		return nil, err
	}

	// ensure ports are freed on error.
	defer func() {
		if err != nil {
			freePort()
		}
	}()

	conn, err = r.r.DialRoutes(ctx, addr.PubKey, routing.Port(localPort), addr.Port, router.DefaultDialOptions())
	if err != nil {
		return nil, err
	}

	return &skywireConn{
		Conn:     conn,
		freePort: freePort,
	}, nil
}

// Listen starts listening on local `addr` in the skynet.
func (r *SkywireNetworker) Listen(addr Addr) (net.Listener, error) {
	return r.ListenContext(context.Background(), addr)
}

// ListenContext starts listening on local `addr` in the skynet with context.
func (r *SkywireNetworker) ListenContext(ctx context.Context, addr Addr) (net.Listener, error) {
	const bufSize = 1000000

	lis := &skywireListener{
		addr:     addr,
		connsCh:  make(chan net.Conn, bufSize),
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
			if err := r.serveRouteGroup(ctx); err != nil && !strings.Contains(err.Error(), "use of closed network connection") {
				r.log.WithError(err).Error("serveRouteGroup stopped unexpectedly.")
			}
		}()
	}

	return lis, nil
}

// serveRouteGroup accepts and serves routes.
func (r *SkywireNetworker) serveRouteGroup(ctx context.Context) error {
	log := r.log.WithField("func", "serveRouteGroup")

	for {
		log.Debug("Awaiting to accept route group...")

		conn, err := r.r.AcceptRoutes(ctx)
		if err != nil {
			log.WithError(err).Info("Stopped accepting routes.")
			return err
		}

		log.
			WithField("local", conn.LocalAddr()).
			WithField("remote", conn.RemoteAddr()).
			Info("Accepted route group.")

		go r.serve(conn)
	}
}

// serveRG passes accepted router group to the corresponding listener.
func (r *SkywireNetworker) serve(conn net.Conn) {
	localAddr, ok := conn.LocalAddr().(routing.Addr)
	if !ok {
		r.close(conn)
		r.log.Error("wrong type of addr in accepted conn")

		return
	}

	lisIfc, ok := r.porter.PortValue(uint16(localAddr.Port))
	if !ok {
		r.close(conn)
		r.log.Errorf("no listener on port %d", localAddr.Port)

		return
	}

	lis, ok := lisIfc.(*skywireListener)
	if !ok {
		r.close(conn)
		r.log.Errorf("wrong type of listener on port %d", localAddr.Port)

		return
	}

	lis.putConn(conn)
}

// closeRG closes router group and logs error if any.
func (r *SkywireNetworker) close(closer io.Closer) {
	if err := closer.Close(); err != nil {
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
