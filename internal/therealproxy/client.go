package therealproxy

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/hashicorp/yamux"

	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

// Log is therealproxy package level logger, it can be replaced with a different one from outside the package
var Log = logging.MustGetLogger("therealproxy")

// Client implement multiplexing proxy client using yamux.
type Client struct {
	session  *yamux.Session
	listener net.Listener
	app      *app.App
	addr     routing.Addr
}

// NewClient constructs a new Client.
func NewClient(lis net.Listener, app *app.App, addr routing.Addr) (*Client, error) {
	c := &Client{
		listener: lis,
		app:      app,
		addr:     addr,
	}
	if err := c.connect(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) connect() error {
	r := netutil.NewRetrier(time.Second, 0, 1)

	var conn net.Conn
	err := r.Do(func() error {
		var err error
		conn, err = c.app.Dial(c.addr)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to dial to a server: %v", err)
	}

	session, err := yamux.Client(conn, nil)
	if err != nil {
		return fmt.Errorf("failed to create client: %s", err)
	}

	c.session = session

	return nil
}

func (c *Client) Serve() error {
	var stream net.Conn

	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %s", err)
		}

		for {
			stream, err = c.session.Open()
			if err == nil {
				break
			}

			Log.Warnf("Failed to open yamux session: %v", err)

			delay := 1 * time.Second
			Log.Warnf("Restarting in %v", delay)
			time.Sleep(delay)

			if err := c.connect(); err != nil {
				Log.Warnf("Failed to reconnect, trying again")
			}
		}

		go func() {
			errCh := make(chan error, 2)
			go func() {
				_, err := io.Copy(stream, conn)
				errCh <- err
			}()

			go func() {
				_, err := io.Copy(conn, stream)
				errCh <- err
			}()

			for err := range errCh {
				if err := conn.Close(); err != nil {
					Log.WithError(err).Warn("Failed to close connection")
				}
				if err := stream.Close(); err != nil {
					Log.WithError(err).Warn("Failed to close stream")
				}

				if err != nil {
					Log.Error("Copy error:", err)
				}
			}
		}()
	}
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	var stream net.Conn
	var err error

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %s", err)
	}

	c.listener = l
	for {
		conn, err := l.Accept()
		if err != nil {
			return fmt.Errorf("accept: %s", err)
		}

		stream, err = c.session.Open()
		if err != nil {
			return fmt.Errorf("yamux: %s", err)
		}

		go func() {
			errCh := make(chan error, 2)
			go func() {
				_, err := io.Copy(stream, conn)
				errCh <- err
			}()

			go func() {
				_, err := io.Copy(conn, stream)
				errCh <- err
			}()

			for err := range errCh {
				if err := conn.Close(); err != nil {
					Log.WithError(err).Warn("Failed to close connection")
				}
				if err := stream.Close(); err != nil {
					Log.WithError(err).Warn("Failed to close stream")
				}

				if err != nil {
					Log.Error("Copy error:", err)
				}
			}
		}()
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	if c == nil {
		return nil
	}
	return c.listener.Close()
}
