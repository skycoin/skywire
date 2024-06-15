// Package appnet pkg/app/appnet/forwarding.go
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
	LocalPort  int
	RemotePort int
	remoteConn net.Conn
	closeOnce  sync.Once
	srv        *http.Server
	closeChan  chan struct{}
	log        *logging.Logger
}

// NewConnectConn creates a new ConnectConn
func NewConnectConn(log *logging.Logger, remoteConn net.Conn, remotePort, localPort int) *ConnectConn {
	closeChan := make(chan struct{})
	var once sync.Once
	handler := http.NewServeMux()
	var lock sync.Mutex
	handler.HandleFunc("/", handleFunc(remoteConn, log, closeChan, once, &lock))

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%v", localPort),
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	fwdConn := &ConnectConn{
		ID:         uuid.New(),
		remoteConn: remoteConn,
		srv:        srv,
		LocalPort:  localPort,
		RemotePort: remotePort,
		closeChan:  closeChan,
		log:        log,
	}
	AddConnect(fwdConn)
	return fwdConn
}

// Serve serves a HTTP forward conn that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
func (f *ConnectConn) Serve() {
	go func() {
		err := f.srv.ListenAndServe()
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
	f.log.Debugf("Serving on localhost:%v", f.LocalPort)
}

// Close closes the server and remote connection.
func (f *ConnectConn) Close() (err error) {
	f.closeOnce.Do(func() {
		err = f.srv.Close()
		err = f.remoteConn.Close()
		RemoveConnectConn(f.ID)
	})
	return err
}

func isClosed(c chan struct{}) bool {
	select {
	case <-c:
		return true
	default:
		return false
	}
}

func handleFunc(remoteConn net.Conn, log *logging.Logger, closeChan chan struct{}, once sync.Once, lock *sync.Mutex) func(w http.ResponseWriter, req *http.Request) {
	return func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()

		if isClosed(closeChan) {
			return
		}
		client := http.Client{Transport: MakeHTTPTransport(remoteConn, log)}
		// Forward request to remote server
		resp, err := client.Transport.RoundTrip(r)
		if err != nil {
			http.Error(w, "Could not reach remote server", 500)
			log.WithError(err).Errorf("Could not reach remote server %v", resp)
			once.Do(func() {
				close(closeChan)
			})
			return
		}

		defer func() {
			if err := resp.Body.Close(); err != nil {
				log.WithError(err).Errorln("Failed to close forwarding response body")
			}
		}()
		for key, value := range resp.Header {
			for _, v := range value {
				w.Header().Set(key, v)
			}
		}
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
