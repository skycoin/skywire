package therealproxy

import (
	"fmt"
	"io"
	"net"

	"github.com/SkycoinProject/skycoin/src/util/logging"
	"github.com/SkycoinProject/yamux"
)

// Log is therealproxy package level logger, it can be replaced with a different one from outside the package
var Log = logging.MustGetLogger("therealproxy")

// Client implement multiplexing proxy client using yamux.
type Client struct {
	session  *yamux.Session
	listener net.Listener
}

// NewClient constructs a new Client.
func NewClient(conn net.Conn) (*Client, error) {
	session, err := yamux.Client(conn, nil)
	if err != nil {
		return nil, fmt.Errorf("error creating client: yamux: %s", err)
	}

	return &Client{session: session}, nil
}

// ListenAndServe start tcp listener on addr and proxies incoming
// connection to a remote proxy server.
func (c *Client) ListenAndServe(addr string) error {
	var err error

	l, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen: %s", err)
	}

	Log.Printf("Listening therealproxy client on %s", addr)

	c.listener = l
	for {
		conn, err := l.Accept()
		if err != nil {
			Log.Printf("Error accepting: %v\n", err)
			return fmt.Errorf("accept: %s", err)
		}

		Log.Println("Accepted therealproxy client")
		stream, err := c.session.Open()
		if err != nil {
			return fmt.Errorf("error on `ListenAndServe`: yamux: %s", err)
		}

		Log.Println("Opened session therealproxy client")

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
		}()
	}
}

// Close implement io.Closer.
func (c *Client) Close() error {
	Log.Infoln("Closing proxy client")
	if c == nil {
		return nil
	}
	return c.listener.Close()
}
