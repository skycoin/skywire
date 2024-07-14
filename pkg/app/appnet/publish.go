// Package appnet pkg/app/appnet/publish.go
package appnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// PublishInfo represents the information of a published listener
type PublishInfo struct {
	ID        uuid.UUID `json:"id"`
	SkyAddr   Addr      `json:"sky_addr"`
	LocalPort int       `json:"local_port"`
	AppType   AppType   `json:"app_type"`
}

// PublishLis represents a listner that is published on the skywire network
type PublishLis struct {
	PublishInfo
	skyLis    net.Listener
	closeOnce sync.Once
	srv       *http.Server
	conn      net.Conn
	log       *logging.Logger
	nm        *NetManager
}

// NewPublishListener creates a new publishListener
func NewPublishListener(log *logging.Logger, nm *NetManager, skyLis net.Listener, localPort int, skyAddr Addr, appType AppType) (*PublishLis, error) {

	var srv *http.Server
	var conn net.Conn
	switch appType {
	case HTTP:
		srv = newHTTPPublishServer(localPort)
	case TCP:
		// conn = newTCPPublishConn(log, localPort)
		return nil, errors.New("app type TCP is not supported yet")
	case UDP:
		return nil, errors.New("app type UDP is not supported yet")
	}

	pubLis := &PublishLis{
		PublishInfo: PublishInfo{
			ID:        uuid.New(),
			SkyAddr:   skyAddr,
			LocalPort: localPort,
			AppType:   appType,
		},
		skyLis: skyLis,
		srv:    srv,
		conn:   conn,
		log:    log,
		nm:     nm,
	}

	if err := nm.AddPublish(pubLis); err != nil {
		return nil, err
	}

	return pubLis, nil
}

// Listen initializes the server based on AppType of the PublishLis.
func (f *PublishLis) Listen() error {
	switch f.AppType {
	case HTTP:
		go func() {
			err := f.srv.Serve(f.skyLis)
			if err != nil {
				// don't print error if local server is closed
				if !errors.Is(err, http.ErrServerClosed) {
					f.log.WithError(err).Error("error listening and serving app forwarding.")
				}
			}
		}()
	case TCP:
		// go func() {
		// 	for {
		// 		conn, err := f.skyLis.Accept()
		// 		if err != nil {
		// 			f.log.Errorf("error accepting connection: %v", err)
		// 			return
		// 		}

		// 		go f.handlePublishTCPConnection(conn)
		// 	}
		// }()
		return errors.New("app type TCP is not supported yet")
	case UDP:
		return errors.New("app type UDP is not supported yet")
	}

	f.log.Debugf("Serving local HTTP port: %v on SKY Addr %s", f.LocalPort, f.skyLis.Addr().String())
	return nil
}

// Close closes the server and publish listner.
func (f *PublishLis) Close() (err error) {
	f.closeOnce.Do(func() {
		switch f.AppType {
		case HTTP:
			err = f.srv.Close()
		case TCP:
			// err = f.conn.Close()
			return
		case UDP:
			return
		}
		err = f.skyLis.Close()
		f.nm.RemovePublishListener(f.ID)
	})
	return err
}

func newHTTPPublishServer(localPort int) *http.Server {
	r := gin.New()
	r.Use(gin.Recovery())
	r.Use(loggingMiddleware())
	authRoute := r.Group("/")
	authRoute.Any("/*path", func(c *gin.Context) {
		targetURL, _ := url.Parse(fmt.Sprintf("http://127.0.0.1:%v%s?%s", localPort, c.Request.URL.Path, c.Request.URL.RawQuery)) //nolint
		proxy := httputil.ReverseProxy{
			Director: func(req *http.Request) {
				req.URL = targetURL
				req.Host = targetURL.Host
				req.Method = c.Request.Method
			},
			Transport: &http.Transport{},
		}
		proxy.ServeHTTP(c.Writer, c.Request)
	})
	// #nosec G112 -- Ignoring potential Slowloris attacks as it the connection to close if the skynet connect is too slow to send the request
	srv := &http.Server{
		Handler: r,
		// todo(ersonp): Consider setting ReadHeaderTimeout to a reasonable value to address the Slowloris attack vector
	}

	return srv
}

func newTCPPublishConn(log *logging.Logger, localPort int) net.Conn {

	conn, err := net.Dial("tcp", fmt.Sprintf("127.0.0.1:%d", localPort))
	if err != nil {
		log.Printf("Error connecting to local port %d: %v", localPort, err)
		return nil
	}

	return conn
}

func (f *PublishLis) handlePublishTCPConnection(conn net.Conn) {
	defer conn.Close() //nolint

	copyConn := func(dst net.Conn, src net.Conn) {
		_, err := io.Copy(dst, src)
		if err != nil {
			f.log.Printf("Error during copy: %v", err)
		}
	}

	go copyConn(conn, f.conn)
	go copyConn(f.conn, conn)
}
