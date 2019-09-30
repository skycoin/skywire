package dmsg

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"

	"github.com/SkycoinProject/skycoin/src/util/logging"

	"github.com/SkycoinProject/dmsg/cipher"
)

// Errors related to REQUEST frames.
var (
	ErrRequestRejected    = errors.New("failed to create transport: request rejected")
	ErrRequestCheckFailed = errors.New("failed to create transport: request check failed")
	ErrAcceptCheckFailed  = errors.New("failed to create transport: accept check failed")
	ErrPortNotListening   = errors.New("failed to create transport: port not listening")
)

// Transport represents communication between two nodes via a single hop:
// a connection from dmsg.Client to remote dmsg.Client (via dmsg.Server intermediary).
type Transport struct {
	net.Conn // underlying connection to dmsg.Server
	log      *logging.Logger

	id    uint16 // tp ID that identifies this dmsg.transport
	lAddr Addr   // local address
	rAddr Addr   // remote address

	inCh chan Frame // handles incoming frames (from dmsg.Client)
	inMx sync.Mutex // protects 'inCh'

	lW *LocalWindow  // local window
	rW *RemoteWindow // remote window

	serving     chan struct{} // chan which closes when serving begins
	servingOnce sync.Once     // ensures 'serving' only closes once
	done        chan struct{} // chan which closes when transport stops serving
	doneOnce    sync.Once     // ensures 'done' only closes once
	doneFunc    func()        // contains a method that triggers when dmsg.Client closes
}

// NewTransport creates a new dms_tp.
func NewTransport(conn net.Conn, log *logging.Logger, local, remote Addr, id uint16, lWindow int, doneFunc func()) *Transport {
	tp := &Transport{
		Conn:     conn,
		log:      log,
		id:       id,
		lAddr:    local,
		rAddr:    remote,
		inCh:     make(chan Frame),
		lW:       NewLocalWindow(lWindow),
		serving:  make(chan struct{}),
		done:     make(chan struct{}),
		doneFunc: doneFunc,
	}
	return tp
}

func (tp *Transport) serve() (started bool) {
	tp.servingOnce.Do(func() {
		started = true
		close(tp.serving)
	})
	return started
}

// Regarding the use of mutexes:
// 1. `done` is always closed before `inCh`/`lCh` is closed.
// 2. mutexes protect `inCh`/`lCh` to ensure that closing and writing to these chans does not happen concurrently.
// 3. Our worry now, is writing to `inCh`/`lCh` AFTER they have been closed.
// 4. But as, under the mutexes protecting `inCh`/`lCh`, checking `done` comes first,
// and we know that `done` is closed before `inCh`/`lCh`, we can guarantee that it avoids writing to closed chan.
func (tp *Transport) close() (closed bool) {
	if tp == nil {
		return false
	}

	tp.doneOnce.Do(func() {
		closed = true

		close(tp.done)
		tp.doneFunc()

		_ = tp.rW.Close() //nolint:errcheck
		_ = tp.lW.Close() //nolint:errcheck

		tp.inMx.Lock()
		close(tp.inCh)
		tp.inMx.Unlock()
	})

	tp.serve() // just in case.
	return closed
}

// Close closes the dmsg_tp.
func (tp *Transport) Close() error {
	if tp.close() {
		if err := writeCloseFrame(tp.Conn, tp.id, PlaceholderReason); err != nil {
			log.WithError(err).Warn("Failed to write frame")
		}
	}
	return nil
}

// IsClosed returns whether dms_tp is closed.
func (tp *Transport) IsClosed() bool {
	select {
	case <-tp.done:
		return true
	default:
		return false
	}
}

// LocalPK returns the local public key of the transport.
func (tp *Transport) LocalPK() cipher.PubKey {
	return tp.lAddr.PK
}

// RemotePK returns the remote public key of the transport.
func (tp *Transport) RemotePK() cipher.PubKey {
	return tp.rAddr.PK
}

// LocalAddr returns local address in from <public-key>:<port>
func (tp *Transport) LocalAddr() net.Addr { return tp.lAddr }

// RemoteAddr returns remote address in form <public-key>:<port>
func (tp *Transport) RemoteAddr() net.Addr { return tp.rAddr }

// Type returns the transport type.
func (tp *Transport) Type() string {
	return Type
}

// HandleFrame allows 'tp.Serve' to handle the frame (typically from 'ClientConn').
func (tp *Transport) HandleFrame(f Frame) error {
	tp.inMx.Lock()
	defer tp.inMx.Unlock()
	for {
		if tp.IsClosed() {
			return io.ErrClosedPipe
		}
		select {
		case tp.inCh <- f:
			return nil
		default:
		}
	}
}

// WriteRequest writes a REQUEST frame to dmsg_server to be forwarded to associated client.
func (tp *Transport) WriteRequest() error {
	if err := writeRequestFrame(tp.Conn, tp.id, tp.lAddr, tp.rAddr, int32(tp.lW.Max())); err != nil {
		tp.log.WithError(err).Error("HandshakeFailed")
		tp.close()
		return err
	}
	return nil
}

// WriteAccept writes an ACCEPT frame to dmsg_server to be forwarded to associated client.
func (tp *Transport) WriteAccept(rWindow int) (err error) {
	defer func() {
		if err != nil {
			tp.log.WithError(err).WithField("remote", tp.rAddr).Warnln("(HANDSHAKE) Rejected locally.")
		} else {
			tp.log.WithField("remote", tp.rAddr).Infoln("(HANDSHAKE) Accepted locally.")
		}
	}()

	tp.rW = NewRemoteWindow(rWindow)

	if err = writeAcceptFrame(tp.Conn, tp.id, tp.lAddr, tp.rAddr, int32(tp.lW.Max())); err != nil {
		tp.close()
		return err
	}
	return nil
}

// ReadAccept awaits for an ACCEPT frame to be read from the remote client.
// TODO(evanlinjin): Cleanup errors.
func (tp *Transport) ReadAccept(ctx context.Context) (err error) {
	defer func() {
		if err != nil {
			switch err {
			case io.ErrClosedPipe, ErrRequestRejected:
				tp.close()
			default:
				if err := tp.Close(); err != nil {
					log.WithError(err).Warn("Failed to close transport")
				}
			}
			tp.log.WithError(err).WithField("remote", tp.rAddr).Warnln("(HANDSHAKE) Rejected by remote.")
		} else {
			tp.log.WithField("remote", tp.rAddr).Infoln("(HANDSHAKE) Accepted by remote.")
		}
	}()

	select {
	case <-tp.done:
		return io.ErrClosedPipe

	case <-ctx.Done():
		return ctx.Err()

	case f, ok := <-tp.inCh:
		if !ok {
			return io.ErrClosedPipe
		}
		switch ft, id, p := f.Disassemble(); ft {
		case AcceptType:
			hp, err := unmarshalHandshakeData(p)
			if err != nil || !isInitiatorID(id) ||
				hp.Version != HandshakePayloadVersion ||
				hp.InitAddr != tp.lAddr ||
				hp.RespAddr != tp.rAddr {
				return ErrAcceptCheckFailed
			}

			tp.rW = NewRemoteWindow(int(hp.Window))
			return nil

		case CloseType:
			return ErrRequestRejected

		default:
			return ErrAcceptCheckFailed
		}
	}
}

// Serve handles received frames.
func (tp *Transport) Serve() {
	// return is transport is already being served, or is closed
	if !tp.serve() {
		return
	}

	// ensure transport closes when serving stops
	// also write CLOSE frame if this is the first time 'close' is triggered
	defer func() {
		if tp.close() {
			if err := writeCloseFrame(tp.Conn, tp.id, PlaceholderReason); err != nil {
				log.WithError(err).Warn("Failed to write close frame")
			}
		}
	}()

	for {
		select {
		case <-tp.done:
			return

		case f, ok := <-tp.inCh:
			if !ok {
				return
			}
			log := tp.log.WithField("remoteClient", tp.rAddr).WithField("received", f)

			switch p := f.Pay(); f.Type() {
			case FwdType:
				log = log.WithField("payload_size", len(p))
				if err := tp.lW.Enqueue(p, tp.done); err != nil {
					log.WithError(err).Warn("Rejected [FWD]")
					return
				}
				log.Debug("Injected [FWD]")

			case AckType:
				offset, err := disassembleAckPayload(p)
				if err != nil {
					log.WithError(err).Warn("Rejected [ACK]: Failed to dissemble payload.")
					return
				}
				if err := tp.rW.Grow(int(offset), tp.done); err != nil {
					log.WithError(err).Warn("Rejected [ACK]: Failed to grow remote window.")
					return
				}
				log.Debug("Injected [ACK]")

			case CloseType:
				log.Info("Injected [CLOSE]: Closing transport...")
				tp.close() // ensure there is no sending of CLOSE frame
				return

			case RequestType:
				log.Warn("Rejected [REQUEST]: ID already occupied, possibly malicious server.")
				if err := tp.Conn.Close(); err != nil {
					log.WithError(err).Debug("Closing connection returned non-nil error.")
				}
				return

			default:
				tp.log.Warnf("Rejected [%s]: Unexpected frame, possibly malicious server (ignored for now).", f.Type())
			}
		}
	}
}

// Read implements io.Reader
// TODO(evanlinjin): read deadline.
func (tp *Transport) Read(p []byte) (n int, err error) {
	<-tp.serving

	return tp.lW.Read(p, tp.done, func(n uint16) {
		if err := writeAckFrame(tp.Conn, tp.id, n); err != nil {
			tp.close()
		}
	})
}

// Write implements io.Writer
// TODO(evanlinjin): write deadline.
func (tp *Transport) Write(p []byte) (int, error) {
	<-tp.serving

	if tp.IsClosed() {
		return 0, io.ErrClosedPipe
	}

	return tp.rW.Write(p, func(b []byte) error {
		if err := writeFwdFrame(tp.Conn, tp.id, p); err != nil {
			tp.close()
			return err
		}
		return nil
	})
}
