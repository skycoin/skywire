// Package server implements the exchange-market side of the client<->market
// protocol over a Skywire dmsg transport.
//
// It does not implement dmsg. The market Listens on appnet.TypeDmsg via the
// visor (app.Client.Listen); every accepted connection is an authenticated,
// end-to-end encrypted net.Conn whose RemoteAddr carries the client's visor
// public key. This package reads exchange frames off that conn, dispatches each
// request to a handler backed by the SQLite layer, and writes the response
// frame back. See exchange-design.md §8.
package server

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/internal/exchange-market/db"
	"github.com/skycoin/skywire/internal/exchange-market/protocol"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// Listener is the subset of *app.Client the server needs to accept dmsg
// connections. Both *app.Client and the exchange-market app wrapper satisfy it.
type Listener interface {
	Listen(n appnet.Type, port protocol.Port) (net.Listener, error)
}

// Server accepts client connections over dmsg and serves the exchange protocol.
type Server struct {
	db   *db.Database
	log  logrus.FieldLogger
	port protocol.Port

	mu       sync.Mutex
	sessions map[string]struct{} // active remote public keys (single-session enforcement)

	// allocMu serializes non-round-amount allocation + insert so two concurrent
	// listings/orders can't be assigned the same amount to the same wallet.
	allocMu sync.Mutex
}

// New creates a Server bound to the given database. A zero port falls back to
// protocol.DefaultPort.
func New(database *db.Database, log logrus.FieldLogger, port protocol.Port) *Server {
	if port == 0 {
		port = protocol.DefaultPort
	}
	if log == nil {
		log = logrus.New()
	}
	return &Server{
		db:       database,
		log:      log,
		port:     port,
		sessions: make(map[string]struct{}),
	}
}

// ListenAndServe listens on the dmsg app network and serves accepted clients
// until ctx is canceled or accepting fails. It blocks.
func (s *Server) ListenAndServe(ctx context.Context, l Listener) error {
	lis, err := l.Listen(appnet.TypeDmsg, s.port)
	if err != nil {
		return fmt.Errorf("listen dmsg on port %d: %w", s.port, err)
	}
	s.log.Infof("exchange-market: listening on dmsg port %d", s.port)

	// Unblock Accept on shutdown.
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()

	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil // clean shutdown
			}
			return fmt.Errorf("accept: %w", err)
		}
		go s.serveConn(conn)
	}
}

// serveConn resolves the authenticated identity, enforces single-session per
// public key, and runs the request loop.
func (s *Server) serveConn(conn net.Conn) {
	defer conn.Close() //nolint:errcheck

	raddr, ok := conn.RemoteAddr().(appnet.Addr)
	if !ok {
		s.log.Warnf("exchange-market: rejecting conn with non-appnet remote addr %v", conn.RemoteAddr())
		return
	}
	pk := raddr.PubKey.Hex()

	if !s.acquire(pk) {
		// Single-session enforcement: a second connection with the same public
		// key is rejected while the first is active (design §3, §8.4).
		s.log.Warnf("exchange-market: rejecting duplicate session for %s", pk)
		return
	}
	defer s.release(pk)

	s.log.Debugf("exchange-market: session opened for %s", pk)
	s.Serve(conn, pk)
	s.log.Debugf("exchange-market: session closed for %s", pk)
}

func (s *Server) acquire(pk string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.sessions[pk]; ok {
		return false
	}
	s.sessions[pk] = struct{}{}
	return true
}

func (s *Server) release(pk string) {
	s.mu.Lock()
	delete(s.sessions, pk)
	s.mu.Unlock()
}

// Serve runs the read/dispatch/write loop on an already-established connection
// for the authenticated remote public key. It returns when the connection ends.
// Exposed (transport-agnostic) so it can be driven over net.Pipe in tests.
func (s *Server) Serve(conn net.Conn, remotePK string) {
	fc := message.NewConn(conn)
	for {
		payload, err := fc.ReadFrame()
		if err != nil {
			if !errors.Is(err, net.ErrClosed) {
				s.log.Debugf("exchange-market: read from %s ended: %v", remotePK, err)
			}
			return
		}

		req, err := protocol.Unmarshal(payload)
		if err != nil {
			// Reply best-effort with a correlation-less error and keep serving;
			// a single malformed frame should not tear down the session.
			s.writeFrame(fc, protocol.ErrorResponse("", protocol.CodeInvalidRequest, "malformed request"))
			continue
		}

		resp := s.dispatch(remotePK, req)
		if !s.writeFrame(fc, resp) {
			return
		}
	}
}

// writeFrame marshals and writes one response frame, reporting success.
func (s *Server) writeFrame(fc *message.Conn, env protocol.Envelope) bool {
	out, err := env.Marshal()
	if err != nil {
		s.log.WithError(err).Error("exchange-market: failed to marshal response")
		return false
	}
	if err := fc.WriteFrame(out); err != nil {
		s.log.Debugf("exchange-market: write failed: %v", err)
		return false
	}
	return true
}

// dispatch routes a request to its handler by message type.
func (s *Server) dispatch(remotePK string, req protocol.Envelope) protocol.Envelope {
	switch req.Type {
	case protocol.TypeRegister:
		return s.handleRegister(remotePK, req)
	case protocol.TypeGetCurrencies:
		return s.handleGetCurrencies(req)
	case protocol.TypeGetProducts:
		return s.handleGetProducts(req)
	case protocol.TypeCreateListing:
		return s.handleCreateListing(remotePK, req)
	case protocol.TypeBuyProduct:
		return s.handleBuyProduct(remotePK, req)
	case protocol.TypeCancelListing:
		return s.handleCancelListing(remotePK, req)
	case protocol.TypeCancelOrder:
		return s.handleCancelOrder(remotePK, req)
	case protocol.TypeGetOrders:
		return s.handleGetOrders(remotePK, req)
	case protocol.TypeGetOrderStatus:
		return s.handleGetOrderStatus(remotePK, req)
	case protocol.TypeGetListings:
		return s.handleGetListings(remotePK, req)
	default:
		return protocol.ErrorResponse(req.ID, protocol.CodeInvalidRequest, "unknown message type: "+req.Type)
	}
}
