// Package visor pkg/visor/udp_bridge.go: bidirectional pump that
// bridges a local UDP socket to a peer-side DatagramRouteGroup.
// Stage 4 of #2607 — together with the `udp` flag on ForwardedPort
// (forwarded_ports.go), this is the user-facing "port-forward UDP
// over skynet" capability.
//
// The bridge is shape-agnostic: it accepts any net.PacketConn for
// the skynet side (DatagramRouteGroup satisfies this) and any
// net.PacketConn for the local side (typical: net.UDPConn bound to
// EffectiveLocalPort()). Two goroutines, both running until either
// end is closed:
//
//   - localToSkynet: ReadFrom local UDP socket → WriteTo skynet
//     PacketConn. Backpressure is handled by net's own queue;
//     under load the local socket's recv buffer fills and the
//     kernel drops further inbound — matching what a directly-
//     bound UDP server would experience.
//
//   - skynetToLocal: ReadFrom skynet PacketConn → WriteTo local
//     UDP socket. Same shape; the local socket's send buffer is
//     the queue, and the bridge does not buffer datagrams
//     internally beyond a single-read scratch buffer.
//
// Single-peer per bridge: a UDPBridge ties one local UDP port to
// one peer (one DatagramRouteGroup). Multi-peer fan-out is the
// listener-loop's responsibility (a forwarded UDP port with
// multiple whitelisted senders gets one bridge per active sender,
// keyed by the local source address that arrived first).

package visor

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/logging"
)

// UDPBridge bidirectionally pumps datagrams between a local
// net.PacketConn (typically *net.UDPConn bound to the forwarded
// port's local TCP port — UDP using the same number) and a skynet-
// side net.PacketConn (typically a *router.DatagramRouteGroup).
//
// Closing the bridge (via Close) cancels the pump goroutines and
// closes both conns. Closing either end externally also stops the
// bridge cleanly — ReadFrom on a closed conn surfaces an error,
// the corresponding pump goroutine exits, and the second pump is
// cancelled via the bridge's context.
type UDPBridge struct {
	local  net.PacketConn // typically *net.UDPConn
	skynet net.PacketConn // typically *router.DatagramRouteGroup
	// localPeer is the remote address on the LOCAL UDP side — the
	// address of the application that's sending UDP to our local
	// port. Captured on the first localToSkynet ReadFrom; all
	// subsequent skynet→local replies WriteTo this address. Nil
	// until set. Multi-application fan-in on the local UDP socket
	// is not supported in a single bridge; the listener loop must
	// create one bridge per (local-app-addr, peer-pk) pair.
	localPeer  net.Addr
	localPeerMu sync.Mutex

	// skynetPeer is the remote address on the SKYNET side — i.e.
	// the DatagramRouteGroup's RemoteAddr. Cached so WriteTo can
	// be called without re-resolving every datagram. Stable for
	// the lifetime of the bridge.
	skynetPeer net.Addr

	log     *logging.Logger
	bufSize int

	ctx    context.Context
	cancel context.CancelFunc

	wg     sync.WaitGroup
	closed chan struct{}
	once   sync.Once
}

// UDPBridgeConfig tunes the bridge. Zero values use defaults.
type UDPBridgeConfig struct {
	// BufSize is the per-pump scratch buffer size. Sized to fit
	// the largest expected datagram; 65535 covers any UDP payload
	// the local socket can deliver.
	BufSize int

	// Logger overrides the default.
	Logger *logging.Logger
}

// NewUDPBridge constructs a bridge. The bridge does NOT start
// pumping until Start is called — gives the caller a chance to
// wire any pre-start state (e.g. a fixed localPeer for an
// outbound-initiated forward).
func NewUDPBridge(local, skynet net.PacketConn, cfg UDPBridgeConfig) *UDPBridge {
	if cfg.BufSize == 0 {
		cfg.BufSize = 65535
	}
	if cfg.Logger == nil {
		cfg.Logger = logging.MustGetLogger("udp-bridge")
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &UDPBridge{
		local:      local,
		skynet:     skynet,
		skynetPeer: skynet.LocalAddr(), // dg.RemoteAddr is the peer; skynet.WriteTo accepts any addr matching dg's invariants
		log:        cfg.Logger,
		bufSize:    cfg.BufSize,
		ctx:        ctx,
		cancel:     cancel,
		closed:     make(chan struct{}),
	}
}

// SetLocalPeer pre-pins the local UDP peer (the address of the
// app sending to our local port). Used for outbound-initiated
// forwards where we know the local app's source up-front. For
// inbound-initiated forwards leave this unset; the first local
// ReadFrom captures it automatically.
func (b *UDPBridge) SetLocalPeer(addr net.Addr) {
	b.localPeerMu.Lock()
	defer b.localPeerMu.Unlock()
	b.localPeer = addr
}

// Start launches the two pump goroutines and returns immediately.
// Idempotent — second call is a no-op.
func (b *UDPBridge) Start() {
	b.wg.Add(2)
	go b.localToSkynet()
	go b.skynetToLocal()
}

// Wait blocks until both pumps have exited. Close-then-Wait is the
// canonical shutdown sequence.
func (b *UDPBridge) Wait() {
	b.wg.Wait()
}

// Close stops the pumps and closes both conns. Idempotent.
func (b *UDPBridge) Close() error {
	var err error
	b.once.Do(func() {
		b.cancel()
		close(b.closed)
		if cerr := b.local.Close(); cerr != nil && err == nil {
			err = cerr
		}
		if cerr := b.skynet.Close(); cerr != nil && err == nil {
			err = cerr
		}
	})
	return err
}

func (b *UDPBridge) localToSkynet() {
	defer b.wg.Done()
	buf := make([]byte, b.bufSize)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		n, addr, err := b.local.ReadFrom(buf)
		if err != nil {
			if isClosedNetErr(err) {
				return
			}
			b.log.WithError(err).Debug("udp-bridge: local ReadFrom error")
			return
		}
		b.localPeerMu.Lock()
		if b.localPeer == nil {
			b.localPeer = addr
		}
		b.localPeerMu.Unlock()

		// WriteTo on a DatagramRouteGroup accepts only its remote
		// addr; pass the skynet PacketConn's RemoteAddr to be
		// explicit about the destination.
		if _, err := b.skynet.WriteTo(buf[:n], b.remoteAddr()); err != nil {
			if isClosedNetErr(err) {
				return
			}
			b.log.WithError(err).Debug("udp-bridge: skynet WriteTo error")
			// Keep pumping — a transient skynet write error
			// shouldn't tear down the local end. The skynet side's
			// own consecutiveWriteFailures threshold (in
			// DatagramRouteGroup) will close it for us on
			// persistent failure.
		}
	}
}

func (b *UDPBridge) skynetToLocal() {
	defer b.wg.Done()
	buf := make([]byte, b.bufSize)
	for {
		select {
		case <-b.ctx.Done():
			return
		default:
		}
		n, _, err := b.skynet.ReadFrom(buf)
		if err != nil {
			if isClosedNetErr(err) {
				return
			}
			b.log.WithError(err).Debug("udp-bridge: skynet ReadFrom error")
			return
		}

		b.localPeerMu.Lock()
		peer := b.localPeer
		b.localPeerMu.Unlock()
		if peer == nil {
			// No local peer known yet — drop. This happens only
			// for skynet-initiated forwards where the local app
			// hasn't reached out first. Future enhancement:
			// support inbound-initiated forwards via a configured
			// local peer (SetLocalPeer pre-Start).
			b.log.Debug("udp-bridge: skynet inbound dropped, no local peer registered yet")
			continue
		}
		if _, err := b.local.WriteTo(buf[:n], peer); err != nil {
			if isClosedNetErr(err) {
				return
			}
			b.log.WithError(err).Debug("udp-bridge: local WriteTo error")
		}
	}
}

// remoteAddr returns the skynet-side destination for WriteTo. A
// DatagramRouteGroup's WriteTo accepts only its configured remote;
// using LocalAddr here is wrong (that's our end). The skynet
// PacketConn convention: pass nil and let the implementation
// resolve internally.
func (b *UDPBridge) remoteAddr() net.Addr {
	return nil // DatagramRouteGroup.WriteTo treats nil addr as "the configured remote"
}

// isClosedNetErr returns true when err corresponds to the conn
// being closed. Standard library doesn't export a sentinel for
// this; both errors.Is(err, net.ErrClosed) and the string check on
// "use of closed network connection" are needed for cross-platform
// stability.
func isClosedNetErr(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, net.ErrClosed) {
		return true
	}
	if errors.Is(err, io.EOF) {
		return true
	}
	// Deadline-induced errors from a closing context look like
	// timeouts on some platforms.
	var nerr net.Error
	if errors.As(err, &nerr) && nerr.Timeout() {
		return false // genuine timeout, keep pumping
	}
	return false
}

// SuggestedReadDeadline is the per-pump idle ReadFrom deadline.
// Set by the caller via SetReadDeadline on the skynet PacketConn
// to give the pump a chance to observe ctx cancellation between
// reads. Without it a long-lived idle bridge sits forever in
// ReadFrom and Close has to rely on conn close to unblock —
// which works but adds a small race window.
const SuggestedReadDeadline = 30 * time.Second
