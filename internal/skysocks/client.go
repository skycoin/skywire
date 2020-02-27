package skysocks

import (
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/SkycoinProject/skywire-mainnet/pkg/router"

	"github.com/SkycoinProject/skywire-mainnet/pkg/routing"

	"github.com/SkycoinProject/dmsg/cipher"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app"

	"github.com/SkycoinProject/skywire-mainnet/pkg/app/appnet"

	"github.com/SkycoinProject/skywire-mainnet/internal/netutil"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/yamux"
)

// Log is skysocks package level logger, it can be replaced with a different one from outside the package
var Log = logging.MustGetLogger("skysocks") // nolint: gochecknoglobals

// Client implement multiplexing proxy client using yamux.
type Client struct {
	sessionMx      sync.RWMutex
	session        *yamux.Session
	listener       net.Listener
	redialer       *netutil.Retrier
	app            *app.Client
	serverPK       cipher.PubKey
	appNetType     appnet.Type
	serverPort     routing.Port
	sessionFailedC chan struct{}
	closeC         chan struct{}
}

// NewClient constructs a new Client.
func NewClient(app *app.Client, serverPK cipher.PubKey, appNetType appnet.Type,
	serverPort routing.Port) (*Client, error) {
	c := &Client{
		redialer:       netutil.NewRetrier(time.Second, 0, 1),
		app:            app,
		serverPK:       serverPK,
		appNetType:     appNetType,
		serverPort:     serverPort,
		sessionFailedC: make(chan struct{}),
		closeC:         make(chan struct{}),
	}

	conn, err := c.dialServer()

	sessionCfg := yamux.DefaultConfig()
	sessionCfg.EnableKeepAlive = false
	session, err := yamux.Client(conn, sessionCfg)
	if err != nil {
		return nil, fmt.Errorf("error creating client: yamux: %s", err)
	}

	c.session = session

	go c.redialLoop()
	go c.sessionKeepAliveLoop()

	return c, nil
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %s", err)
	}

	Log.Printf("Listening skysocks client on %s", addr)

	c.listener = l

	for {
		conn, err := l.Accept()
		if err != nil {
			Log.Printf("Error accepting: %v\n", err)
			return fmt.Errorf("accept: %s", err)
		}

		Log.Println("Accepted skysocks client")

		c.sessionMx.RLock()
		stream, err := c.session.Open()
		if err != nil {
			Log.Errorf("Session failed: %v, redialing...", err)

			select {
			case <-c.closeC:
				return nil
			case c.sessionFailedC <- struct{}{}:
			default:
			}
		}
		c.sessionMx.RUnlock()

		Log.Println("Opened session skysocks client")

		go c.handleStream(conn, stream)
	}
}

func (c *Client) sessionKeepAliveLoop() {
	for {
		select {
		case <-c.closeC:
			return
		default:
		}

		c.sessionMx.RLock()
		if c.session.IsClosed() {
			Log.Errorln("Session failed, redialing...")

			select {
			case c.sessionFailedC <- struct{}{}:
			default:
			}
		}
		c.sessionMx.RUnlock()

		time.Sleep(router.DefaultRouteKeepAlive / 2)
	}
}

func (c *Client) redialLoop() {
	for {
		select {
		case <-c.closeC:
			return
		case <-c.sessionFailedC:
			c.sessionMx.Lock()

			conn, err := c.dialServer()
			if err != nil {
				c.sessionMx.Unlock()
				Log.Fatal("Failed to dial to a server: ", err)
			}

			sessionCfg := yamux.DefaultConfig()
			sessionCfg.EnableKeepAlive = false
			session, err := yamux.Client(conn, sessionCfg)
			if err != nil {
				c.sessionMx.Unlock()
				Log.Fatalf("error creating client: yamux: %s", err)
			}

			c.session = session
			c.sessionMx.Unlock()
		}
	}
}

func (c *Client) dialServer() (net.Conn, error) {
	var conn net.Conn
	err := c.redialer.Do(func() error {
		var err error
		conn, err = c.app.Dial(appnet.Addr{
			Net:    c.appNetType,
			PubKey: c.serverPK,
			Port:   c.serverPort,
		})
		return err
	})
	if err != nil {
		return nil, err
	}

	return conn, nil
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
		Log.WithError(err).Error("GOT ERROR FROM STREAM OF APP CONN")
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

	c.sessionMx.RLock()
	if c.session.IsClosed() {
		Log.Errorln("Session failed, redialing...")

		select {
		case c.sessionFailedC <- struct{}{}:
		default:
		}
	}
	c.sessionMx.RUnlock()
}

// Close implement io.Closer.
func (c *Client) Close() error {
	Log.Infoln("Closing proxy client")

	if c == nil {
		return nil
	}

	close(c.closeC)

	return c.listener.Close()
}
