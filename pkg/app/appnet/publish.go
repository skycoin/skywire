// Package appnet pkg/app/appnet/publish.go
package appnet

import (
	"errors"
	"fmt"
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

// PublishLis represents a publishion that is published on the skywire network
type PublishLis struct {
	ID        uuid.UUID
	LocalPort int
	lis       net.Listener
	closeOnce sync.Once
	srv       *http.Server
	log       *logging.Logger
	nm        *NetManager
}

type ginHandler struct {
	Router *gin.Engine
}

func (h *ginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Router.ServeHTTP(w, r)
}

// NewPublishListener creates a new publishListener
func NewPublishListener(log *logging.Logger, nm *NetManager, lis net.Listener, localPort int) (*PublishLis, error) {

	r1 := gin.New()
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())
	authRoute := r1.Group("/")
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
		Handler:           &ginHandler{Router: r1},
		ReadHeaderTimeout: 5 * time.Second,
		WriteTimeout:      10 * time.Second,
	}

	pubLis := &PublishLis{
		ID:        uuid.New(),
		srv:       srv,
		lis:       lis,
		LocalPort: localPort,
		log:       log,
		nm:        nm,
	}
	nm.AddPublish(pubLis)
	return pubLis, nil
}

// Serve serves a HTTP forward Lis that accepts all requests and forwards them directly to the remote server over the specified net.Lis.
func (f *PublishLis) Listen() {
	go func() {
		err := f.srv.Serve(f.lis)
		if err != nil {
			// don't print error if local server is closed
			if !errors.Is(err, http.ErrServerClosed) {
				f.log.WithError(err).Error("Error listening and serving app forwarding.")
			}
		}
	}()
	f.log.Debugf("Serving HTTP on dmsg port %v with DMSG listener %s", f.LocalPort, f.lis.Addr().String())
}

// Close closes the server and publish listner.
func (f *PublishLis) Close() (err error) {
	f.closeOnce.Do(func() {
		err = f.srv.Close()
		err = f.lis.Close()
		f.nm.RemovePublishListener(f.ID)
	})
	return err
}
