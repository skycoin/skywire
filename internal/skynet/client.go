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

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
)

// Client connects to a remote skynet server and forwards traffic to a local port
type Client struct {
	log          logrus.FieldLogger
	remotePK     cipher.PubKey
	remotePort   int
	localPort    int
	rawTCP       bool
	remoteConn   net.Conn
	localLis     net.Listener
	mu           sync.RWMutex
	closeCh      chan struct{}
	closeOnce    sync.Once
	activeConns  sync.WaitGroup
}

// NewClient creates a new skynet client
func NewClient(log logrus.FieldLogger, remotePK cipher.PubKey, remotePort, localPort int, rawTCP bool) *Client {
	return &Client{
		log:        log,
		remotePK:   remotePK,
		remotePort: remotePort,
		localPort:  localPort,
		rawTCP:     rawTCP,
		closeCh:    make(chan struct{}),
	}
}

// Connect establishes connection to remote skynet server
func (c *Client) Connect(remoteConn net.Conn) error {
	c.mu.Lock()
	c.remoteConn = remoteConn
	c.mu.Unlock()

	// Send connection request
	msg := clientMsg{
		Port:   c.remotePort,
		RawTCP: c.rawTCP,
	}

	data, err := json.Marshal(msg)
	if err != nil {
		return fmt.Errorf("failed to marshal request: %w", err)
	}

	if _, err := remoteConn.Write(data); err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}

	c.log.Debugf("Sent connection request for port %d (raw_tcp=%v)", c.remotePort, c.rawTCP)

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
	// Listen on local port
	addr := fmt.Sprintf("127.0.0.1:%d", c.localPort)
	lis, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("failed to listen on %s: %w", addr, err)
	}

	c.mu.Lock()
	c.localLis = lis
	c.mu.Unlock()

	c.log.Infof("Listening on %s, forwarding to remote port %d", addr, c.remotePort)

	if c.rawTCP {
		// For raw TCP, we forward bidirectionally on the existing connection
		return c.forwardRawTCP()
	}

	// For HTTP mode, accept local connections and forward requests
	for {
		select {
		case <-c.closeCh:
			return nil
		default:
		}

		conn, err := lis.Accept()
		if err != nil {
			select {
			case <-c.closeCh:
				return nil
			default:
				if !errors.Is(err, net.ErrClosed) {
					c.log.WithError(err).Error("Failed to accept connection")
				}
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

func (c *Client) forwardRawTCP() error {
	c.mu.RLock()
	remoteConn := c.remoteConn
	localLis := c.localLis
	c.mu.RUnlock()

	// Accept one local connection and forward
	conn, err := localLis.Accept()
	if err != nil {
		return err
	}
	defer conn.Close()

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

	// Wait for one direction to finish
	select {
	case <-done:
	case <-c.closeCh:
	}

	c.log.Debug("Raw TCP forwarding completed")
	return nil
}

func (c *Client) handleLocalConn(localConn net.Conn) {
	defer localConn.Close()

	c.mu.RLock()
	remoteConn := c.remoteConn
	c.mu.RUnlock()

	// Read from local, forward to remote
	buf := make([]byte, 32*1024)
	n, err := localConn.Read(buf)
	if err != nil {
		if !errors.Is(err, io.EOF) {
			c.log.WithError(err).Debug("Failed to read from local")
		}
		return
	}

	// Forward to remote
	if _, err := remoteConn.Write(buf[:n]); err != nil {
		c.log.WithError(err).Error("Failed to write to remote")
		return
	}

	// Read response from remote
	respBuf := make([]byte, 64*1024)
	total := 0
	for {
		remoteConn.SetReadDeadline(timeoutAfter(5)) //nolint:errcheck
		rn, err := remoteConn.Read(respBuf[total:])
		if err != nil {
			if !errors.Is(err, io.EOF) && !isTimeout(err) {
				c.log.WithError(err).Debug("Failed to read from remote")
			}
			break
		}
		total += rn
		if total >= len(respBuf) {
			break
		}
	}

	if total > 0 {
		if _, err := localConn.Write(respBuf[:total]); err != nil {
			c.log.WithError(err).Error("Failed to write response to local")
		}
	}
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

// RawTCP returns whether using raw TCP mode
func (c *Client) RawTCP() bool {
	return c.rawTCP
}
