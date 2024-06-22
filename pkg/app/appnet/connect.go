// Package appnet pkg/app/appnet/forwarding.go
package appnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// nolint: gochecknoglobals
var (
	connectConns   = make(map[uuid.UUID]*ConnectConn)
	connectConnsMu sync.Mutex
)

// AddConnect adds ConnectConn to with it's ID
func AddConnect(fwd *ConnectConn) {
	connectConnsMu.Lock()
	defer connectConnsMu.Unlock()
	connectConns[fwd.ID] = fwd
}

// GetConnectConn get's a ConnectConn by ID
func GetConnectConn(id uuid.UUID) *ConnectConn {
	connectConnsMu.Lock()
	defer connectConnsMu.Unlock()

	return connectConns[id]
}

// GetAllConnectConns gets all ConnectConns
func GetAllConnectConns() map[uuid.UUID]*ConnectConn {
	connectConnsMu.Lock()
	defer connectConnsMu.Unlock()

	return connectConns
}

// RemoveConnectConn removes a ConnectConn by ID
func RemoveConnectConn(id uuid.UUID) {
	connectConnsMu.Lock()
	defer connectConnsMu.Unlock()
	delete(connectConns, id)
}

// ConnectConn represents a connection that is published on the skywire network
type ConnectConn struct {
	ID         uuid.UUID
	WebPort    int
	RemotePort int
	remoteConn net.Conn
	r          *gin.Engine
	closeOnce  sync.Once
	closeChan  chan struct{}
	log        *logging.Logger
}

// NewConnectConn creates a new ConnectConn
func NewConnectConn(log *logging.Logger, remoteConn net.Conn, remotePK cipher.PubKey, remotePort, webPort int) *ConnectConn {

	httpC := &http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
	mu := new(sync.Mutex)

	r := gin.New()

	r.Use(gin.Recovery())

	r.Use(loggingMiddleware())

	r.Any("/*path", handleConnectFunc(httpC, remotePK, remotePort, mu))

	fwdConn := &ConnectConn{
		ID:         uuid.New(),
		remoteConn: remoteConn,
		WebPort:    webPort,
		RemotePort: remotePort,
		log:        log,
		r:          r,
	}

	AddConnect(fwdConn)
	return fwdConn
}

// Serve serves a HTTP forward conn that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
func (f *ConnectConn) Serve() {
	go func() {
		err := f.r.Run(":" + fmt.Sprintf("%v", f.WebPort)) //nolint
		if err != nil {
			// don't print error if local server is closed
			if !errors.Is(err, http.ErrServerClosed) {
				f.log.WithError(err).Error("Error listening and serving app forwarding.")
			}
		}
	}()
	go func() {
		<-f.closeChan
		err := f.Close()
		if err != nil {
			f.log.Error(err)
		}
	}()
	f.log.Debugf("Serving on localhost:%v", f.WebPort)
}

// Close closes the server and remote connection.
func (f *ConnectConn) Close() (err error) {
	f.closeOnce.Do(func() {
		err = f.remoteConn.Close()
		RemoveConnectConn(f.ID)
	})
	return err
}

func handleConnectFunc(httpC *http.Client, remotePK cipher.PubKey, remotePort int, mu *sync.Mutex) func(c *gin.Context) {
	return func(c *gin.Context) {
		mu.Lock()
		defer mu.Unlock()

		var urlStr string
		urlStr = fmt.Sprintf("sky://%s:%v%s", remotePK, remotePort, c.Param("path"))
		if c.Request.URL.RawQuery != "" {
			urlStr = fmt.Sprintf("%s?%s", urlStr, c.Request.URL.RawQuery)
		}

		fmt.Printf("Proxying request: %s %s\n", c.Request.Method, urlStr)
		req, err := http.NewRequest(c.Request.Method, urlStr, c.Request.Body)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to create HTTP request")
			return
		}

		for header, values := range c.Request.Header {
			for _, value := range values {
				req.Header.Add(header, value)
			}
		}

		resp, err := httpC.Do(req)
		if err != nil {
			c.String(http.StatusInternalServerError, "Failed to connect to HTTP server")
			fmt.Printf("Error: %v\n", err)
			return
		}
		defer resp.Body.Close() //nolint

		for header, values := range resp.Header {
			for _, value := range values {
				c.Writer.Header().Add(header, value)
			}
		}

		c.Status(resp.StatusCode)
		if _, err := io.Copy(c.Writer, resp.Body); err != nil {
			c.String(http.StatusInternalServerError, "Failed to copy response body")
			fmt.Printf("Error copying response body: %v\n", err)
		}
	}
}
