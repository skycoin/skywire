package dmsgpty

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/rpc"
	"net/url"
	"strings"
	"sync/atomic"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"

	dmsg "github.com/skycoin/dmsg/pkg/dmsg"
)

// Host represents the main instance of dmsgpty.
type Host struct {
	dmsgC *dmsg.Client
	wl    Whitelist

	cliN  int32
	connN int32
}

// NewHost creates a new dmsgpty.Host with a given dmsg.Client and whitelist.
func NewHost(dmsgC *dmsg.Client, wl Whitelist) *Host {
	host := new(Host)
	host.dmsgC = dmsgC
	host.wl = wl
	return host
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
				continue
			}
			if err == io.ErrClosedPipe || strings.Contains(err.Error(), "use of closed network connection") {
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
				continue
			}
			if err == io.ErrClosedPipe || err == dmsg.ErrEntityClosed ||
				strings.Contains(err.Error(), "use of closed network connection") {
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
		log.WithError(err).Panic("dmsgpty.Whitelist error.")
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
	mux.Handle(WhitelistURI, handleWhitelist(h))
	mux.Handle(PtyURI, handlePty(h))
	mux.Handle(PtyProxyURI, handleProxy(h))
	return mux
}

// dmsgEndpoints returns the endpoints served for remote dmsg connections.
func dmsgEndpoints(h *Host) (mux hostMux) {
	mux.Handle(PtyURI, handlePty(h))
	return mux
}

func handleWhitelist(h *Host) handleFunc {
	return func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error {
		return rpcS.RegisterName(WhitelistRPCName, NewWhitelistGateway(h.wl))
	}
}

func handlePty(h *Host) handleFunc {
	return func(ctx context.Context, uri *url.URL, rpcS *rpc.Server) error {
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
			return fmt.Errorf("invalid query value 'pk': %v", err)
		}
		var port uint16
		if _, err := fmt.Sscan(q.Get("port"), &port); err != nil {
			return fmt.Errorf("invalid query value 'port': %v", err)
		}

		// Proxy request.
		stream, err := h.dmsgC.DialStream(ctx, dmsg.Addr{PK: pk, Port: port})
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
