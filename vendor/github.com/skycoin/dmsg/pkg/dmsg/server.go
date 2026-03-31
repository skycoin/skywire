// Package dmsg pkg/dmsg/server.go
package dmsg

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
	"github.com/xtaci/smux"

	"github.com/skycoin/dmsg/pkg/disc"
	"github.com/skycoin/dmsg/pkg/dmsg/metrics"
)

// ErrClosed is returned when an operation is attempted on a closed server.
var ErrClosed = errors.New("server closed")

// PeerEntry represents a peer dmsg server to connect to.
type PeerEntry struct {
	PK   cipher.PubKey
	Addr string
}

// ServerConfig configues the Server
type ServerConfig struct {
	MaxSessions    int
	UpdateInterval time.Duration
	AuthPassphrase string
	Peers          []PeerEntry
}

// DefaultServerConfig returns the default server config.
func DefaultServerConfig() *ServerConfig {
	return &ServerConfig{
		MaxSessions:    DefaultMaxSessions,
		UpdateInterval: DefaultUpdateInterval,
	}
}

// Server represents a dsmg server entity.
type Server struct {
	EntityCommon

	m metrics.Metrics

	ready     chan struct{} // Closed once dmsg.Server is serving.
	readyOnce sync.Once

	done chan struct{}
	once sync.Once
	wg   sync.WaitGroup

	// Public TCP address which the dmsg server advertises itself as.
	// This should only be set once. Once set, addrDone closes.
	addr     string
	addrDone chan struct{}

	maxSessions int

	authPassphrase string

	// Peer server mesh support.
	peers          []PeerEntry
	peerPKs        map[cipher.PubKey]struct{} // set of known peer server PKs
	peerSessions   map[cipher.PubKey]*SessionCommon
	peerSessionsMx sync.Mutex
}

// NewServer creates a new dmsg server entity.
func NewServer(pk cipher.PubKey, sk cipher.SecKey, dc disc.APIClient, conf *ServerConfig, m metrics.Metrics) *Server {
	if conf == nil {
		conf = DefaultServerConfig()
	}
	if m == nil {
		m = metrics.NewEmpty()
	}
	log := logging.MustGetLogger("dmsg_server")

	s := new(Server)
	s.EntityCommon.init(pk, sk, dc, log, conf.UpdateInterval)
	s.m = m
	s.ready = make(chan struct{})
	s.done = make(chan struct{})
	s.addrDone = make(chan struct{})
	s.maxSessions = conf.MaxSessions
	s.setSessionCallback = func(ctx context.Context) error {
		s.sessionsMx.Lock()
		defer s.sessionsMx.Unlock()
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions, conf.AuthPassphrase)
	}
	s.delSessionCallback = func(ctx context.Context) error {
		s.sessionsMx.Lock()
		defer s.sessionsMx.Unlock()
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions, conf.AuthPassphrase)
	}
	s.authPassphrase = conf.AuthPassphrase

	// Initialize peer mesh.
	s.peers = conf.Peers
	s.peerPKs = make(map[cipher.PubKey]struct{}, len(conf.Peers))
	for _, p := range conf.Peers {
		s.peerPKs[p.PK] = struct{}{}
	}
	s.peerSessions = make(map[cipher.PubKey]*SessionCommon)
	s.peerSessionsFunc = func() []*SessionCommon {
		s.peerSessionsMx.Lock()
		defer s.peerSessionsMx.Unlock()
		sessions := make([]*SessionCommon, 0, len(s.peerSessions))
		for _, ses := range s.peerSessions {
			sessions = append(sessions, ses)
		}
		return sessions
	}

	return s
}

// GetSessions returns underlying sessions map.
func (s *Server) GetSessions() map[cipher.PubKey]*SessionCommon {
	s.sessionsMx.Lock()
	defer s.sessionsMx.Unlock()

	sessions := make(map[cipher.PubKey]*SessionCommon, len(s.sessions))
	for pk, session := range s.sessions {
		sessions[pk] = session
	}

	return sessions
}

// Close implements io.Closer
func (s *Server) Close() error {
	if s == nil {
		return nil
	}
	var closeErr error
	s.once.Do(func() {
		close(s.done)
		s.wg.Wait()
		closeErr = s.delEntry(context.Background())
		if closeErr != nil {
			s.log.Warn("Cannot delete entry from db.")
		}
	})
	return closeErr
}

// Serve serves the server.
func (s *Server) Serve(lis net.Listener, addr string) error {
	s.SetAdvertisedAddr(lis, &addr)

	log := s.log.
		WithField("advertised_addr", addr).
		WithField("local_pk", s.pk)

	log.Info("Serving server.")
	select {
	case <-s.done:
		return ErrClosed
	default:
		s.wg.Add(1)
	}
	defer func() {
		log.Info("Stopped server.")
		s.wg.Done()
	}()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		<-s.done
		cancel()
		log.WithError(lis.Close()).Info("Stopping server...")
	}()

	if err := s.startUpdateEntryLoop(ctx); err != nil {
		return err
	}

	s.connectToPeers(ctx)

	log.Info("Accepting sessions...")
	s.readyOnce.Do(func() { close(s.ready) })
	for {
		conn, err := lis.Accept()
		if err != nil {
			// If server is closed, there is no error to report.
			if isClosed(s.done) {
				return nil
			}
			return err
		}

		if s.SessionCount() >= s.maxSessions {
			s.log.
				WithField("max_sessions", s.maxSessions).
				WithField("remote_tcp", conn.RemoteAddr()).
				Debug("Max sessions is reached, but still accepting so clients who delegated us can still listen.")
		}

		s.wg.Add(1)
		go func(conn net.Conn) {
			defer s.wg.Done()
			defer func() {
				if err := recover(); err != nil {
					log.Warnf("panic in handleSession: %+v", err)
				}
			}()
			s.handleSession(conn)
		}(conn)
	}
}

func (s *Server) startUpdateEntryLoop(ctx context.Context) error {
	err := netutil.NewDefaultRetrier(s.log).Do(ctx, func() error {
		s.sessionsMx.Lock()
		defer s.sessionsMx.Unlock()
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.maxSessions, s.authPassphrase)
	})
	if err != nil {
		return err
	}

	go s.updateServerEntryLoop(ctx, s.AdvertisedAddr(), s.maxSessions, s.authPassphrase)
	return nil
}

// AdvertisedAddr returns the TCP address in which the dmsg server is advertised by.
// This is the TCP address that should be contained within the dmsg discovery entry of this server.
func (s *Server) AdvertisedAddr() string {
	<-s.addrDone
	return s.addr
}

// SetAdvertisedAddr sets the advertised TCP address in which the dmsg server is advertised by.
// This should only be called once.
func (s *Server) SetAdvertisedAddr(lis net.Listener, addr *string) {
	if *addr == "" {
		s.log.Warn("We are using a local addr as the advertised addr. This should only be done in a local test env.")
		*addr = lis.Addr().String()
	}
	s.addr = *addr
	close(s.addrDone)
}

// Ready returns a chan which blocks until the server begins serving.
func (s *Server) Ready() <-chan struct{} {
	return s.ready
}

func (s *Server) connectToPeers(ctx context.Context) {
	// Connect to statically configured peers.
	for _, peer := range s.peers {
		s.wg.Add(1)
		go func(peer PeerEntry) {
			defer s.wg.Done()
			s.maintainPeerConnection(ctx, peer)
		}(peer)
	}

	// Periodically discover other servers from discovery and peer with them.
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		s.discoverAndConnectPeers(ctx)
	}()
}

// discoverAndConnectPeers periodically queries discovery for all servers
// and establishes peer connections to any that aren't already connected.
func (s *Server) discoverAndConnectPeers(ctx context.Context) {
	// activePeers tracks goroutines managing discovered peer connections.
	activePeers := make(map[cipher.PubKey]context.CancelFunc)

	// Initial delay to let the server register itself first.
	select {
	case <-time.After(10 * time.Second):
	case <-ctx.Done():
		return
	}

	ticker := time.NewTicker(s.updateInterval)
	defer ticker.Stop()

	for {
		entries, err := s.dc.AllServers(ctx)
		if err != nil {
			s.log.WithError(err).Debug("Failed to discover peer servers.")
		} else {
			for _, entry := range entries {
				pk := entry.Static
				// Skip self and already-connected peers.
				if pk == s.pk {
					continue
				}
				if _, ok := activePeers[pk]; ok {
					continue
				}
				// Skip if already a static peer (handled by connectToPeers).
				s.peerSessionsMx.Lock()
				_, alreadyPeer := s.peerPKs[pk]
				if !alreadyPeer && entry.Server != nil && entry.Server.Address != "" {
					s.peerPKs[pk] = struct{}{}
				}
				s.peerSessionsMx.Unlock()
				if alreadyPeer {
					continue
				}
				if entry.Server == nil || entry.Server.Address == "" {
					continue
				}

				peerCtx, peerCancel := context.WithCancel(ctx) //nolint:gosec
				activePeers[pk] = peerCancel

				peer := PeerEntry{PK: pk, Addr: entry.Server.Address}
				s.wg.Add(1)
				go func() {
					defer s.wg.Done()
					s.maintainPeerConnection(peerCtx, peer)
				}()
			}
		}

		select {
		case <-ticker.C:
		case <-ctx.Done():
			// Cancel all discovered peer connections.
			for _, cancel := range activePeers {
				cancel()
			}
			return
		}
	}
}

func (s *Server) maintainPeerConnection(ctx context.Context, peer PeerEntry) {
	log := s.log.WithField("peer_pk", peer.PK).WithField("peer_addr", peer.Addr)
	bo := 5 * time.Second

	for {
		select {
		case <-ctx.Done():
			return
		case <-s.done:
			return
		default:
		}

		log.Info("Dialing peer server...")
		conn, err := net.DialTimeout("tcp", peer.Addr, 10*time.Second)
		if err != nil {
			log.WithError(err).Warn("Failed to dial peer server.")
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return
			}
			if bo < time.Minute {
				bo = time.Duration(float64(bo) * 1.5)
			}
			continue
		}

		ses := new(SessionCommon)
		if err := ses.initClient(&s.EntityCommon, conn, peer.PK); err != nil {
			log.WithError(err).Warn("Peer noise handshake failed.")
			conn.Close() //nolint:errcheck,gosec
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return
			}
			continue
		}

		ses.sm.mutx.Lock()
		ses.sm.yamux, err = yamux.Client(conn, yamux.DefaultConfig())
		if err != nil {
			ses.sm.mutx.Unlock()
			log.WithError(err).Warn("Peer yamux setup failed.")
			conn.Close() //nolint:errcheck,gosec
			select {
			case <-time.After(bo):
			case <-ctx.Done():
				return
			}
			continue
		}
		ses.sm.addr = ses.sm.yamux.RemoteAddr()
		ses.isPeer = true
		ses.sm.mutx.Unlock()

		s.peerSessionsMx.Lock()
		s.peerSessions[peer.PK] = ses
		s.peerSessionsMx.Unlock()

		log.Info("Connected to peer server.")
		bo = 5 * time.Second // reset backoff on success

		// Block until the yamux session closes or context is done.
		select {
		case <-ctx.Done():
		case <-s.done:
		}

		// Clean up.
		s.peerSessionsMx.Lock()
		delete(s.peerSessions, peer.PK)
		s.peerSessionsMx.Unlock()
		ses.Close() //nolint:errcheck,gosec

		log.Info("Peer session closed, will reconnect.")
	}
}

// isPeerPK returns true if the given PK is a known peer server.
func (s *Server) isPeerPK(pk cipher.PubKey) bool {
	s.peerSessionsMx.Lock()
	_, ok := s.peerPKs[pk]
	s.peerSessionsMx.Unlock()
	return ok
}

func (s *Server) handleSession(conn net.Conn) {
	defer func() {
		if r := recover(); r != nil {
			s.log.WithField("panic", r).
				WithField("remote_tcp", conn.RemoteAddr()).
				Error("Recovered from panic in handleSession, connection will be closed")
			if err := conn.Close(); err != nil {
				s.log.WithError(err).Warn("Failed to close connection after panic recovery")
			}
		}
	}()

	log := s.log.WithField("remote_tcp", conn.RemoteAddr())

	dSes, err := makeServerSession(s.m, &s.EntityCommon, conn)
	if err != nil {
		log.WithError(err).Warn("Failed to create server session")
		if err := conn.Close(); err != nil {
			log.WithError(err).Warn("On handleSession() failure, close connection resulted in error.")
		}
		return
	}
	log = log.WithField("remote_pk", dSes.RemotePK())

	// Mark session as peer if remote PK is a known peer server.
	if s.isPeerPK(dSes.RemotePK()) {
		dSes.isPeer = true
		log.Info("Started peer server session.")
	} else {
		log.Info("Started session.")
	}

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		awaitDone(ctx, s.done)
		log.WithError(dSes.Close()).Info("Stopped session.")
	}()
	// detect visor protocol for dmsg
	protocol := s.entryProtocol(ctx, dSes.RemotePK())

	// based on protocol, create smux or yamux stream session
	dSes.sm.mutx.Lock()
	if protocol == "smux" {
		dSes.sm.smux, err = smux.Server(conn, smux.DefaultConfig())
		if err != nil {
			dSes.sm.mutx.Unlock()
			conn.Close() //nolint:errcheck,gosec
			cancel()
			return
		}
		dSes.sm.addr = dSes.sm.smux.RemoteAddr()
		log.Infof("smux stream session initial for %s", dSes.RemotePK().String())
	} else {
		dSes.sm.yamux, err = yamux.Server(conn, yamux.DefaultConfig())
		if err != nil {
			dSes.sm.mutx.Unlock()
			conn.Close() //nolint:errcheck,gosec
			cancel()
			return
		}
		dSes.sm.addr = dSes.sm.yamux.RemoteAddr()
		log.Infof("yamux stream session initial for %s", dSes.RemotePK().String())
	}
	dSes.sm.mutx.Unlock()

	if s.setSession(ctx, dSes.SessionCommon) {
		dSes.Serve()
	}

	s.delSession(ctx, dSes.RemotePK())
	cancel()
}
