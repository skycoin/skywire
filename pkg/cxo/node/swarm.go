package node

import (
	"bytes"
	"errors"
	"fmt"
	mathrand "math/rand"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skycoin/src/cipher"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
)

var (
	errPeerNotFound = errors.New("peer not found")
)

// Peer
type Peer struct {
	PubKey     cipher.PubKey
	Metadata   []byte
	TCPAddr    string
	UDPAddr    string
	LastSeen   time.Time
	RetryTimes uint64
}

func msgToPeer(m msg.PeerInfo) Peer {
	p := Peer{
		PubKey:   m.PubKey,
		Metadata: m.Metadata,
		TCPAddr:  m.TCPAddr,
		UDPAddr:  m.UDPAddr,
	}

	return p
}

func peerToMsg(p Peer) msg.PeerInfo {
	m := msg.PeerInfo{
		PubKey:   p.PubKey,
		Metadata: p.Metadata,
		TCPAddr:  p.TCPAddr,
		UDPAddr:  p.UDPAddr,
	}

	return m
}

func (p *Peer) update(pi msg.PeerInfo) bool {
	needUpdate := bytes.Compare(p.Metadata, pi.Metadata) != 0 || //nolint:staticcheck
		p.TCPAddr != pi.TCPAddr ||
		p.UDPAddr != pi.UDPAddr

	if needUpdate {
		p.Metadata = pi.Metadata
		p.TCPAddr = pi.TCPAddr
		p.UDPAddr = pi.UDPAddr
	}

	return needUpdate
}

func (p *Peer) seen() {
	p.LastSeen = time.Now()
}

// Swarm
type Swarm struct {
	cfg  SwarmConfig
	feed cipher.PubKey

	node *Node

	quit chan struct{}
	done chan struct{}

	mu sync.RWMutex

	peers map[cipher.PubKey]Peer
}

func newSwarm(
	n *Node, f cipher.PubKey, cfg SwarmConfig) *Swarm {

	// Set default configuration if necessary
	dcfg := DefaultSwarmConfig()

	if cfg.MaxPeers == 0 {
		cfg.MaxPeers = dcfg.MaxPeers
	}
	if cfg.RequestPeerRate == 0 {
		cfg.RequestPeerRate = dcfg.RequestPeerRate
	}
	if cfg.PeerExpirePeriod == 0 {
		cfg.PeerExpirePeriod = dcfg.PeerExpirePeriod
	}
	if cfg.ClearOldPeersRate == 0 {
		cfg.ClearOldPeersRate = dcfg.ClearOldPeersRate
	}
	if cfg.MaxConns == 0 {
		cfg.MaxConns = dcfg.MaxConns
	}
	if cfg.OutgoingConnRate == 0 {
		cfg.OutgoingConnRate = dcfg.OutgoingConnRate
	}
	if cfg.PeersPerResponse == 0 {
		cfg.PeersPerResponse = dcfg.PeersPerResponse
	}

	s := &Swarm{
		cfg:  cfg,
		feed: f,

		node: n,

		quit: make(chan struct{}),
		done: make(chan struct{}),

		peers: make(map[cipher.PubKey]Peer),
	}

	return s
}

func (s *Swarm) AddPeer(
	pk cipher.PubKey, meta []byte, tcp, udp string) error {

	if err := s.validatePeer(pk, tcp, udp); err != nil {
		return fmt.Errorf("invalid peer: %s", err)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if uint64(len(s.peers)) >= s.cfg.MaxPeers {
		return fmt.Errorf("maximum number of peers exceeded")
	}

	p := Peer{
		PubKey:   pk,
		Metadata: meta,
		TCPAddr:  tcp,
		UDPAddr:  udp,
		LastSeen: time.Now(),
	}
	s.peers[pk] = p

	return nil
}

func (s *Swarm) RemovePeer(pk cipher.PubKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.peers[pk]; !ok {
		return errPeerNotFound
	}

	delete(s.peers, pk)

	return nil
}

func (s *Swarm) Peers() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peers := make([]Peer, 0, len(s.peers))
	for _, p := range s.peers {
		peers = append(peers, p)
	}

	return peers
}

func (s *Swarm) run() {
	requestPeers := time.NewTicker(s.cfg.RequestPeerRate)
	clearOldPeers := time.NewTicker(s.cfg.ClearOldPeersRate)
	outgoingConn := time.NewTicker(s.cfg.OutgoingConnRate)
	defer requestPeers.Stop()
	defer clearOldPeers.Stop()
	defer outgoingConn.Stop()

	for {
		select {
		case <-s.quit:
			close(s.done)
			return

		case <-requestPeers.C:
			if s.needPeers() {
				s.requestPeers()
			}

		case <-clearOldPeers.C:
			s.clearOldPeers()

		case <-outgoingConn.C:
			if count, ok := s.needConns(); ok {
				s.createOutgoingConns(count)
			}
		}
	}
}

func (s *Swarm) shutdown() {
	close(s.quit)
	<-s.done
}

func (s *Swarm) requestPeers() {
	var (
		conns = s.node.ConnectionsOfFeed(s.feed)
		wg    sync.WaitGroup
	)

	for i := range conns {
		wg.Add(1)

		go func(c *Conn) {
			s.node.Debugf(PEXPin, "requesting peers for feed %s, peer %s, addr %s",
				s.feed.Hex()[:8], c.PeerID().Hex()[:8], c.Address())

			defer wg.Done()

			// Send request
			req := &msg.RqPeers{
				Feed: s.feed,
			}
			resp, err := c.sendRequest(req)
			if err != nil {
				s.node.Errorf(err, "failed to send request for feed %s, peer %s, addr %s",
					s.feed.Hex()[:8], c.PeerID().Hex()[:8], c.Address())
				// TODO: maybe call s.incPeerRetryTimes(c.PeerID())
				return
			}

			// Handle response
			switch m := resp.(type) {
			case *msg.Peers:
				if m.Feed != s.feed {
					err = errors.New("received peers for wrong feed")
					// TODO: maybe call s.incPeerRetryTimes(c.PeerID())
				}
				s.addPeers(m.List)

			case *msg.Err:
				err = errors.New(m.Err)
				// TODO: maybe call s.incPeerRetryTimes(c.PeerID())

			default:
				err = errors.New("received unexpected message")
				// TODO: maybe call s.incPeerRetryTimes(c.PeerID())
			}

			if err != nil {
				s.node.Errorf(err, "failed to request peers for feed %s, peer %s, addr %s",
					s.feed.Hex()[:8], c.PeerID().Hex()[:8], c.Address())
			}
		}(conns[i])
	}
}

func (s *Swarm) needPeers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()

	result := s.cfg.MaxPeers > 0 &&
		uint64(len(s.peers)) < s.cfg.MaxPeers

	return result
}

func (s *Swarm) addPeers(peers []msg.PeerInfo) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.node.Debugf(PEXPin, "adding %d peers for feed %s",
		len(peers), s.feed.Hex()[:8])

	if s.cfg.MaxPeers > 0 && s.cfg.MaxPeers <= uint64(len(s.peers)) {
		s.node.Debugf(PEXPin, "feed %s already have maximum number of peers",
			s.feed.Hex()[:8])
		return
	}

	// Validate peers
	var validPeers []msg.PeerInfo
	for _, pi := range peers {
		if ok, _ := s.isSelf(pi); ok {
			continue
		}
		if err := s.validatePeer(pi.PubKey, pi.TCPAddr, pi.UDPAddr); err != nil {
			s.node.Errorf(err, "failed to add peer %s for feed %s",
				pi.PubKey.Hex()[:8], s.feed.Hex()[:8])
			continue
		}
		validPeers = append(validPeers, pi)
	}
	peers = validPeers

	// Shuffle and cap peers
	mathrand.Shuffle(len(peers), func(i, j int) {
		peers[i], peers[j] = peers[j], peers[i]
	})
	if s.cfg.MaxPeers > 0 {
		rcap := s.cfg.MaxPeers - uint64(len(s.peers))
		if uint64(len(peers)) > rcap {
			s.node.Debugf(PEXPin, "capping number of peers to be added for feed %s to %d",
				s.feed.Hex()[:8], rcap)
			peers = peers[:rcap]
		}
	}

	// Update existing peers or add new ones
	for _, pi := range peers {
		p, ok := s.peers[pi.PubKey]
		if ok {
			// Existing peer — update last seen time and metadata
			s.node.Debugf(PEXPin, "updating last seen time of peer %s for feed %s",
				pi.PubKey.Hex()[:8], s.feed.Hex()[:8])
			p.seen()

			if updated := p.update(pi); updated {
				s.node.Debugf(PEXPin, "updating info about peer %s for feed %s",
					pi.PubKey.Hex()[:8], s.feed.Hex()[:8])
				s.onPeerUpdated(p)
			}
		} else {
			// New peer — add it
			s.node.Debugf(PEXPin, "adding new peer %s for feed %s",
				pi.PubKey.Hex()[:8], s.feed.Hex()[:8])
			p = msgToPeer(pi)
			s.onPeerAdded(p)
		}

		s.peers[p.PubKey] = p
	}
}

func (s *Swarm) isSelf(p msg.PeerInfo) (bool, string) {
	if p.PubKey == s.node.ID() {
		return true, "pubkey is same"
	}
	if p.TCPAddr == s.node.TCP().Address() {
		return true, "tcp address is same"
	}
	if p.UDPAddr == s.node.UDP().Address() {
		return true, "udp address is saem"
	}

	return false, ""
}

func (s *Swarm) clearOldPeers() {
	now := time.Now()

	s.mu.Lock()
	defer s.mu.Unlock()

	for _, p := range s.peers {
		if now.Sub(p.LastSeen) > s.cfg.PeerExpirePeriod {
			s.node.Debugf(PEXPin, "removing expired peer %s from feed %s",
				p.PubKey.Hex()[:8], s.feed.Hex()[:8])

			delete(s.peers, p.PubKey)
			s.onPeerRemoved(p)
		}
	}
}

func (s *Swarm) onPeerAdded(p Peer) {
	if s.node.config.OnPeerAdded != nil {
		go s.node.config.OnPeerAdded(s.feed, p)
	}
}

func (s *Swarm) onPeerUpdated(p Peer) {
	if s.node.config.OnPeerUpdated != nil {
		go s.node.config.OnPeerUpdated(s.feed, p)
	}
}

func (s *Swarm) onPeerRemoved(p Peer) {
	if s.node.config.OnPeerRemoved != nil {
		go s.node.config.OnPeerRemoved(s.feed, p)
	}
}

func (s *Swarm) needConns() (uint64, bool) {
	var (
		connCap     = uint64(s.node.connCap())        //nolint:gosec,gofmt
		pendConnCap = uint64(s.node.pendingConnCap()) //nolint:gosec
		feedConns   = uint64(len(s.node.ConnectionsOfFeed(s.feed)))
	)

	if connCap == 0 ||
		pendConnCap == 0 ||
		feedConns >= s.cfg.MaxConns {

		return 0, false
	}

	needFeedConns := s.cfg.MaxConns - feedConns
	if needFeedConns >= connCap {
		return connCap, true
	}

	return needFeedConns, true
}

func (s *Swarm) createOutgoingConns(count uint64) {
	conns := make(map[cipher.PubKey]struct{})
	for _, c := range s.node.Connections() {
		conns[c.PeerID()] = struct{}{}
	}

	var (
		noConn = func(p Peer) bool {
			_, ok := conns[p.PubKey]
			return !ok
		}

		peers = s.randomPeers(int(count), noConn) //nolint:gosec

		wg sync.WaitGroup
	)

	for i := range peers {
		wg.Add(1)

		go func(p Peer) {
			s.node.Debugf(PEXPin, "connecting to peer %s, sharing feed %s",
				p.PubKey.Hex()[:8], s.feed.Hex()[:8])

			defer wg.Done()

			var (
				conn *Conn
				err  error
			)

			conn, err = s.node.TCP().Connect(p.TCPAddr)
			if err != nil {
				s.node.Errorf(err, "failed to connect to peer %s, sharing feed %s",
					p.PubKey.Hex()[:8], s.feed.Hex()[:8])
			} else {
				if err = conn.Subscribe(s.feed); err != nil {
					s.node.Errorf(err, "failed to subscribe for feed %s, shared by peer %s",
						s.feed.Hex()[:8], p.PubKey.Hex()[:8])
				}
			}

			if err != nil {
				s.incPeerRetryTimes(p.PubKey) //nolint:errcheck,gosec
			} else {
				s.resetPeerRetryTimes(p.PubKey) //nolint:errcheck,gosec
			}
		}(peers[i])
	}
}

type peerFilter func(Peer) bool

func (s *Swarm) incPeerRetryTimes(pk cipher.PubKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[pk]
	if !ok {
		return errPeerNotFound
	}

	p.RetryTimes++
	s.peers[pk] = p

	return nil
}

func (s *Swarm) resetPeerRetryTimes(pk cipher.PubKey) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	p, ok := s.peers[pk]
	if !ok {
		return errPeerNotFound
	}

	p.RetryTimes--
	s.peers[pk] = p

	return nil
}

func (s *Swarm) peersForExchange(peerPK cipher.PubKey) []msg.PeerInfo {
	var (
		zeroRetries = func(p Peer) bool {
			return p.RetryTimes == 0
		}
		notItself = func(p Peer) bool {
			return p.PubKey != peerPK
		}

		peers = s.randomPeers(int(s.cfg.PeersPerResponse), zeroRetries, notItself) //nolint:gosec

		peerMsg = make([]msg.PeerInfo, len(peers))
	)

	for i, p := range peers {
		peerMsg[i] = peerToMsg(p)
	}

	return peerMsg
}

func (s *Swarm) randomPeers(count int, filters ...peerFilter) []Peer {
	filteredPeers := s.filterPeers(filters...)

	if count > len(filteredPeers) {
		count = len(filteredPeers)
	}

	var (
		peerIdx = mathrand.Perm(len(filteredPeers))[:count]
		peers   = make([]Peer, len(peerIdx))
	)
	for i, idx := range peerIdx {
		peers[i] = filteredPeers[idx]
	}

	return peers
}

func (s *Swarm) filterPeers(filters ...peerFilter) []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var peers []Peer

PeerLoop:
	for _, p := range s.peers {
		for _, f := range filters {
			if !f(p) {
				continue PeerLoop
			}
		}

		peers = append(peers, p)
	}

	return peers
}

func (s *Swarm) validatePeer(pk cipher.PubKey, tcp, udp string) error {
	// Public key.
	if (pk == cipher.PubKey{}) {
		return errors.New("invalid public key")
	}
	if pk == s.node.ID() {
		return errors.New("pubkey can not be same as of node")
	}

	// TCP address.
	if tcp != "" {
		if tcp == s.node.TCP().Address() {
			return errors.New("tcp address can not be same as of node")
		}
		if err := validateAddress(tcp); err != nil {
			return fmt.Errorf("invlaid tcp address: %s", err)
		}
	}

	// UDP address
	if udp != "" {
		if udp == s.node.UDP().Address() {
			return errors.New("udp address can not be same as of node")
		}
		if err := validateAddress(udp); err != nil {
			return fmt.Errorf("invalid udp address: %s", err)
		}
	}

	return nil
}

func validateAddress(addr string) error {
	var (
		whitespaceFilter = regexp.MustCompile(`\s`)
		ipPort           = whitespaceFilter.ReplaceAllString(addr, "")
		split            = strings.Split(ipPort, ":")
	)
	if len(split) != 2 {
		return errors.New("invalid format")
	}
	if ip := net.ParseIP(split[0]); ip == nil {
		return errors.New("invalid ip")
	}
	if _, err := strconv.ParseUint(split[1], 10, 16); err != nil {
		return errors.New("invlaid port")
	}

	return nil
}
