// Package appnet pkg/app/appnet/forwarding.go
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

// nolint: gochecknoglobals
var (
	publishListenertners = make(map[uuid.UUID]*publishListener)
	publishListenerMu    sync.Mutex
)

// AddPublish adds publishListener to with it's ID
func AddPublish(fwd *publishListener) {
	publishListenerMu.Lock()
	defer publishListenerMu.Unlock()
	publishListenertners[fwd.ID] = fwd
}

// GetpublishListenertner get's a publishListener by ID
func GetpublishListenertner(id uuid.UUID) *publishListener {
	publishListenerMu.Lock()
	defer publishListenerMu.Unlock()

	return publishListenertners[id]
}

// GetAllpublishListenertners gets all publishListeners
func GetAllpublishListenertners() map[uuid.UUID]*publishListener {
	publishListenerMu.Lock()
	defer publishListenerMu.Unlock()

	return publishListenertners
}

// RemovepublishListener removes a publishListener by ID
func RemovepublishListener(id uuid.UUID) {
	publishListenerMu.Lock()
	defer publishListenerMu.Unlock()
	delete(publishListenertners, id)
}

// publishListener represents a publishion that is published on the skywire network
type publishListener struct {
	ID        uuid.UUID
	LocalPort int
	lis       net.Listener
	closeOnce sync.Once
	srv       *http.Server
	closeChan chan struct{}
	log       *logging.Logger
}

type ginHandler struct {
	Router *gin.Engine
}

func (h *ginHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.Router.ServeHTTP(w, r)
}

// NewPublishListener creates a new publishListener
func NewPublishListener(log *logging.Logger, lis net.Listener, localPort int) *publishListener {
	closeChan := make(chan struct{})
	r1 := gin.New()
	r1.Use(gin.Recovery())
	r1.Use(loggingMiddleware())
	authRoute := r1.Group("/")
	authRoute.Any("/*path", func(c *gin.Context) {
		log.Error("Request received")
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

	pubLis := &publishListener{
		ID:        uuid.New(),
		srv:       srv,
		lis:       lis,
		LocalPort: localPort,
		closeChan: closeChan,
		log:       log,
	}
	AddPublish(pubLis)
	return pubLis
}

// Serve serves a HTTP forward Lis that accepts all requests and forwards them directly to the remote server over the specified net.Lis.
func (f *publishListener) Listen() {
	go func() {
		err := f.srv.Serve(f.lis)
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
	f.log.Debugf("Serving HTTP on dmsg port %v with DMSG listener %s", f.LocalPort, f.lis.Addr().String())
}

// Close closes the server and remote publishion.
func (f *publishListener) Close() (err error) {
	f.closeOnce.Do(func() {
		f.log.Error("Closing publishListener")
		err = f.srv.Close()
		err = f.lis.Close()
		RemovepublishListener(f.ID)
	})
	return err
}

func loggingMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		start := time.Now()
		c.Next()
		latency := time.Since(start)
		if latency > time.Minute {
			latency = latency.Truncate(time.Second)
		}
		statusCode := c.Writer.Status()
		method := c.Request.Method
		path := c.Request.URL.Path
		// Get the background color based on the status code
		statusCodeBackgroundColor := getBackgroundColor(statusCode)
		// Get the method color
		methodColor := getMethodColor(method)
		// Print the logging in a custom format which includes the publickeyfrom c.Request.RemoteAddr ex.:
		// [DMSGHTTP] 2023/05/18 - 19:43:15 | 200 |    10.80885ms |                 | 02b5ee5333aa6b7f5fc623b7d5f35f505cb7f974e98a70751cf41962f84c8c4637:49153 | GET      /node-info.json
		fmt.Printf("[DMSGWEB] %s |%s %3d %s| %13v | %15s | %72s |%s %-7s %s %s\n",
			time.Now().Format("2006/01/02 - 15:04:05"),
			statusCodeBackgroundColor,
			statusCode,
			resetColor(),
			latency,
			c.ClientIP(),
			c.Request.RemoteAddr,
			methodColor,
			method,
			resetColor(),
			path,
		)
	}
}

func getBackgroundColor(statusCode int) string {
	switch {
	case statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices:
		return green
	case statusCode >= http.StatusMultipleChoices && statusCode < http.StatusBadRequest:
		return white
	case statusCode >= http.StatusBadRequest && statusCode < http.StatusInternalServerError:
		return yellow
	default:
		return red
	}
}

func getMethodColor(method string) string {
	switch method {
	case http.MethodGet:
		return blue
	case http.MethodPost:
		return cyan
	case http.MethodPut:
		return yellow
	case http.MethodDelete:
		return red
	case http.MethodPatch:
		return green
	case http.MethodHead:
		return magenta
	case http.MethodOptions:
		return white
	default:
		return reset
	}
}

func resetColor() string {
	return reset
}

const (
	green   = "\033[97;42m"
	white   = "\033[90;47m"
	yellow  = "\033[90;43m"
	red     = "\033[97;41m"
	blue    = "\033[97;44m"
	magenta = "\033[97;45m"
	cyan    = "\033[97;46m"
	reset   = "\033[0m"
)
