// Package skysocks client.go
package skysocks

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	ipc "github.com/james-barrow/golang-ipc"
	"github.com/skycoin/yamux"

	"github.com/skycoin/skywire/pkg/app"
	"github.com/skycoin/skywire/pkg/app/appserver"
	"github.com/skycoin/skywire/pkg/router"
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

	fmt.Printf("Listening skysocks client on %s", addr)

	c.listener = l
	if c.appCl != nil {
		c.setAppStatus(appserver.AppDetailedStatusRunning)
	}

	for {
		select {
		case <-c.closeC:
			return nil
		default:
		}

		conn, err := l.Accept()
		if err != nil {
			fmt.Printf("Error accepting: %v\n", err)
			return fmt.Errorf("accept: %w", err)
		}

		fmt.Println("Accepted skysocks client")

		stream, err := c.session.Open()
		if err != nil {
			c.close()

			return fmt.Errorf("error opening yamux stream: %w", err)
		}

		fmt.Println("Opened session skysocks client")

		go c.handleStream(conn, stream)
	}
}

func (c *Client) sessionKeepAliveLoop() {
	ticker := time.NewTicker(router.DefaultRouteKeepAlive / 2)
	defer ticker.Stop()

	for {
		select {
		case <-c.closeC:
			return
		case <-ticker.C:
			if c.session.IsClosed() {
				c.close()

				return
			}
		}
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

	var connClosed, streamClosed bool

	for err := range errCh {
		if !connClosed {
			if err := conn.Close(); err != nil {
				fmt.Printf("Failed to close connection: %v\n", err)
			}

			connClosed = true
		}

		if !streamClosed {
			if err := stream.Close(); err != nil {
				fmt.Printf("Failed to close stream: %v\n", err)
			}

			streamClosed = true
		}

		if err != nil {
			print(fmt.Sprintf("Copy error: %v\n", err))
		}
	}

	close(errCh)

	if c.session.IsClosed() {
		c.close()
	}
}

func (c *Client) close() {
	print("Session failed, closing skysocks client")
	if err := c.Close(); err != nil {
		print(fmt.Sprintf("Error closing skysocks client: %v\n", err))
	}
}

// ListenIPC starts named-pipe based connection server for windows or unix socket for other OSes
func (c *Client) ListenIPC(client *ipc.Client) {
	listenIPC(client, skyenv.SkychatName+"-client", func() {
		client.Close()
		if err := c.Close(); err != nil {
			print(fmt.Sprintf("Error closing skysocks-client: %v\n", err))
		}
	})
}

func (c *Client) setAppStatus(status appserver.AppDetailedStatus) {
	if err := c.appCl.SetDetailedStatus(string(status)); err != nil {
		print(fmt.Sprintf("Failed to set status %v: %v\n", status, err))
	}
}

func (c *Client) setAppError(appErr error) {
	if err := c.appCl.SetError(appErr.Error()); err != nil {
		print(fmt.Sprintf("Failed to set error %v: %v\n", appErr, err))
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		fmt.Println("Closing proxy client")

		close(c.closeC)

		err = c.listener.Close()
	})

	return err
}
