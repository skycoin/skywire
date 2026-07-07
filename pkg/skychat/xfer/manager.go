// Package xfer pkg/skychat/xfer/manager.go c2-app-chat
package xfer

import (
	"context"
	"io"
	"net"
	"sync"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/logging"
)

// Direction labels a completed transfer for the OnDone hook.
type Direction string

const (
	// Incoming is a transfer this node received.
	Incoming Direction = "incoming"
	// Outgoing is a transfer this node sent.
	Outgoing Direction = "outgoing"
)

// Config configures a Manager.
type Config struct {
	// LocalPK is this node's public key (stamped into outgoing offers).
	LocalPK cipher.PubKey
	// Dial opens a transfer stream to a peer (dmsg or skynet, same port).
	Dial DialFunc
	// Port is the file-transfer port (skyenv.SkychatFilePort) — the port this
	// manager listens on and dials out to.
	Port uint16
	// Accept decides inbound transfers (auto-accept paired/group peers). Required
	// to receive; if nil, every inbound transfer is declined.
	Accept AcceptFunc
	// OnDone, if set, is called after each transfer completes (either direction)
	// with the final error (nil on success) — the app uses it to drop a message
	// into chat/group history and update progress.
	OnDone func(dir Direction, peer cipher.PubKey, offer Offer, err error)
	// Logger is optional.
	Logger *logging.Logger
}

// Manager accepts inbound file transfers on one port over every skywire network
// it's given a listener for (dmsg + skynet, same port — like voice), and dials
// out to send. It is safe for concurrent use.
type Manager struct {
	cfg Config
	log *logging.Logger

	mu      sync.Mutex
	serving []net.Listener
}

// NewManager builds a Manager. A nil Accept means inbound transfers are declined.
func NewManager(cfg Config) *Manager {
	log := cfg.Logger
	if log == nil {
		log = logging.MustGetLogger("skychat-xfer")
	}
	return &Manager{cfg: cfg, log: log}
}

// Serve accepts transfer streams on ALL provided listeners concurrently — pass
// the dmsg listener AND the skynet listener (both bound to cfg.Port). Returns
// when ctx is done or every listener has failed.
func (m *Manager) Serve(ctx context.Context, listeners ...net.Listener) {
	m.mu.Lock()
	m.serving = append(m.serving, listeners...)
	m.mu.Unlock()

	var wg sync.WaitGroup
	for _, lis := range listeners {
		if lis == nil {
			continue
		}
		wg.Add(1)
		go func(lis net.Listener) {
			defer wg.Done()
			m.acceptLoop(ctx, lis)
		}(lis)
	}
	go func() { <-ctx.Done(); m.closeListeners() }()
	wg.Wait()
}

// AddListener starts accepting on one more listener after Serve is already
// running (e.g. the skynet listener that comes up only once the skynet networker
// registers). It returns immediately; accepting runs until ctx is done.
func (m *Manager) AddListener(ctx context.Context, lis net.Listener) {
	if lis == nil {
		return
	}
	m.mu.Lock()
	m.serving = append(m.serving, lis)
	m.mu.Unlock()
	go m.acceptLoop(ctx, lis)
}

func (m *Manager) acceptLoop(ctx context.Context, lis net.Listener) {
	for {
		conn, err := lis.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return
			}
			m.log.WithError(err).Debug("xfer: accept failed")
			return
		}
		go m.handleInbound(ctx, conn)
	}
}

func (m *Manager) handleInbound(ctx context.Context, conn net.Conn) {
	defer func() { _ = conn.Close() }() //nolint:errcheck

	from := remotePK(conn)
	accept := m.cfg.Accept
	if accept == nil {
		// No accept policy → decline everything, but still read the offer so the
		// sender gets a clean Receipt rather than a reset.
		accept = func(cipher.PubKey, Offer) (io.WriteCloser, bool) { return nil, false }
	}
	offer, err := Receive(ctx, from, conn, accept)
	if err != nil && !IsDeclined(err) {
		m.log.WithError(err).WithField("from", from).Warn("xfer: inbound transfer failed")
	}
	if m.cfg.OnDone != nil && !IsDeclined(err) {
		m.cfg.OnDone(Incoming, from, offer, err)
	}
}

// SendFile dials peer and streams the file described by offer from r. It stamps
// offer.From with the local PK. The returned Receipt carries the receiver's
// verdict; a non-nil error is a transport/protocol failure before a verdict.
func (m *Manager) SendFile(ctx context.Context, peer cipher.PubKey, offer Offer, r io.Reader) (Receipt, error) {
	offer.From = m.cfg.LocalPK
	conn, err := m.cfg.Dial(ctx, peer, m.cfg.Port)
	if err != nil {
		return Receipt{}, err
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	rc, err := Send(ctx, conn, offer, r)
	if m.cfg.OnDone != nil {
		m.cfg.OnDone(Outgoing, peer, offer, err)
	}
	return rc, err
}

func (m *Manager) closeListeners() {
	m.mu.Lock()
	ls := m.serving
	m.serving = nil
	m.mu.Unlock()
	for _, l := range ls {
		if l != nil {
			_ = l.Close() //nolint:errcheck
		}
	}
}

// remoteAddrPK is implemented by the skywire stream conns (dmsg + skynet), whose
// RemoteAddr carries the peer's public key.
type remoteAddrPK interface {
	PK() cipher.PubKey
}

// remotePK best-effort extracts the peer PK from a skywire conn. Both the dmsg
// Stream addr and the appnet skynet addr expose it; if neither does (e.g. an
// in-memory test pipe) it returns the zero key and the AcceptFunc must not rely
// on it for those conns.
func remotePK(conn net.Conn) cipher.PubKey {
	if conn == nil {
		return cipher.PubKey{}
	}
	if a, ok := conn.RemoteAddr().(remoteAddrPK); ok {
		return a.PK()
	}
	return cipher.PubKey{}
}
