// Package dmsgpty pkg/dmsgpty/host.go
package dmsgpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
	dmsg "github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/logging"
)

// Host represents the main instance of dmsgpty.
//
// dmsgC is kept for the listening side (ListenAndServe) and logger
// access; the outbound proxy-dial side now goes through `dialer` so
// callers can plug in a transport-aware StreamDialer. NewHost wires
// the default dmsg adapter so behavior is unchanged when callers
// don't opt in.
type Host struct {
	dmsgC  *dmsg.Client
	dialer StreamDialer
	wl     Whitelist

	cliN  int32
	connN int32
}

// NewHost creates a new dmsgpty.Host with a given dmsg.Client and
// whitelist. Outbound proxy dials go over dmsg via the default
// adapter (pre-refactor behavior). Callers that want the outbound
// side to use a different transport — typically the visor with its
// transport.Manager — should use NewHostWithDialer instead.
func NewHost(dmsgC *dmsg.Client, wl Whitelist) *Host {
	return NewHostWithDialer(dmsgC, wl, dmsgDialer{c: dmsgC})
}

// NewHostWithDialer constructs a Host with an explicit outbound
// StreamDialer. dmsgC is still required for the listening side
// (ListenAndServe binds the dmsg port for incoming proxy requests)
// and for logger plumbing — only the OUTBOUND proxy dial flows
// through the supplied dialer.
//
// Pass dmsgDialer{c: dmsgC} to keep dmsg-only outbound (equivalent
// to NewHost). Pass a transport-aware adapter (Phase 2) to let
// `cli dmsg pty start` ride the visor's existing transports.
func NewHostWithDialer(dmsgC *dmsg.Client, wl Whitelist, dialer StreamDialer) *Host {
	host := new(Host)
	host.dmsgC = dmsgC
	host.dialer = dialer
	host.wl = wl
	return host
}

// ExecRemote runs a one-shot command on the remote dmsgpty host at
// (rPK, rPort) using this host's dmsg client. Equivalent to what a CLI
// client would obtain via the proxy path, but skips the local CLI
// socket entirely — callers that already hold a Host (notably the
// visor's own RPC layer) can drive Exec without the intermediate
// unix-socket-or-tcp control listener. Same trust model: the remote's
// whitelist gates the connection on its own PK admission.
func (h *Host) ExecRemote(ctx context.Context, rPK cipher.PubKey, rPort uint16, req *CommandExecReq) (*CommandExecResult, error) {
	return h.ExecRemoteVia(ctx, h.dialer, rPK, rPort, req)
}

// ExecRemoteVia runs ExecRemote against the supplied dialer instead
// of the Host's configured one. Used when the caller wants to pin
// the transport (e.g. dmsg-only / skynet-only) rather than ride the
// Host's MultiDialer chain. Pass dmsgpty.NewDmsgDialer(c) to force
// dmsg, or the visor's skywireDialer to force skynet.
//
// Same protocol, same wire shape — the dialer only changes the
// underlying transport that carries the dmsgpty stream.
func (h *Host) ExecRemoteVia(ctx context.Context, dialer StreamDialer, rPK cipher.PubKey, rPort uint16, req *CommandExecReq) (*CommandExecResult, error) {
	if rPort == 0 {
		rPort = DefaultPort
	}
	stream, err := dialer.DialStream(ctx, rPK, rPort)
	if err != nil {
		return nil, fmt.Errorf("dmsgpty: dial %s:%d: %w", rPK, rPort, err)
	}
	defer stream.Close() //nolint:errcheck

	ptyC, err := NewPtyClient(stream)
	if err != nil {
		return nil, fmt.Errorf("dmsgpty: new pty client: %w", err)
	}
	defer ptyC.Close() //nolint:errcheck

	return ptyC.Exec(req)
}

// ServeCLI listens for CLI connections via the provided listener.
func (h *Host) ServeCLI(ctx context.Context, lis net.Listener) error {

	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	log := logging.MustGetLogger("dmsg_pty:cli-server")
	masterLogger := h.dmsgC.MasterLogger()
	if masterLogger != nil {
		log = masterLogger.PackageLogger("dmsg_pty:cli-server")
	}

	mux := cliEndpoints(h)

	for {
		conn, err := lis.Accept()
		if err != nil {
			// TODO (ersonp): Temporary has been depricaited but there is no replacement for it
			// since ServeCLI is based on Serve of `net/http.Server` https://github.com/golang/go/blob/ab9d31da9e088a271e656120a3d99cd3b1103ab6/src/net/http/server.go#L3047-L3059
			// and it is still using Temporary we should keep an eye on it and make changes when it's changed there.
			// This is the main comment for reference https://github.com/golang/go/issues/45729#issuecomment-1104607098
			if err, ok := err.(net.Error); ok && err.Temporary() { //nolint
				log.Warn("Failed to accept CLI connection with temporary error, continuing...")
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if err == io.ErrClosedPipe || errors.Is(err, net.ErrClosed) {
				log.Debug("Cleanly stopped serving.")
				return nil
			}
			log.Error("Failed to accept CLI connection with permanent error.")
			return err
		}

		log := log.WithField("cli_id", atomic.AddInt32(&h.cliN, 1))
		log.Debug("CLI connection accepted.")
		go func() {
			h.serveConn(ctx, log, &mux, conn)
			atomic.AddInt32(&h.cliN, -1)
		}()
	}
}

// ListenAndServe serves the host over the dmsg network via the given dmsg port.
func (h *Host) ListenAndServe(ctx context.Context, port uint16) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := dmsgEndpoints(h)

	lis, err := h.dmsgC.Listen(port)
	if err != nil {
		return err
	}

	log := logging.MustGetLogger("dmsg_pty")
	masterLogger := h.dmsgC.MasterLogger()
	if masterLogger != nil {
		log = masterLogger.PackageLogger("dmsg_pty")
	}

	go func() {
		<-ctx.Done()
		log.
			WithError(lis.Close()).
			Debug("Serve() ended.")
	}()

	for {
		stream, err := lis.AcceptStream()
		if err != nil {
			log := log.WithError(err)
			// TODO (ersonp): Temporary has been depricaited but there is no replacement for it
			// since ListenAndServe is based on Serve of `net/http.Server` https://github.com/golang/go/blob/ab9d31da9e088a271e656120a3d99cd3b1103ab6/src/net/http/server.go#L3047-L3059
			// and it is still using Temporary we should keep an eye on it and make changes when it's changed there.
			// This is the main comment for reference https://github.com/golang/go/issues/45729#issuecomment-1104607098
			if err, ok := err.(net.Error); ok && err.Temporary() { //nolint
				log.Warn("Failed to accept dmsg.Stream with temporary error, continuing...")
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if err == io.ErrClosedPipe || err == dmsg.ErrEntityClosed ||
				errors.Is(err, net.ErrClosed) {
				log.Debug("Cleanly stopped serving.")
				return nil
			}
			log.Error("Failed to accept dmsg.Stream with permanent error.")
			return err
		}

		rPK := stream.RawRemoteAddr().PK
		log := log.WithField("remote_pk", rPK.String())
		log.Debug("Processing dmsg.Stream...")

		if !h.authorize(log, rPK) {
			err := writeResponse(stream,
				errors.New("dmsg stream rejected by whitelist"))
			log.WithError(err).Warn()

			if err := stream.Close(); err != nil {
				log.WithError(err).Warn("Stream closed with error.")
			}
			continue
		}

		log = log.WithField("conn_id", atomic.AddInt32(&h.connN, 1))
		log.Debug("dmsg.Stream accepted.")
		log = stream.Logger().WithField("dmsgpty", "stream")
		go func() {
			h.serveConn(ctx, log, &mux, stream)
			atomic.AddInt32(&h.connN, -1)
		}()
	}
}

// PKExtractor pulls the remote public key from an accepted conn so
// ListenAndServeNet can drive whitelist authorization on transports
// that aren't dmsg. Returns (pk, true) on success; (zero, false)
// causes the conn to be rejected before any pty handshake happens
// — the same defense-in-depth that the dmsg path's whitelist check
// provides. Callers wrap their listener's conn type (typically
// appnet.SkywireConn) and read the embedded address.
type PKExtractor func(conn net.Conn) (cipher.PubKey, bool)

// ListenAndServeNet serves dmsgpty over an arbitrary net.Listener.
// Generic counterpart of ListenAndServe (which is hard-bound to
// dmsg.Listener for AcceptStream + dmsg-specific error sentinels);
// this method handles only the generic surface: accept → extract PK
// → authorize → fan out to serveConn.
//
// Use case: visor wires a parallel listener on appnet.SkywireNetworker
// so dmsgpty traffic can ride skynet routes in addition to dmsg.
// Each transport gets its own listener call and they run in
// parallel — accepted conns are served via the same mux as the
// dmsg path, so the wire protocol is identical regardless of
// transport.
//
// extractPK must be non-nil; nil short-circuits to a rejected conn
// because skipping whitelist enforcement would silently weaken the
// dmsgpty trust model.
func (h *Host) ListenAndServeNet(ctx context.Context, lis net.Listener, extractPK PKExtractor) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	mux := dmsgEndpoints(h)

	log := logging.MustGetLogger("dmsg_pty_net")
	if h.dmsgC != nil {
		if ml := h.dmsgC.MasterLogger(); ml != nil {
			log = ml.PackageLogger("dmsg_pty_net")
		}
	}

	go func() {
		<-ctx.Done()
		log.WithError(lis.Close()).Debug("ListenAndServeNet() ended.")
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			log := log.WithError(err)
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() { //nolint:staticcheck
				log.Warn("Failed to accept net.Conn with temporary error, continuing...")
				time.Sleep(50 * time.Millisecond)
				continue
			}
			if err == io.ErrClosedPipe || errors.Is(err, net.ErrClosed) {
				log.Debug("Cleanly stopped serving.")
				return nil
			}
			log.Error("Failed to accept net.Conn with permanent error.")
			return err
		}

		var rPK cipher.PubKey
		var ok bool
		if extractPK != nil {
			rPK, ok = extractPK(conn)
		}
		if !ok {
			log.Warn("Accepted conn rejected: PK extraction failed (nil extractor or non-skywire conn).")
			_ = conn.Close() //nolint:errcheck,gosec
			continue
		}

		logC := log.WithField("remote_pk", rPK.String())
		if !h.authorize(logC, rPK) {
			err := writeResponse(conn, errors.New("net stream rejected by whitelist"))
			logC.WithError(err).Warn()
			if err := conn.Close(); err != nil {
				logC.WithError(err).Warn("Conn closed with error.")
			}
			continue
		}

		logC = logC.WithField("conn_id", atomic.AddInt32(&h.connN, 1))
		logC.Debug("net.Conn accepted.")
		go func() {
			h.serveConn(ctx, logC, &mux, conn)
			atomic.AddInt32(&h.connN, -1)
		}()
	}
}

// serveConn serves a CLI connection or dmsg stream.
func (h *Host) serveConn(ctx context.Context, log logrus.FieldLogger, mux *hostMux, conn net.Conn) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	closeErr := make(chan error, 1)
	go func() {
		<-ctx.Done()
		closeErr <- conn.Close()
		close(closeErr)
	}()

	log.WithError(mux.ServeConn(ctx, conn)).
		WithField("error_close", <-closeErr).
		WithField("remote_addr", conn.RemoteAddr()).
		Debug("Stopped serving connection.")
}

// authorize returns true if the provided public key is whitelisted.
func (h *Host) authorize(log logrus.FieldLogger, rPK cipher.PubKey) bool {
	ok, err := h.wl.Get(rPK)
	if err != nil {
		log.WithError(err).Error("dmsgpty.Whitelist error.")
		return false
	}
	if !ok {
		log.Warn("Public key rejected by whitelist.")
		return false
	}
	return true
}

// log returns the logrus.FieldLogger that should be used for all log outputs.
func (h *Host) log() logrus.FieldLogger {
	return h.dmsgC.Logger().WithField("dmsgpty", "host")
}

/*
	<<< ENDPOINTS >>>
*/

// cliEndpoints returns the endpoints served for CLI connections.
func cliEndpoints(h *Host) (mux hostMux) {
	mux.Handle(WhitelistURI, handleWhitelist(h)) //nolint:errcheck,gosec
	mux.Handle(PtyURI, handlePty(h))             //nolint:errcheck,gosec
	mux.Handle(PtyProxyURI, handleProxy(h))      //nolint:errcheck,gosec
	return mux
}

// dmsgEndpoints returns the endpoints served for remote dmsg connections.
func dmsgEndpoints(h *Host) (mux hostMux) {
	mux.Handle(PtyURI, handlePty(h)) //nolint:errcheck,gosec
	return mux
}

func handleWhitelist(h *Host) handleFunc {
	//	return func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error {
	return func(_ context.Context, _ *url.URL, rpcS *rpc.Server) error {
		return rpcS.RegisterName(WhitelistRPCName, NewWhitelistGateway(h.wl))
	}
}

func handlePty(h *Host) handleFunc {
	//	return func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error {
	return func(ctx context.Context, _ *url.URL, rpcS *rpc.Server) error {
		pty := NewPty()
		go func() {
			<-ctx.Done()
			h.log().
				WithError(pty.Stop()).
				Debug("PTY stopped.")
		}()
		return rpcS.RegisterName(PtyRPCName, NewPtyGateway(pty))
	}
}

func handleProxy(h *Host) handleFunc {
	return func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error {
		q := uri.Query()

		// Get query values.
		var pk cipher.PubKey
		if err := pk.Set(q.Get("pk")); err != nil {
			return fmt.Errorf("invalid query value 'pk': %w", err)
		}
		var port uint16
		if _, err := fmt.Sscan(q.Get("port"), &port); err != nil {
			return fmt.Errorf("invalid query value 'port': %w", err)
		}

		// Proxy request.
		stream, err := h.dialer.DialStream(ctx, pk, port)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			h.log().
				WithError(stream.Close()).
				Debug("Closed proxy dmsg stream.")
		}()

		ptyC, err := NewPtyClient(stream)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			h.log().
				WithError(ptyC.Close()).
				Debug("Closed proxy pty client.")
		}()
		return rpcS.RegisterName(PtyRPCName, NewProxyGateway(ptyC))
	}
}
