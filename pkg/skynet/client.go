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

// Client connects to a remote skynet server and forwards traffic to a local port
type Client struct {
	log         logrus.FieldLogger
	remotePK    cipher.PubKey
	remotePort  int
	localPort   int
	remoteConn  net.Conn
	localLis    net.Listener
	mu          sync.RWMutex
	closeCh     chan struct{}
	closeOnce   sync.Once
	activeConns sync.WaitGroup
}

// NewClient creates a new skynet client
func NewClient(log logrus.FieldLogger, remotePK cipher.PubKey, remotePort, localPort int) *Client {
	return &Client{
		log:        log,
		remotePK:   remotePK,
		remotePort: remotePort,
		localPort:  localPort,
		closeCh:    make(chan struct{}),
	}
}

// Connect establishes connection to remote skynet server
func (c *Client) Connect(remoteConn net.Conn) error {
	c.mu.Lock()
	c.remoteConn = remoteConn
	c.mu.Unlock()

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

	c.log.Info("Connected to remote skynet server")
	return nil
}

// Serve starts accepting local connections and forwarding to remote
func (c *Client) Serve() error {
	addr := fmt.Sprintf("127.0.0.1:%d", c.localPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	c.mu.Lock()
	c.localLis = lis
	c.mu.Unlock()

	c.log.Infof("Listening on %s, forwarding to remote port %d", addr, c.remotePort)

	return c.forwardRawTCP()
}

func (c *Client) forwardRawTCP() error {
	c.mu.RLock()
	remoteConn := c.remoteConn
	localLis := c.localLis
	c.mu.RUnlock()

	conn, err := localLis.Accept()
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }() //nolint:errcheck

	done := make(chan struct{}, 2)

	// local -> remote
	go func() {
		_, err := io.Copy(remoteConn, conn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Debug("local->remote copy ended")
		}
		done <- struct{}{}
	}()

	// remote -> local
	go func() {
		_, err := io.Copy(conn, remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			c.log.WithError(err).Debug("remote->local copy ended")
		}
		done <- struct{}{}
	}()

	select {
	case <-done:
	case <-c.closeCh:
	}

	c.log.Debug("Raw TCP forwarding completed")
	return nil
}

// Close stops the client
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
		if c.remoteConn != nil {
			if cErr := c.remoteConn.Close(); cErr != nil && err == nil {
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
