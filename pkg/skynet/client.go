// Package skynet internal/skynet/client.go
package skynet

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/sirupsen/logrus"

	"github.com/skycoin/skywire/pkg/cipher"
)

// DialRemoteFunc returns a fresh authenticated net.Conn to the remote
// skynet server. The Client calls it per-accept so each local TCP
// connection gets its own independent remote tunnel — necessary
// because the skynet wire is a raw byte stream with no per-connection
// framing, so concurrent or even tightly-sequenced local conns
// sharing a single remoteConn would interleave bytes and corrupt
// payloads.
//
// The handshake (ready-signal read + port-request write + reply
// read) lives inside Client.Connect, which the caller invokes
// against the returned conn before forwarding. Implementations
// typically wrap the host visor's appCl.Dial to the
// SkyForwardingServerPort.
type DialRemoteFunc func() (net.Conn, error)

// Client connects to a remote skynet server and forwards traffic to a local port.
// Pre-2026-05-21 this was single-shot: one Connect + one Serve = one
// local accept + forward + exit. That tore down the cli skynet
// start app after every curl, leaked the local listener for ~minutes,
// and made the port-forward operationally useless for any
// multi-connection workload. The current shape accepts in a loop and
// dials a fresh remote per accept so sequential local clients each
// get a clean independent forward.
type Client struct {
	log         logrus.FieldLogger
	remotePK    cipher.PubKey
	remotePort  int
	localPort   int
	dialRemote  DialRemoteFunc
	localLis    net.Listener
	mu          sync.RWMutex
	closeCh     chan struct{}
	closeOnce   sync.Once
	activeConns sync.WaitGroup
}

// NewClient creates a new skynet client. dialRemote is called once
// per accepted local connection to produce a fresh remote conn.
func NewClient(log logrus.FieldLogger, remotePK cipher.PubKey, remotePort, localPort int, dialRemote DialRemoteFunc) *Client {
	return &Client{
		log:        log,
		remotePK:   remotePK,
		remotePort: remotePort,
		localPort:  localPort,
		dialRemote: dialRemote,
		closeCh:    make(chan struct{}),
	}
}

// Connect runs the skynet handshake against a freshly-dialed remote
// conn: read ready-signal, send port-request, read reply. Returns
// nil on success — the conn is then ready for raw byte forwarding.
// Errors close the conn and propagate; caller should drop the
// local conn that triggered the dial.
func (c *Client) Connect(remoteConn net.Conn) error {
	// Wait for ready signal from server to ensure noise handshake is complete
	readyBuf := make([]byte, 1)
	if _, err := remoteConn.Read(readyBuf); err != nil {
		return fmt.Errorf("failed to read ready signal: %w", err)
	}
	c.log.Debug("Received ready signal from server")

	// Send connection request
	msg := clientMsg{Port: c.remotePort}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	if _, err := remoteConn.Write(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	c.log.Debugf("Sent connection request for port %d", c.remotePort)

	// Read response
	buf := make([]byte, 32*1024)
	n, err := remoteConn.Read(buf)
	if err != nil {
		return fmt.Errorf("failed to read response: %w", err)
	}

	var reply serverReply
	if err := json.Unmarshal(buf[:n], &reply); err != nil {
		return fmt.Errorf("failed to parse response: %w", err)
	}

	if reply.Error != nil {
		return fmt.Errorf("server error: %s", *reply.Error)
	}

	c.log.Debug("Skynet handshake complete on fresh remote conn")
	return nil
}

// Serve binds the local listener and runs the accept loop. Each
// accepted local conn gets its own goroutine + fresh remote dial.
// Returns when the listener closes (Client.Close) or Accept errors
// non-recoverably.
func (c *Client) Serve() error {
	addr := fmt.Sprintf("127.0.0.1:%d", c.localPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	c.mu.Lock()
	c.localLis = lis
	c.mu.Unlock()

	c.log.Infof("Listening on %s, forwarding to remote port %d (accept loop)", addr, c.remotePort)

	for {
		conn, err := lis.Accept()
		if err != nil {
			select {
			case <-c.closeCh:
				return nil
			default:
				if errors.Is(err, net.ErrClosed) {
					return nil
				}
				c.log.WithError(err).Error("Accept failed")
				return err
			}
		}

		c.activeConns.Add(1)
		go func(localConn net.Conn) {
			defer c.activeConns.Done()
			c.handleLocalConn(localConn)
		}(conn)
	}
}

// handleLocalConn services one accepted local TCP connection: dial a
// fresh remote conn, do the skynet handshake, forward bytes both
// ways until either side closes, then tear down both ends.
//
// Per-conn remote dial is the right model for this transport — the
// skynet wire is raw bytes with no per-conn framing, so re-using a
// single remoteConn across multiple local conns would interleave
// their payloads.
func (c *Client) handleLocalConn(localConn net.Conn) {
	defer func() { _ = localConn.Close() }() //nolint:errcheck

	remoteConn, err := c.dialRemote()
	if err != nil {
		c.log.WithError(err).Warn("handleLocalConn: dial remote failed; dropping local conn")
		return
	}
	defer func() { _ = remoteConn.Close() }() //nolint:errcheck

	if err := c.Connect(remoteConn); err != nil {
		c.log.WithError(err).Warn("handleLocalConn: skynet handshake failed; dropping conn")
		return
	}

	c.forwardOnce(localConn, remoteConn)
}

// forwardOnce wires bytes both ways between one local conn and one
// remote conn. Half-closes the destination on the side whose Copy
// finishes first so buffered bytes drain to the peer before the
// full Close. Pattern mirrors pkg/visor/init_services.go's #2735
// fix: io.Copy returns when bytes are accepted by the send buffer,
// not when delivered, so an immediate Close on the OTHER conn drops
// the in-flight tail. Half-close lets the kernel send buffer drain
// before the conn fully closes.
func (c *Client) forwardOnce(localConn, remoteConn net.Conn) {
	type direction int
	const (
		dirLocalToRemote direction = iota
		dirRemoteToLocal
	)
	type doneEvent struct{ d direction }

	done := make(chan doneEvent, 2)

	// local -> remote
	go func() {
		_, err := io.Copy(remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Debug("local->remote copy ended")
		}
		done <- doneEvent{d: dirLocalToRemote}
	}()

	// remote -> local
	go func() {
		_, err := io.Copy(localConn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Debug("remote->local copy ended")
		}
		done <- doneEvent{d: dirRemoteToLocal}
	}()

	// First-done — half-close the OTHER side's write so its send
	// buffer drains to the peer before we tear down.
	var first doneEvent
	select {
	case first = <-done:
	case <-c.closeCh:
		return
	}

	switch first.d {
	case dirLocalToRemote:
		halfCloseWrite(c.log, remoteConn, "remote")
	case dirRemoteToLocal:
		halfCloseWrite(c.log, localConn, "local")
	}

	// Wait for the second direction to drain (bounded by the OS-level
	// keep-alive / TCP timeout — there's no per-forward drain ticker
	// here because in practice both copies terminate quickly once one
	// side half-closes).
	select {
	case <-done:
	case <-c.closeCh:
	}

	c.log.Debug("Raw TCP forwarding completed")
}

// halfCloseWrite attempts CloseWrite (TCP half-close) on c so
// buffered bytes drain to the peer before the conn is fully closed
// by the surrounding defer. Falls back to full Close when the
// underlying conn doesn't implement CloseWriter — preserves
// pre-#2735-style behavior in that path.
func halfCloseWrite(log logrus.FieldLogger, c net.Conn, side string) {
	type closeWriter interface{ CloseWrite() error }
	if cw, ok := c.(closeWriter); ok {
		if err := cw.CloseWrite(); err != nil {
			log.WithError(err).WithField("side", side).Debug("CloseWrite failed; falling back to full Close")
			_ = c.Close() //nolint:errcheck
		}
		return
	}
	_ = c.Close() //nolint:errcheck
}

// Close stops the client. Subsequent Accept calls return ErrClosed.
// Active per-conn forwards drain via the closeCh in forwardOnce.
func (c *Client) Close() error {
	var err error
	c.closeOnce.Do(func() {
		close(c.closeCh)

		c.mu.Lock()
		if c.localLis != nil {
			if cErr := c.localLis.Close(); cErr != nil {
				err = cErr
			}
		}
		c.mu.Unlock()

		c.activeConns.Wait()
	})
	return err
}

// RemotePK returns the remote public key
func (c *Client) RemotePK() cipher.PubKey {
	return c.remotePK
}

// RemotePort returns the remote port
func (c *Client) RemotePort() int {
	return c.remotePort
}

// LocalPort returns the local port
func (c *Client) LocalPort() int {
	return c.localPort
}
