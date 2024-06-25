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
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

type PublishInfo struct {
	ID        uuid.UUID `json:"id"`
	LocalAddr Addr      `json:"local_addr"`
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
func NewPublishListener(log *logging.Logger, nm *NetManager, skyLis net.Listener, addr Addr, appType AppType) (*PublishLis, error) {

	var srv *http.Server
	var conn net.Conn
	switch appType {
	case HTTP:
		srv = newHTTPPublishServer(int(addr.GetPort()))
	case TCP:
		// conn = newTCPPublishConn(log, webPort)
		return nil, errors.New("app type TCP is not supported yet")
	case UDP:
		return nil, errors.New("app type UDP is not supported yet")
	}

	pubLis := &PublishLis{
		PublishInfo: PublishInfo{
			ID:        uuid.New(),
			LocalAddr: addr,
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

// Serve serves a HTTP forward Lis that accepts all requests and forwards them directly to the remote server over the specified net.Lis.
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

	f.log.Debugf("Serving HTTP on sky port %v with SKY listener %s", f.LocalAddr.GetPort(), f.skyLis.Addr().String())
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

	srv := &http.Server{
		Handler:           r,
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
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
