// Package appnet pkg/app/appnet/http_transport.go
package appnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/skycoin/skywire-utilities/pkg/logging"
)

// nolint: gochecknoglobals
var (
	proxies   = make(map[uuid.UUID]*Proxy)
	proxiesMu sync.Mutex
)

// AddProxy adds Proxy to with it's ID
func AddProxy(proxy *Proxy) {
	proxiesMu.Lock()
	defer proxiesMu.Unlock()
	proxies[proxy.ID] = proxy
}

// GetProxy get's a proxy by ID
func GetProxy(id uuid.UUID) *Proxy {
	proxiesMu.Lock()
	defer proxiesMu.Unlock()

	return proxies[id]
}

// GetAllProxies gets all proxies
func GetAllProxies() map[uuid.UUID]*Proxy {
	proxiesMu.Lock()
	defer proxiesMu.Unlock()

	return proxies
}

// RemoveProxy removes a proxy by ID
func RemoveProxy(id uuid.UUID) {
	proxiesMu.Lock()
	defer proxiesMu.Unlock()
	delete(proxies, id)
}

// Proxy ...
type Proxy struct {
	ID         uuid.UUID `json:"id"`
	remoteConn net.Conn
	closeOnce  sync.Once
	srv        *http.Server
	LocalPort  int `json:"local_port"`
	RemotePort int `json:"remote_port"`
	closeChan  chan struct{}
	log        *logging.Logger
}

// NewProxy ...
func NewProxy(log *logging.Logger, remoteConn net.Conn, remotePort, localPort int) *Proxy {
	closeChan := make(chan struct{})
	handler := http.NewServeMux()
	handler.HandleFunc("/", handleFunc(remoteConn, log, closeChan))

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%v", localPort),
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	proxy := &Proxy{
		ID:         uuid.New(),
		remoteConn: remoteConn,
		srv:        srv,
		LocalPort:  localPort,
		RemotePort: remotePort,
		closeChan:  closeChan,
		log:        log,
	}
	AddProxy(proxy)
	return proxy
}

// Serve serves a HTTP forward proxy that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
func (p *Proxy) Serve() {
	go func() {
		err := p.srv.ListenAndServe()
		if err != nil {
			// don't print error if local server is closed
			if !errors.Is(err, http.ErrServerClosed) {
				p.log.WithError(err).Error("Error listening and serving app proxy.")
			}
		}
	}()
	go func() {
		<-p.closeChan
		err := p.Close()
		if err != nil {
			p.log.Error(err)
		}
	}()
	p.log.Debugf("Serving on localhost:%v", p.LocalPort)
}

// Close closes the server and remote connection.
func (p *Proxy) Close() (err error) {
	p.closeOnce.Do(func() {
		err = p.srv.Close()
		err = p.remoteConn.Close()
		RemoveProxy(p.ID)
	})
	return err
}

func handleFunc(remoteConn net.Conn, log *logging.Logger, closeChan chan struct{}) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		client := http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
		// Forward request to remote server
		resp, err := client.Transport.RoundTrip(r)
		if err != nil {
			http.Error(w, "Could not reach remote server", 500)
			close(closeChan)
			return
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Errorln("Failed to close proxy response body")
			}
		}()

		w.WriteHeader(resp.StatusCode)

		// Transfer response from remote server -> client
		if resp.ContentLength > 0 {
			if _, err := io.CopyN(w, resp.Body, resp.ContentLength); err != nil {
				log.Warn(err)
			}
		} else if resp.Close {
			// Copy until EOF or some other error occurs
			for {
				if _, err := io.Copy(w, resp.Body); err != nil {
					break
				}
			}
		}
	}
}
