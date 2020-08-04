package skysocks

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/sirupsen/logrus"
	"github.com/skycoin/skycoin/src/util/logging"
	"github.com/skycoin/yamux"

	"github.com/skycoin/skywire/pkg/router"
)

// Log is skysocks package level logger, it can be replaced with a different one from outside the package
var Log logrus.FieldLogger = logging.MustGetLogger("skysocks") // nolint: gochecknoglobals

// Client implement multiplexing proxy client using yamux.
type Client struct {
	session  *yamux.Session
	listener net.Listener
	once     sync.Once
	closeC   chan struct{}
}

// NewClient constructs a new Client.
func NewClient(conn net.Conn) (*Client, error) {
	c := &Client{
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
		return fmt.Errorf("listen: %w", err)
	}

	Log.Printf("Listening skysocks client on %s", addr)

	c.listener = l

	for {
		select {
		case <-c.closeC:
			return nil
		default:
		}

		conn, err := l.Accept()
		if err != nil {
			Log.Printf("Error accepting: %v\n", err)
			return fmt.Errorf("accept: %w", err)
		}

		Log.Println("Accepted skysocks client")

		stream, err := c.session.Open()
		if err != nil {
			c.close()

			return fmt.Errorf("error opening yamux stream: %w", err)
		}

		Log.Println("Opened session skysocks client")

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
				Log.WithError(err).Warn("Failed to close connection")
			}

			connClosed = true
		}

		if !streamClosed {
			if err := stream.Close(); err != nil {
				Log.WithError(err).Warn("Failed to close stream")
			}

			streamClosed = true
		}

		if err != nil {
			Log.Error("Copy error:", err)
		}
	}

	close(errCh)

	if c.session.IsClosed() {
		c.close()
	}
}

func (c *Client) close() {
	Log.Error("Session failed, closing skysocks client")
	if err := c.Close(); err != nil {
		Log.WithError(err).Error("Error closing skysocks client")
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		Log.Infoln("Closing proxy client")

		close(c.closeC)

		err = c.listener.Close()
	})

	return err
}
