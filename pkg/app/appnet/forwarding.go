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

	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/logging"
)

// nolint: gochecknoglobals
var (
	forwardConns   = make(map[uuid.UUID]*ForwardConn)
	forwardConnsMu sync.Mutex
)

// AddForwarding adds ForwardConn to with it's ID
func AddForwarding(fwd *ForwardConn) {
	forwardConnsMu.Lock()
	defer forwardConnsMu.Unlock()
	forwardConns[fwd.ID] = fwd
}

// GetForwardConn get's a ForwardConn by ID
func GetForwardConn(id uuid.UUID) *ForwardConn {
	forwardConnsMu.Lock()
	defer forwardConnsMu.Unlock()

	return forwardConns[id]
}

// GetAllForwardConns gets all ForwardConns
func GetAllForwardConns() map[uuid.UUID]*ForwardConn {
	forwardConnsMu.Lock()
	defer forwardConnsMu.Unlock()

	return forwardConns
}

// RemoveForwardConn removes a ForwardConn by ID
func RemoveForwardConn(id uuid.UUID) {
	forwardConnsMu.Lock()
	defer forwardConnsMu.Unlock()
	delete(forwardConns, id)
}

// ForwardConn ...
type ForwardConn struct {
	ID         uuid.UUID
	LocalPort  int
	RemotePort int
	remoteConn net.Conn
	closeOnce  sync.Once
	srv        *http.Server
	closeChan  chan struct{}
	log        *logging.Logger
}

// NewForwardConn creates a new forwarding conn
func NewForwardConn(log *logging.Logger, remoteConn net.Conn, remotePort, localPort int) *ForwardConn {
	closeChan := make(chan struct{})
	var once sync.Once
	handler := http.NewServeMux()
	var lock sync.Mutex
	handler.HandleFunc("/", handleFunc(remoteConn, log, closeChan, &once, &lock))

	srv := &http.Server{
		Addr:           fmt.Sprintf(":%v", localPort),
		Handler:        handler,
		ReadTimeout:    10 * time.Second,
		WriteTimeout:   10 * time.Second,
		MaxHeaderBytes: 1 << 20,
	}
	fwdConn := &ForwardConn{
		ID:         uuid.New(),
		remoteConn: remoteConn,
		srv:        srv,
		LocalPort:  localPort,
		RemotePort: remotePort,
		closeChan:  closeChan,
		log:        log,
	}
	AddForwarding(fwdConn)
	return fwdConn
}

// Serve serves a HTTP forward conn that accepts all requests and forwards them directly to the remote server over the specified net.Conn.
func (f *ForwardConn) Serve() {
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
func (f *ForwardConn) Close() (err error) {
	f.closeOnce.Do(func() {
		err = f.srv.Close()
		err = f.remoteConn.Close()
		RemoveForwardConn(f.ID)
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

func handleFunc(remoteConn net.Conn, log *logging.Logger, closeChan chan struct{}, once *sync.Once, lock *sync.Mutex) func(w http.ResponseWriter, req *http.Request) {
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

// nolint: gochecknoglobals
var (
	rawTCPForwardConns   = make(map[uuid.UUID]*RawTCPForwardConn)
	rawTCPForwardConnsMu sync.Mutex
)

// RawTCPForwardConn handles raw TCP port forwarding over skywire
type RawTCPForwardConn struct {
	ID         uuid.UUID
	LocalPort  int
	RemotePort int
	remoteConn net.Conn
	listener   net.Listener
	closeOnce  sync.Once
	closeChan  chan struct{}
	log        *logging.Logger
}

// AddRawTCPForwarding adds RawTCPForwardConn with its ID
func AddRawTCPForwarding(fwd *RawTCPForwardConn) {
	rawTCPForwardConnsMu.Lock()
	defer rawTCPForwardConnsMu.Unlock()
	rawTCPForwardConns[fwd.ID] = fwd
}

// GetRawTCPForwardConn gets a RawTCPForwardConn by ID
func GetRawTCPForwardConn(id uuid.UUID) *RawTCPForwardConn {
	rawTCPForwardConnsMu.Lock()
	defer rawTCPForwardConnsMu.Unlock()
	return rawTCPForwardConns[id]
}

// GetAllRawTCPForwardConns gets all RawTCPForwardConns
func GetAllRawTCPForwardConns() map[uuid.UUID]*RawTCPForwardConn {
	rawTCPForwardConnsMu.Lock()
	defer rawTCPForwardConnsMu.Unlock()
	return rawTCPForwardConns
}

// RemoveRawTCPForwardConn removes a RawTCPForwardConn by ID
func RemoveRawTCPForwardConn(id uuid.UUID) {
	rawTCPForwardConnsMu.Lock()
	defer rawTCPForwardConnsMu.Unlock()
	delete(rawTCPForwardConns, id)
}

// NewRawTCPForwardConn creates a new raw TCP forwarding connection
func NewRawTCPForwardConn(log *logging.Logger, remoteConn net.Conn, remotePort, localPort int) (*RawTCPForwardConn, error) {
	listener, err := net.Listen("tcp", fmt.Sprintf(":%v", localPort))
	if err != nil {
		return nil, fmt.Errorf("failed to listen on port %v: %w", localPort, err)
	}

	fwd := &RawTCPForwardConn{
		ID:         uuid.New(),
		LocalPort:  localPort,
		RemotePort: remotePort,
		remoteConn: remoteConn,
		listener:   listener,
		closeChan:  make(chan struct{}),
		log:        log,
	}
	AddRawTCPForwarding(fwd)
	return fwd, nil
}

// Serve starts accepting local connections and forwarding them to the remote skywire connection
func (f *RawTCPForwardConn) Serve() {
	f.log.Debugf("Raw TCP forwarding: listening on localhost:%v -> remote:%v", f.LocalPort, f.RemotePort)

	go func() {
		for {
			select {
			case <-f.closeChan:
				return
			default:
			}

			localConn, err := f.listener.Accept()
			if err != nil {
				if errors.Is(err, net.ErrClosed) {
					return
				}
				f.log.WithError(err).Warn("Failed to accept local connection, continuing")
				continue
			}

			f.log.Debugf("Accepted local connection from %s", localConn.RemoteAddr())

			// Start bidirectional copy
			go f.handleLocalConn(localConn)
		}
	}()
}

func (f *RawTCPForwardConn) handleLocalConn(localConn net.Conn) {
	done := make(chan struct{}, 2)

	// local -> remote
	go func() {
		_, err := io.Copy(f.remoteConn, localConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			f.log.WithError(err).Debug("local->remote copy ended")
		}
		done <- struct{}{}
	}()

	// remote -> local
	go func() {
		_, err := io.Copy(localConn, f.remoteConn)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, net.ErrClosed) {
			f.log.WithError(err).Debug("remote->local copy ended")
		}
		done <- struct{}{}
	}()

	// Wait for one direction to finish
	<-done

	// Close both connections
	if err := localConn.Close(); err != nil {
		f.log.WithError(err).Debug("Error closing local connection")
	}

	// Signal that we're done - the remote conn will be closed when the forward conn is closed
	f.log.Debug("Local connection forwarding completed")
}

// Close closes the listener and remote connection
func (f *RawTCPForwardConn) Close() (err error) {
	f.closeOnce.Do(func() {
		close(f.closeChan)
		if f.listener != nil {
			if e := f.listener.Close(); e != nil {
				err = e
			}
		}
		if e := f.remoteConn.Close(); e != nil {
			err = e
		}
		RemoveRawTCPForwardConn(f.ID)
	})
	return err
}
