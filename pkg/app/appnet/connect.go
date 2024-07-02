// Package appnet pkg/app/appnet/connect.go
package appnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire-utilities/pkg/logging"
	"github.com/skycoin/skywire/pkg/routing"
)

// ConnectInfo represents the information of a connected connection
type ConnectInfo struct {
	ID         uuid.UUID `json:"id"`
	WebPort    int       `json:"web_port"`
	RemoteAddr Addr      `json:"remote_addr"`
	AppType    AppType   `json:"app_type"`
}

// ConnectConn represents a connection that is connected to a published app
type ConnectConn struct {
	ConnectInfo
	skyConn   net.Conn
	srv       *http.Server
	lis       net.Listener
	closeOnce sync.Once
	log       *logging.Logger
	nm        *NetManager
}

// NewConnectConn creates a new ConnectConn
func NewConnectConn(log *logging.Logger, nm *NetManager, remoteConn net.Conn, remoteAddr Addr, webPort int, appType AppType) (*ConnectConn, error) {
	var srv *http.Server
	var lis net.Listener

	switch appType {
	case HTTP:
		srv = newHTTPConnectServer(log, remoteConn, remoteAddr, webPort)
	case TCP:
		// lis = newTCPConnectListner(log, webPort)
		return nil, errors.New("app type TCP is not supported yet")
	case UDP:
		return nil, errors.New("app type UDP is not supported yet")
	}

	conn := &ConnectConn{
		ConnectInfo: ConnectInfo{
			ID:         uuid.New(),
			WebPort:    webPort,
			RemoteAddr: remoteAddr,
			AppType:    appType,
		},
		skyConn: remoteConn,
		log:     log,
		srv:     srv,
		lis:     lis,
		nm:      nm,
	}

	if err := nm.AddConnect(conn); err != nil {
		return nil, err
	}

	return conn, nil
}

// Serve starts the server based on the AppType of the ConnectConn.
func (f *ConnectConn) Serve() error {
	switch f.AppType {
	case HTTP:
		go func() {
			err := f.srv.ListenAndServe() //nolint
			if err != nil {
				// don't print error if local server is closed
				if !errors.Is(err, http.ErrServerClosed) {
					f.log.WithError(err).Error("Error listening and serving app forwarding.")
				}
			}
		}()
	case TCP:
		// go func() {
		// 	handleConnectTCPConnection(f.lis, f.remoteConn, f.log)
		// }()
		return errors.New("app type TCP is not supported yet")
	case UDP:
		return errors.New("app type UDP is not supported yet")
	}
	f.log.Debugf("Serving on localhost:%v", f.WebPort)
	return nil
}

// Close closes the server, listener and remote connection.
func (f *ConnectConn) Close() (err error) {
	f.closeOnce.Do(func() {

		switch f.AppType {
		case HTTP:
			err = f.srv.Close()
		case TCP:
			// err = f.lis.Close()
			return
		case UDP:
			return
		}
		err = f.skyConn.Close()
		f.nm.RemoveConnectConn(f.ID)
	})
	return err
}

func handleConnectFunc(httpC *http.Client, remotePK cipher.PubKey, remotePort routing.Port, mu *sync.Mutex) func(c *gin.Context) {
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

func newHTTPConnectServer(log *logging.Logger, remoteConn net.Conn, remoteAddr Addr, webPort int) *http.Server {

	httpC := &http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
	mu := new(sync.Mutex)

	r := gin.New()

	r.Use(gin.Recovery())

	r.Use(loggingMiddleware())

	r.Any("/*path", handleConnectFunc(httpC, remoteAddr.PK(), remoteAddr.GetPort(), mu))

	srv := &http.Server{
		Addr:              fmt.Sprint(":", webPort),
		ReadHeaderTimeout: 5 * time.Second,
		Handler:           r,
	}
	return srv
}

func newTCPConnectListner(log *logging.Logger, webPort int) net.Listener {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%d", webPort))
	if err != nil {
		log.Errorf("Failed to start TCP listener on port %d: %v", webPort, err)
	}
	return listener
}

func handleConnectTCPConnection(listener net.Listener, remoteConn net.Conn, log *logging.Logger) {
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("Failed to accept connection: %v", err)
			continue
		}

		go func(conn net.Conn) {
			defer conn.Close() //nolint

			go func() {
				_, err := io.Copy(remoteConn, conn)
				if err != nil {
					log.Printf("Error copying data to dmsg server: %v", err)
				}
				remoteConn.Close() //nolint
			}()

			go func() {
				_, err := io.Copy(conn, remoteConn)
				if err != nil {
					log.Printf("Error copying data from dmsg server: %v", err)
				}
				conn.Close() //nolint
			}()
		}(conn)
	}
}
