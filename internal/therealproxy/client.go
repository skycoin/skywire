package therealproxy

import (
	"fmt"
	"io"
	"net"
	"time"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/hashicorp/yamux"

	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"
	"github.com/SkycoinProject/skywire-mainnet/internal/skyenv"
	"github.com/SkycoinProject/skywire-mainnet/pkg/app"
	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"
)

// Log is therealproxy package level logger, it can be replaced with a different one from outside the package
var Log = logging.MustGetLogger("therealproxy")

// Client implement multiplexing proxy client using yamux.
type Client struct {
	listener net.Listener
	app      *app.App
	addr     routing.Addr
	timeout  time.Duration
	session  *yamux.Session
}

// NewClient constructs a new Client.
func NewClient(lis net.Listener, app *app.App, addr routing.Addr, timeout time.Duration) (*Client, error) {
	c := &Client{
		listener: lis,
		app:      app,
		addr:     addr,
		timeout:  timeout,
	}
	if err := c.connect(); err != nil {
		return nil, err
	}

	return c, nil
}

func (c *Client) connect() error {
	r := netutil.NewRetrier(skyenv.SkyproxyReconnectInterval, skyenv.SkyproxyRetryTimes, skyenv.SkyproxyRetryFactor)

	var conn net.Conn
	err := r.Do(func() error {
		var err error
		conn, err = c.app.Dial(c.addr)
		return err
	})
	if err != nil {
		return fmt.Errorf("failed to dial to a server: %v", err)
	}

	// If connection fails, yamux client doesn't wait, fails early and reconnects.
	yamuxCfg := yamux.DefaultConfig()
	yamuxCfg.KeepAliveInterval = c.timeout
	yamuxCfg.ConnectionWriteTimeout = c.timeout

	session, err := yamux.Client(conn, yamuxCfg)
	if err != nil {
		return fmt.Errorf("failed to create client: %s", err)
	}

	c.session = session

	return nil
}

// Serve proxies incoming connection to a remote proxy server.
func (c *Client) Serve() error {
	for {
		conn, err := c.listener.Accept()
		if err != nil {
			return fmt.Errorf("accept: %s", err)
		}

		stream := c.createStream()
		c.handleStream(conn, stream)
	}
}

func (c *Client) createStream() net.Conn {
	for {
		stream, err := c.session.Open()
		if err == nil {
			return stream
		}

		Log.Warnf("Failed to open yamux session: %v", err)

		delay := skyenv.SkyproxyReconnectInterval
		Log.Warnf("Restarting in %v", delay)
		time.Sleep(delay)

		if err := c.connect(); err != nil {
			Log.Warnf("Failed to reconnect, trying again")
		}
	}
}

func (c *Client) handleStream(in, out net.Conn) {
	go func() {
		errCh := make(chan error, 2)
		go func() {
			_, err := io.Copy(out, in)
			errCh <- err
		}()

		go func() {
			_, err := io.Copy(in, out)
			errCh <- err
		}()

		for err := range errCh {
			if err := in.Close(); err != nil {
				Log.WithError(err).Warn("Failed to close connection")
			}
			if err := out.Close(); err != nil {
				Log.WithError(err).Warn("Failed to close stream")
			}

			if err != nil {
				Log.Error("Copy error:", err)
			}
		}
	}()
}

// ListenAndServe starts tcp listener on addr and proxies incoming
// connection to a remote proxy server.
// TODO: get rid of it
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
