// Package appnet pkg/app/appnet/forwarding.go
package appnet

import (
	"errors"
	"fmt"
	"io"
	"net"
	"sync"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/logging"
)

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
