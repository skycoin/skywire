// Package appnet pkg/app/appnet/skywire_networker.go
package appnet

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
	"github.com/skycoin/skywire/pkg/router"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/transport"
)

var (
	// ErrPortAlreadyBound is being returned when the desired port is already bound to.
	ErrPortAlreadyBound = errors.New("port already bound")
	// ErrClosedConn is being returned when we are listening on a closed connection.
	ErrClosedConn = errors.New("listening on closed connection")
)

// SkywireNetworker implements `Networker` for skynet.
type SkywireNetworker struct {
	log       logrus.FieldLogger
	r         router.Router
	porter    *netutil.Porter
	isServing int32
	MuxRoutes int // Number of parallel mux routes per connection (0 or 1 = disabled)

	// appDirectMux carries direct-transport app dials (route ID 0).
	// When non-nil, Dial tries it first before falling back to
	// route-setup-mediated DialRoutes. Server-side: an accept loop
	// dispatches inbound streams by destination port to whichever
	// listener registered with porter. Set via SetAppDirectMux at
	// visor init time; never changes thereafter.
	appDirectMux *transport.VStreamMux
	// appDirectAccepting is set on the first SetAppDirectMux call so
	// repeated calls (or late mux replacement) don't spawn duplicate
	// accept goroutines.
	appDirectAccepting int32
}

// directDialHandshakeTimeout bounds how long we'll wait for the
// destination visor to ack the port handshake on a direct dial.
// Direct dials should be near-instant (no setup-node round trip);
// 5s is generous and surfaces hung-server problems quickly.
const directDialHandshakeTimeout = 5 * time.Second

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
	return r.DialContextWithOptions(ctx, addr, nil)
}

// DialContextWithOptions dials remote `addr` via `skynet` with context and custom dial options.
//
// Tries the direct-transport path first when the visor has an
// AppDirectMux configured: opens a vstream over an existing direct
// transport to addr.PubKey and writes a 2-byte port header to the
// remote's accept loop. No setup-node, no rule installation, no
// handshake-await — just the transport's own framing. Falls back to
// the legacy route-setup-mediated DialRoutes path when there's no
// direct transport, or when the direct dial fails (e.g. remote
// doesn't expose the mux yet — older build).
func (r *SkywireNetworker) DialContextWithOptions(ctx context.Context, addr Addr, opts *router.DialOptions) (conn net.Conn, err error) {
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

	if opts == nil {
		opts = router.DefaultDialOptions()
	}
	if r.MuxRoutes > 1 && opts.MuxRoutes == 0 {
		opts.MuxRoutes = r.MuxRoutes
	}
	// If the caller threaded an app name on the context (e.g.
	// RPCIngressGateway.Dial), surface it on opts so router-side
	// rg-scoped logs get tagged with app_name=<n>.
	if opts.AppName == "" {
		opts.AppName = AppNameFromContext(ctx)
	}

	if directConn, ok := r.tryDirectDial(addr); ok {
		return &SkywireConn{
			Conn:     directConn,
			freePort: freePort,
		}, nil
	}

	conn, err = r.r.DialRoutes(ctx, addr.PubKey, routing.Port(localPort), addr.Port, opts)
	if err != nil {
		return nil, err
	}

	return &SkywireConn{
		Conn:     conn,
		nrg:      conn.(*router.NoiseRouteGroup),
		freePort: freePort,
	}, nil
}

// tryDirectDial attempts to dial `addr` via the AppDirectMux. Returns
// (conn, true) on success or (nil, false) on any failure — the caller
// then falls back to the route-setup path. Any error is logged at
// debug level since the fallback is the load-bearing path; we don't
// want to surface a noisy error every time direct happens not to be
// available.
func (r *SkywireNetworker) tryDirectDial(addr Addr) (net.Conn, bool) {
	mux := r.appDirectMux
	if mux == nil {
		return nil, false
	}
	stream, err := mux.Dial(addr.PubKey)
	if err != nil {
		r.log.WithField("remote", addr.PubKey.String()).
			WithError(err).
			Debug("Direct app dial: vstream dial failed, falling through to route setup")
		return nil, false
	}
	return r.finishDirectDial(stream, addr)
}

// finishDirectDial completes the port-handshake portion of a direct
// vstream dial. Split out from tryDirectDial so the ping path
// (tryDirectPingDial) can pin to a specific transport via
// DialByTransportID and then reuse the same handshake.
//
// Wire protocol on the stream:
//
//	client → server: 2-byte BE destination port
//	server → client: 1 ack byte (0x00 = listener found,
//	                              0x01 = no listener on that port)
//
// The ack lets us detect "older remote without AppDirect support"
// — those visors silently ignore unrecognized packet types, so the
// SYN is dropped on their side and our ack read times out. Either
// way, on no-ack we close and let the caller fall through to the
// route-setup path.
func (r *SkywireNetworker) finishDirectDial(stream *transport.VStream, addr Addr) (net.Conn, bool) {
	hdr := make([]byte, 2)
	binary.BigEndian.PutUint16(hdr, uint16(addr.Port))
	if _, err := stream.Write(hdr); err != nil {
		r.log.WithField("remote", addr.PubKey.String()).
			WithError(err).
			Debug("Direct dial: port-header write failed, falling through to route setup")
		stream.Close() //nolint:errcheck,gosec
		return nil, false
	}
	ack := make([]byte, 1)
	ackErr := readWithTimeout(stream, ack, directDialHandshakeTimeout)
	if ackErr != nil {
		r.log.WithField("remote", addr.PubKey.String()).
			WithError(ackErr).
			Debug("Direct dial: ack not received, falling through to route setup")
		stream.Close() //nolint:errcheck,gosec
		return nil, false
	}
	if ack[0] != 0x00 {
		r.log.WithField("remote", addr.PubKey.String()).
			WithField("port", addr.Port).
			Debug("Direct dial: remote rejected (no listener), falling through to route setup")
		stream.Close() //nolint:errcheck,gosec
		return nil, false
	}
	r.log.WithField("remote", addr.PubKey.String()).
		WithField("port", addr.Port).
		Debug("Direct dial succeeded (no setup-node)")
	return &directConn{VStream: stream, remote: addr.PubKey}, true
}

// readWithTimeout reads len(buf) bytes from r within d, since
// VStream doesn't natively support read deadlines. Goroutine leaks
// on timeout are bounded — the caller closes the stream after a
// timeout, which causes the in-flight Read to return io.EOF and
// the goroutine to exit.
func readWithTimeout(rdr io.Reader, buf []byte, d time.Duration) error {
	ch := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(rdr, buf)
		ch <- err
	}()
	select {
	case err := <-ch:
		return err
	case <-time.After(d):
		return errors.New("read timed out")
	}
}

// directConn wraps a transport vstream as a net.Conn for the app
// layer. VStream itself provides Read / Write / Close / RemotePK;
// directConn fills in the remaining net.Conn surface (addrs +
// deadlines). Apps that introspect LocalAddr / RemoteAddr only do
// so on route-based conns, so the addr stubs are minimal.
type directConn struct {
	*transport.VStream
	remote cipher.PubKey
}

func (c *directConn) LocalAddr() net.Addr                { return Addr{Net: TypeSkynet} }
func (c *directConn) RemoteAddr() net.Addr               { return Addr{Net: TypeSkynet, PubKey: c.remote} }
func (c *directConn) SetDeadline(_ time.Time) error      { return nil }
func (c *directConn) SetReadDeadline(_ time.Time) error  { return nil }
func (c *directConn) SetWriteDeadline(_ time.Time) error { return nil }
func (c *directConn) RemotePK() cipher.PubKey            { return c.remote }

// SetAppDirectMux installs the visor's AppDirectMux on this
// networker and starts the accept loop that dispatches inbound
// direct-dial streams to listeners by destination port. Safe to call
// once at visor init; repeated calls are no-ops after the loop is
// running.
func (r *SkywireNetworker) SetAppDirectMux(mux *transport.VStreamMux) {
	r.appDirectMux = mux
	if mux == nil {
		return
	}
	if !atomic.CompareAndSwapInt32(&r.appDirectAccepting, 0, 1) {
		return
	}
	go r.serveDirectMux(mux)
}

// serveDirectMux runs the inbound accept loop for direct-dial
// vstreams. For each accepted stream, reads the 2-byte destination
// port, looks up the corresponding listener via the porter, and
// hands the connection in. Streams whose port has no listener are
// closed.
func (r *SkywireNetworker) serveDirectMux(mux *transport.VStreamMux) {
	log := r.log.WithField("loop", "direct-app-mux")
	log.Info("Direct app-dial accept loop started")
	for {
		stream, err := mux.Accept()
		if err != nil {
			log.WithError(err).Debug("Direct app-dial accept loop stopped")
			return
		}
		go r.handleDirectStream(stream)
	}
}

// handleDirectStream reads the port header, finds the matching
// listener, and feeds the conn in. Errors are logged at debug; on
// any failure the stream is closed.
func (r *SkywireNetworker) handleDirectStream(stream *transport.VStream) {
	log := r.log.WithField("remote", stream.RemotePK().String())
	hdr := make([]byte, 2)
	// Bound the port-header read with our own goroutine timer since
	// VStream doesn't support read deadlines. Worst case a stuck
	// client just leaks one goroutine; a misbehaving peer can't
	// stall us further since we close the stream below.
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(stream, hdr)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			log.WithError(err).Debug("Direct app-dial: failed to read port header")
			stream.Close() //nolint:errcheck,gosec
			return
		}
	case <-time.After(directDialHandshakeTimeout):
		log.Debug("Direct app-dial: port-header read timed out")
		stream.Close() //nolint:errcheck,gosec
		return
	}
	port := binary.BigEndian.Uint16(hdr)
	lisIfc, ok := r.porter.PortValue(port)
	if !ok {
		log.WithField("port", port).Debug("Direct app-dial: no listener for port")
		// Send 0x01 NACK so the dialer doesn't time out waiting for
		// an ack that never comes; lets it fall through to the
		// route-setup path immediately.
		_, _ = stream.Write([]byte{0x01}) //nolint:errcheck
		stream.Close()                    //nolint:errcheck,gosec
		return
	}
	lis, ok := lisIfc.(*skywireListener)
	if !ok {
		log.WithField("port", port).Debug("Direct app-dial: porter slot is not a skywireListener")
		_, _ = stream.Write([]byte{0x01}) //nolint:errcheck
		stream.Close()                    //nolint:errcheck,gosec
		return
	}
	// 0x00 ACK — listener found, conn is being handed in.
	if _, err := stream.Write([]byte{0x00}); err != nil {
		log.WithError(err).Debug("Direct app-dial: ack write failed")
		stream.Close() //nolint:errcheck,gosec
		return
	}
	conn := &directConn{VStream: stream, remote: stream.RemotePK()}
	// putConn channels the conn into the listener's accept queue;
	// SkywireNetworker.serve isn't used on this path because we
	// already know the destination port (no LocalAddr lookup needed).
	// Accept will detect the pre-wrapped SkywireConn and skip the
	// nrg type-assert that the route-based path needs.
	lis.putConn(&SkywireConn{Conn: conn})
	log.WithField("port", port).Debug("Direct app-dial: handed to listener")
}

// Ping dials remote `addr` via `skynet`.
func (r *SkywireNetworker) Ping(pk cipher.PubKey, addr Addr) (net.Conn, error) {
	return r.PingContext(context.Background(), pk, addr)
}

// PingContext dials remote `addr` via `skynet` with context.
func (r *SkywireNetworker) PingContext(ctx context.Context, pk cipher.PubKey, addr Addr) (net.Conn, error) {
	return r.PingContextWithOpts(ctx, pk, addr, nil)
}

// PingContextWithOpts dials remote `addr` via `skynet` with context and custom dial options.
//
// Direct-transport bypass: when an AppDirectMux is configured and
// either (a) opts.TransportID names a transport to the peer or
// (b) any non-DMSG transport to the peer exists, try a vstream
// over that transport BEFORE falling back to the RSN-mediated
// PingRoute. Same mechanism that DialContextWithOptions uses for
// regular app dials. Cuts ping setup time from ~1-7s (RSN
// round-trip over dmsg) down to ~10ms (transport handshake only)
// — the difference shows up directly in `ping tree`'s setup-phase
// column when the operator and peer are both direct-transport
// reachable. Falls back transparently on no-direct-transport,
// no-mux, or older-peer-doesn't-support-AppDirect.
func (r *SkywireNetworker) PingContextWithOpts(ctx context.Context, pk cipher.PubKey, addr Addr, opts *router.DialOptions) (net.Conn, error) {
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

	if opts == nil {
		opts = router.DefaultDialOptions()
	}

	if directConn, ok := r.tryDirectPingDial(addr, opts); ok {
		return &SkywireConn{
			Conn:     directConn,
			freePort: freePort,
		}, nil
	}

	conn, err := r.r.PingRoute(ctx, pk, routing.Port(localPort), addr.Port, opts)
	if err != nil {
		return nil, err
	}

	return &SkywireConn{
		Conn:     conn,
		nrg:      conn.(*router.NoiseRouteGroup),
		freePort: freePort,
	}, nil
}

// tryDirectPingDial is the ping-flavor of tryDirectDial. When the
// caller has a specific transport in mind (opts.TransportID, set
// by `ping tree` measurements), pins the vstream to that transport
// — matters because the caller is trying to measure the latency
// of *that exact* transport, not "any direct one." When
// opts.TransportID is unset, picks any non-DMSG transport to the
// peer, same as the regular app-dial path.
//
// The port handshake and ack semantics are identical to
// tryDirectDial — see that function's comment for the wire protocol.
func (r *SkywireNetworker) tryDirectPingDial(addr Addr, opts *router.DialOptions) (net.Conn, bool) {
	mux := r.appDirectMux
	if mux == nil {
		return nil, false
	}
	var stream *transport.VStream
	var dialErr error
	if opts != nil && opts.TransportID != (uuid.UUID{}) {
		stream, dialErr = mux.DialByTransportID(addr.PubKey, opts.TransportID)
	} else {
		stream, dialErr = mux.Dial(addr.PubKey)
	}
	if dialErr != nil {
		r.log.WithField("remote", addr.PubKey.String()).
			WithError(dialErr).
			Debug("Direct ping dial: vstream dial failed, falling through to route setup")
		return nil, false
	}
	return r.finishDirectDial(stream, addr)
}

// Listen starts listening on local `addr` in the skynet.
func (r *SkywireNetworker) Listen(addr Addr) (net.Listener, error) {
	return r.ListenContext(context.Background(), addr)
}

// ListenContext starts listening on local `addr` in the skynet with context.
func (r *SkywireNetworker) ListenContext(ctx context.Context, addr Addr) (net.Listener, error) {
	const bufSize = 128

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
			if err := r.serveRouteGroup(ctx); err != nil && !errors.Is(err, net.ErrClosed) {
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
			// Check if shutting down (context canceled or connection closed)
			if ctx.Err() != nil || errors.Is(err, net.ErrClosed) {
				log.WithError(err).Debug("Stopped accepting routes.")
				return err
			}
			// Non-fatal error (e.g., missing transport for a stale route).
			// Log and continue accepting — don't kill the accept loop.
			log.WithError(err).Warn("Failed to accept route group, continuing...")
			continue
		}

		log.
			WithField("local", conn.LocalAddr()).
			WithField("remote", conn.RemoteAddr()).
			Debug("Accepted route group.")

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
		err := ErrServiceOffline(uint16(localAddr.Port))
		r.log.Error(err)
		if ng, ok := conn.(*router.NoiseRouteGroup); ok {
			ng.SetError(err)
		}
		r.close(conn)

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

// Accept accepts incoming connection. If the conn is already a
// *SkywireConn (direct app-dial path pre-wrapped it), pass it
// through; otherwise it's a route-group conn that still needs the
// NoiseRouteGroup wrap.
func (l *skywireListener) Accept() (net.Conn, error) {
	conn, ok := <-l.connsCh
	if !ok {
		return nil, ErrClosedConn
	}
	if sc, ok := conn.(*SkywireConn); ok {
		return sc, nil
	}
	return &SkywireConn{
		Conn: conn,
		nrg:  conn.(*router.NoiseRouteGroup),
	}, nil
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
