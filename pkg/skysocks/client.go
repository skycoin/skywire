// Package skysocks pkg/skysocks/client.go c4-app-proxy
package skysocks

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	ipc "github.com/james-barrow/golang-ipc"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// Client implement multiplexing proxy client using yamux.
type Client struct {
	appCl    *app.Client
	session  *yamux.Session
	listener net.Listener
	once     sync.Once
	closeC   chan struct{}
}

// NewClient constructs a new Client.
func NewClient(conn net.Conn, appCl *app.Client) (*Client, error) {
	c := &Client{
		appCl:  appCl,
		closeC: make(chan struct{}),
	}

	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	session, err := yamux.Client(conn, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating client: yamux: %w", err)
	}

	c.session = session

	go c.sessionKeepAliveLoop()

	return c, nil
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		if c.appCl != nil {
			c.setAppError(err)
		}
		return fmt.Errorf("listen: %w", err)
	}

	if c.appCl != nil {
		c.appCl.Log().Infof("Listening skysocks client on %s", addr)
	}

	c.listener = l
	go func() {
		<-c.closeC
		l.Close() //nolint:errcheck,gosec
	}()

	for {
		conn, err := l.Accept()
		if err != nil {
			if c.appCl != nil {
				c.appCl.Log().Errorf("Error accepting: %v", err)
			}
			// Release the yamux session + its keepalive goroutine + the
			// underlying conn on Accept failure, mirroring the session.Open
			// error path below. Without this, a listener that fails
			// independently of an orderly Close (so closeC was never
			// signaled) leaked the whole session. c.close() is sync.Once-
			// guarded, so it's a no-op when shutdown already triggered this.
			c.close()
			return fmt.Errorf("accept: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Accepted skysocks client")
		}

		stream, err := c.session.Open()
		if err != nil {
			c.close()

			return fmt.Errorf("error opening yamux stream: %w", err)
		}

		if c.appCl != nil {
			c.appCl.Log().Debug("Opened session skysocks client")
		}

		go c.handleStream(conn, stream)
	}
}

// Liveness-probe tuning for sessionKeepAliveLoop. A route group can be
// torn down router-side (remote visor restart, all mux legs dropped)
// WITHOUT the underlying conn delivering EOF, so session.IsClosed() may
// never flip and ListenAndServe would block forever in Accept(). A timed
// yamux ping detects that. The interval/timeout are generous and we
// require consecutive failures so a merely-slow multihop route is not
// mistaken for a dead one (a false close just costs one reconnect cycle).
const (
	livenessProbeInterval = 15 * time.Second
	livenessProbeTimeout  = 10 * time.Second
	livenessFailThreshold = 2
)

func (c *Client) sessionKeepAliveLoop() {
	ticker := time.NewTicker(livenessProbeInterval)
	defer ticker.Stop()

	fails := 0
	for {
		select {
		case <-c.closeC:
			return
		case <-ticker.C:
			if c.session.IsClosed() {
				c.close()

				return
			}
			if c.sessionAlive(livenessProbeTimeout) {
				fails = 0

				continue
			}
			fails++
			if fails >= livenessFailThreshold {
				if c.appCl != nil {
					c.appCl.Log().Warnf("Liveness probe failed %dx; route group gone, closing for reconnect", fails)
				}
				c.close()

				return
			}
		}
	}
}

// sessionAlive reports whether a yamux ping round-trips within timeout.
// A silently torn-down rg conn never pongs; the timer catches that. The
// ping runs in a goroutine so a wedged ping cannot stall the loop —
// Close() tears down the session, which unblocks the pending ping.
func (c *Client) sessionAlive(timeout time.Duration) bool {
	done := make(chan error, 1)
	go func() {
		_, err := c.session.Ping()
		done <- err
	}()
	select {
	case err := <-done:
		return err == nil
	case <-time.After(timeout):
		return false
	}
}

func (c *Client) handleStream(conn, stream net.Conn) {
	const errorCount = 2

	errCh := make(chan error, errorCount)

	go func() {
		_, err := io.Copy(stream, conn)
		errCh <- err
	}()

	go func() {
		_, err := io.Copy(conn, stream)
		errCh <- err
	}()

	// Wait for both io.Copy goroutines to finish (exactly 2 sends).
	for i := 0; i < errorCount; i++ {
		if err := <-errCh; err != nil && c.appCl != nil {
			c.appCl.Log().Debugf("Copy error: %v", err)
		}
		// Close both sides after the first copy finishes to unblock the other.
		if i == 0 {
			conn.Close()   //nolint:errcheck,gosec
			stream.Close() //nolint:errcheck,gosec
		}
	}

	if c.session.IsClosed() {
		c.close()
	}
}

func (c *Client) close() {
	if c.appCl != nil {
		c.appCl.Log().Debug("Session failed, closing skysocks client")
	}
	if err := c.Close(); err != nil && c.appCl != nil {
		c.appCl.Log().Errorf("Error closing skysocks client: %v", err)
	}
}

// ListenIPC starts named-pipe based connection server for windows or unix socket for other OSes
func (c *Client) ListenIPC(client *ipc.Client) {
	if c.appCl == nil {
		return
	}
	listenIPC(client, skyenv.SkysocksClientName, c.appCl.Log(), func() {
		client.Close()
		if err := c.Close(); err != nil {
			c.appCl.Log().Errorf("Error closing skysocks-client: %v", err)
		}
	})
}

func (c *Client) setAppError(appErr error) {
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		c.appCl.Log().Errorf("Failed to set error %v: %v", appErr, err)
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		if c.appCl != nil {
			c.appCl.Log().Debug("Closing proxy client")
		}

		close(c.closeC)
		// Tear down the yamux session so reconnect builds a fresh one
		// and any in-flight liveness ping unblocks.
		if c.session != nil {
			err = c.session.Close()
		}
	})

	return err
}
