// Package dmsg pkg/dmsg/server.go
package dmsg

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/xtaci/smux"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/disc"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg/metrics"
	"github.com/skycoin/skywire/pkg/logging"
	"github.com/skycoin/skywire/pkg/netutil"
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

	// AnnounceAsPeer makes this server send a signed PeerAnnounce on
	// every outbound peer link it dials, asking the remote to treat
	// this server as a forwardable peer. Used by a non-public server
	// (not registered in discovery) so its clients become reachable
	// inbound via the link it dialed out.
	AnnounceAsPeer bool
	// AcceptPeerAnnouncements lets this server honor inbound PeerAnnounce
	// frames, filing the announcing session in its peer set so it
	// forwards client streams back down that link. When AcceptedPeerPKs
	// is non-empty it acts as an allowlist; empty means accept any
	// announcer (an open relay surface — set deliberately).
	AcceptPeerAnnouncements bool
	AcceptedPeerPKs         []cipher.PubKey
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
	//
	// addr is the IPv4 endpoint (legacy single-stack contract); addrV6
	// is the optional IPv6 endpoint, set independently via
	// SetAdvertisedAddrV6 before Serve when the operator wants the
	// server discoverable over both families. Empty addrV6 keeps the
	// pre-#1525 single-stack behavior — disc.Server.AddressV6 stays
	// empty and v4-only clients see no change.
	addr     string
	addrV6   string
	addrDone chan struct{}

	maxSessions int

	authPassphrase string

	// Peer server mesh support.
	peers          []PeerEntry
	peerPKs        map[cipher.PubKey]struct{} // set of known peer server PKs
	peerSessions   map[cipher.PubKey]*SessionCommon
	peerSessionsMx sync.Mutex

	// Inbound peer-announcement support (non-public servers).
	announceAsPeer          bool
	acceptPeerAnnouncements bool
	acceptedPeerPKs         map[cipher.PubKey]struct{} // allowlist; empty = accept any
}

// GeoLookupFunc returns geolocation data for the given IP. Returns
// zero values when the IP is non-public or the lookup fails — the
// caller passes the zero values straight through into the response
// and the dmsg client falls back to a local lookup on its end.
type GeoLookupFunc func(ip net.IP) (country, region string, lat, lon float64)

// SetGeoLookup registers an IP-to-geo lookup function. The function
// is called by the IP-info stream handler when the request has
// GeoInfo=true, so dmsg clients can get their public IP and its
// geolocation in one round-trip. Pass nil to disable. cmd/dmsg/dmsg-
// server wires the embedded MaxMind DB; pkg/dmsg/dmsg deliberately
// does not import pkg/geoip to avoid pulling the 62MB embedded DB
// into every dmsg-using binary.
func (s *Server) SetGeoLookup(fn GeoLookupFunc) {
	s.geoLookup = fn
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
	s.setSessionCallback = func(_ context.Context) error {
		s.nudgeEntryUpdate()
		return nil
	}
	s.delSessionCallback = func(_ context.Context) error {
		s.nudgeEntryUpdate()
		return nil
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

	// Inbound peer-announcement support: a non-public server announces
	// itself as a forwardable peer over its outbound links; an accepting
	// server promotes the announced inbound session into peerSessions.
	s.announceAsPeer = conf.AnnounceAsPeer
	s.acceptPeerAnnouncements = conf.AcceptPeerAnnouncements
	s.acceptedPeerPKs = make(map[cipher.PubKey]struct{}, len(conf.AcceptedPeerPKs))
	for _, pk := range conf.AcceptedPeerPKs {
		s.acceptedPeerPKs[pk] = struct{}{}
	}
	s.EntityCommon.acceptPeerAnnouncements = conf.AcceptPeerAnnouncements
	s.EntityCommon.peerAnnounceAllowedFunc = func(remotePK cipher.PubKey) bool {
		if !s.acceptPeerAnnouncements {
			return false
		}
		s.peerSessionsMx.Lock()
		defer s.peerSessionsMx.Unlock()
		if len(s.acceptedPeerPKs) == 0 {
			return true // empty allowlist = accept any announcer
		}
		_, ok := s.acceptedPeerPKs[remotePK]
		return ok
	}
	s.EntityCommon.promoteToPeerFunc = s.addAcceptedPeerSession

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
		// TCP_NODELAY on accepted visor sessions — symmetric with the
		// dial-side fix in client_sessions.go. Without matching it
		// here, response packets from server to visor would still
		// Nagle-batch even if the visor's outbound side was clean.
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true) //nolint:errcheck
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
		// updateServerEntry takes sessionsMx internally only for the
		// session-count snapshot; do NOT hold it across this call —
		// see the comment in EntityCommon.updateServerEntry.
		return s.updateServerEntry(ctx, s.AdvertisedAddr(), s.AdvertisedAddrV6(), s.maxSessions, s.authPassphrase)
	})
	if err != nil {
		return err
	}

	go s.updateServerEntryLoop(ctx, s.AdvertisedAddr(), s.AdvertisedAddrV6(), s.maxSessions, s.authPassphrase)
	return nil
}

// AdvertisedAddr returns the TCP address in which the dmsg server is advertised by.
// This is the TCP address that should be contained within the dmsg discovery entry of this server.
func (s *Server) AdvertisedAddr() string {
	<-s.addrDone
	return s.addr
}

// AdvertisedAddrV6 returns the optional IPv6 TCP address the dmsg server
// is advertised as. Empty when the server hasn't been configured for
// IPv6 (the default — preserves the pre-#1525 single-stack contract).
// Does NOT block on addrDone: v6 advertisement is independent of the
// primary v4 advertisement and may legitimately remain empty.
func (s *Server) AdvertisedAddrV6() string {
	return s.addrV6
}

// AddDiscovery registers an additional dmsg-discovery this server
// should publish its entry to. advertisedAddr, when non-empty,
// overrides the server's default AdvertisedAddr() for THIS discovery
// only — useful for advertising a LAN address to a local discovery
// and a public address to an internet discovery. discPK, when set,
// lets the server recognize an inbound DMSG session originated by
// this discovery and trigger an immediate registration push.
//
// Safe to call before or after Serve. Subsequent updateServerEntry
// iterations pick up the new endpoint automatically.
func (s *Server) AddDiscovery(client disc.APIClient, advertisedAddr string, discPK cipher.PubKey) {
	s.addDiscovery(client, advertisedAddr, "", discPK)
}

// AddDiscoveryDualStack is the dual-stack variant of AddDiscovery —
// adds an additional v6 endpoint advertised to this discovery alongside
// advertisedAddr. Empty advertisedAddrV6 is equivalent to calling
// AddDiscovery (v4-only for this endpoint). Use this when the same
// server should expose distinct {v4, v6} addresses to a particular
// discovery (e.g. LAN-v6 to a local discovery while still v4-public
// elsewhere).
func (s *Server) AddDiscoveryDualStack(client disc.APIClient, advertisedAddr, advertisedAddrV6 string, discPK cipher.PubKey) {
	s.addDiscovery(client, advertisedAddr, advertisedAddrV6, discPK)
}

// SetDiscoveryClients replaces the underlying disc.APIClient on each
// configured discovery endpoint, preserving advertised addresses and
// PKs. The lengths must match the current endpoint count, otherwise
// the call is a no-op. Used to upgrade plain HTTP clients to
// dmsgfirst-wrapped clients once the server's outbound dmsg.Client is
// available — registration then prefers DMSG over HTTP, which keeps
// the server reachable from local discoveries when the public internet
// is unavailable.
func (s *Server) SetDiscoveryClients(clients []disc.APIClient) {
	s.setDiscoveryClients(clients)
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

// SetAdvertisedAddrV6 sets the optional IPv6 advertised address. Must
// be called BEFORE Serve so the first updateServerEntry tick already
// carries the v6 endpoint. Empty addr is a no-op (leaves the server
// IPv4-only). Independent of SetAdvertisedAddr — they can be called in
// either order, and the absence of a v6 SetAdvertisedAddrV6 call is
// the default "no v6 advertisement" behavior for backward compat.
func (s *Server) SetAdvertisedAddrV6(addr string) {
	s.addrV6 = addr
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
		// TCP_NODELAY on the server↔server peer link. Same rationale
		// as the visor↔server side: small interactive payloads
		// routed across the dmsg mesh shouldn't pay 40–200ms of
		// Nagle batching at each hop.
		if tcpConn, ok := conn.(*net.TCPConn); ok {
			_ = tcpConn.SetNoDelay(true) //nolint:errcheck
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
		ses.sm.yamux, err = yamux.Client(conn, YamuxConfig())
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

		// Announce ourselves as a forwardable peer over this outbound
		// link so a non-public server (not in discovery) becomes
		// reachable inbound: the remote files this session in its peer
		// set and forwards client streams back down it.
		if s.announceAsPeer {
			if aErr := s.sendPeerAnnounce(ses); aErr != nil {
				log.WithError(aErr).Warn("Failed to announce as peer.")
			} else {
				log.Info("Announced as forwardable peer.")
			}
		}

		// Serve the outbound peer session so forwarded streams arriving
		// on it (the reverse path: the remote pushing a client stream
		// back down this link to one of our local clients) are accepted
		// and bridged. Without this the outbound link is send-only and
		// the reverse path cannot be received. Serve returns when the
		// session closes, which then triggers a reconnect.
		srvSes := ServerSession{SessionCommon: ses, m: s.m}
		serveDone := make(chan struct{})
		go func() {
			srvSes.Serve()
			close(serveDone)
		}()

		// Block until the session closes, the context is done, or shutdown.
		select {
		case <-ctx.Done():
		case <-s.done:
		case <-serveDone:
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

// sendPeerAnnounce opens a stream on the given (outbound) peer session,
// sends a signed PeerAnnounce, and waits for the remote's ack. Used on
// outbound peer links when AnnounceAsPeer is set, so a non-public server
// becomes reachable inbound.
func (s *Server) sendPeerAnnounce(ses *SessionCommon) error {
	ann := &PeerAnnounce{Timestamp: time.Now().UnixNano(), SrcPK: s.pk}
	obj, err := MakeSignedPeerAnnounce(ann, s.sk)
	if err != nil {
		return err
	}
	// Peer sessions are always yamux (maintainPeerConnection never uses smux).
	str, err := ses.sm.yamux.OpenStream()
	if err != nil {
		return err
	}
	defer str.Close()                                     //nolint:errcheck,gosec
	_ = str.SetDeadline(time.Now().Add(HandshakeTimeout)) //nolint:errcheck
	if err := ses.writeObject(str, obj); err != nil {
		return err
	}
	respObj, err := ses.readObject(str)
	if err != nil {
		return err
	}
	resp, err := respObj.ObtainStreamResponse()
	if err != nil {
		return err
	}
	if !resp.Accepted {
		return errors.New("peer announcement not accepted by remote")
	}
	return nil
}

// addAcceptedPeerSession files an announced inbound session as a
// forwardable peer: marks it isPeer, records the PK in peerPKs (so a
// reconnect classifies consistently), and inserts it into peerSessions
// so forwardViaPeer routes client streams down it. The reverse-path
// enabler for non-public servers; called via the promoteToPeerFunc hook
// from serveStream when an inbound announcement is accepted.
func (s *Server) addAcceptedPeerSession(remotePK cipher.PubKey, ses *SessionCommon) {
	s.peerSessionsMx.Lock()
	ses.isPeer = true
	s.peerPKs[remotePK] = struct{}{}
	s.peerSessions[remotePK] = ses
	s.peerSessionsMx.Unlock()
	s.log.WithField("peer_pk", remotePK).
		Info("Accepted inbound peer announcement; session is now forwardable.")
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
		dSes.sm.smux, err = smux.Server(conn, SmuxConfig())
		if err != nil {
			dSes.sm.mutx.Unlock()
			conn.Close() //nolint:errcheck,gosec
			cancel()
			return
		}
		dSes.sm.addr = dSes.sm.smux.RemoteAddr()
		log.Infof("smux stream session initial for %s", dSes.RemotePK().String())
	} else {
		dSes.sm.yamux, err = yamux.Server(conn, YamuxConfig())
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

	// Newest-session-wins: setSession always installs this session
	// (replacing and closing any stale predecessor), so always serve it.
	s.setSession(ctx, dSes.SessionCommon)
	dSes.Serve()

	// If this inbound session was promoted to a forwardable peer (an
	// accepted PeerAnnounce from a non-public server), drop it from the
	// peer set on close — identity-checked so a fresh reconnect that
	// already replaced us is not evicted. A statically-configured peer's
	// inbound session is isPeer too but is NOT in peerSessions (only the
	// outbound dial is), so the identity check makes this a no-op there.
	if dSes.isPeer {
		s.peerSessionsMx.Lock()
		if s.peerSessions[dSes.RemotePK()] == dSes.SessionCommon {
			delete(s.peerSessions, dSes.RemotePK())
		}
		s.peerSessionsMx.Unlock()
	}

	// Identity-checked: only remove the map entry if it is still THIS
	// session. setSession may have already replaced us with a fresh
	// reconnect (newest-session-wins); deleting by PK alone would evict
	// that live successor.
	s.delSession(ctx, dSes.RemotePK(), dSes.SessionCommon)
	cancel()
}
