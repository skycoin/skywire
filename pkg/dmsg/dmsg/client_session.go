// Package dmsg pkg/dmsg/client_session.go
package dmsg

import (
	"context"
	"errors"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skywire-utilities/pkg/netutil"
)

// ClientSession represents a session from the perspective of a dmsg client.
type ClientSession struct {
	*SessionCommon
	porter *netutil.Porter
}

func makeClientSession(entity *EntityCommon, porter *netutil.Porter, conn net.Conn, rPK cipher.PubKey) (ClientSession, error) {
	var cSes ClientSession
	cSes.SessionCommon = new(SessionCommon)
	if err := cSes.SessionCommon.initClient(entity, conn, rPK); err != nil {
		return cSes, err
	}
	cSes.porter = porter
	return cSes, nil
}

// Close closes the client session and reaps any porter entries for streams
// belonging to this session. Without this reaping, ephemeral ports stay
// reserved after the session dies because nothing calls Stream.Close() on
// the orphaned streams — eventually exhausting the porter's 16k port range.
// Streams are matched by their underlying SessionCommon pointer because
// multiple ClientSession value copies can wrap the same SessionCommon.
func (cs *ClientSession) Close() error {
	if cs != nil && cs.porter != nil && cs.SessionCommon != nil {
		var toFree []*Stream
		cs.porter.RangePortValues(func(_ uint16, v interface{}) bool {
			if s, ok := v.(*Stream); ok && s != nil && s.ses != nil &&
				s.ses.SessionCommon == cs.SessionCommon {
				toFree = append(toFree, s)
			}
			return true
		})
		for _, s := range toFree {
			if s.close != nil {
				s.close()
			}
		}
	}
	return cs.SessionCommon.Close()
}

// DialStream attempts to dial a stream to a remote client via the dmsg server that this session is connected to.
// The context is used to cancel the dial if the caller's deadline expires — this prevents ephemeral port
// leaks when many dials are attempted and the caller gives up before the handshake completes.
func (cs *ClientSession) DialStream(ctx context.Context, dst Addr) (dStr *Stream, err error) {
	log := cs.log.
		WithField("func", "ClientSession.DialStream").
		WithField("dst_addr", dst)

	if dStr, err = newInitiatingStream(cs); err != nil {
		return nil, err
	}

	// Close stream on failure — this frees the reserved ephemeral port.
	defer func() {
		if err != nil {
			log.WithError(err).
				WithField("close_error", dStr.Close()).
				WithField("ports_reserved", cs.porter.Count()).
				Debug("Stream closed on failure.")
		}
	}()

	// If the caller's context is canceled, close the stream to interrupt
	// any blocked read/write and free the ephemeral port immediately.
	//
	// Synchronization is non-trivial. A naive watcher goroutine that
	// just `select`s on ctx.Done() vs a ctxDone signal races with the
	// post-handshake cleanup: if ctx is canceled *around the same
	// time* the handshake finishes (e.g. a sibling racePhaseDial
	// goroutine that canceled the shared raceCtx as soon as its own
	// winning dial returned), both select cases become ready
	// concurrently and the runtime picks one randomly. Picking
	// ctx.Done() in that instant closes the stream that DialStream
	// is about to return successfully, and the caller sees the Write
	// fail with "stream closed" or reads EOF.
	//
	// The fix is a mutex that serializes the decision. The watcher
	// grabs `mu` before inspecting `finished`; the main-flow defer
	// grabs `mu` first, marks `finished=true`, releases. Whichever
	// wins the mutex decides. If the watcher won and closed the
	// stream, it also sets `closedByWatcher=true` so the defer can
	// surface the race as a proper error to the caller rather than
	// handing them a dead stream.
	var (
		mu              sync.Mutex
		finished        bool
		closedByWatcher bool
	)
	ctxDone := make(chan struct{})
	go func() {
		select {
		case <-ctx.Done():
			mu.Lock()
			if !finished {
				closedByWatcher = true
				dStr.Close() //nolint:errcheck,gosec
			}
			mu.Unlock()
		case <-ctxDone:
		}
	}()
	defer func() {
		mu.Lock()
		finished = true
		wasClosed := closedByWatcher
		mu.Unlock()
		close(ctxDone)
		// If the watcher fired and closed the stream but the main
		// flow also finished the handshake with err==nil, the
		// returned stream is unusable. Report it as a cancellation
		// and release the now-nil pointer so the outer failure
		// defer at line 69 becomes a no-op.
		if wasClosed && err == nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				err = ctxErr
			} else {
				err = context.Canceled
			}
			dStr = nil
		}
	}()

	// Check context before starting.
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}

	// Prepare deadline.
	if err = dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}

	// Do stream handshake.
	req, err := dStr.writeRequest(dst)
	if err != nil {
		return nil, err
	}

	if err := dStr.readResponse(req); err != nil {
		return nil, err
	}

	// Set idle timeout — refreshed on each successful read.
	if err = dStr.SetReadDeadline(time.Now().Add(StreamIdleTimeout)); err != nil {
		return nil, err
	}
	// Clear the write deadline so writes are not affected.
	if err = dStr.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}

	return dStr, err
}

// LookupIP attempts to dial a stream to the server for the IP address of the client.
func (cs *ClientSession) LookupIP(dst Addr) (myIP net.IP, err error) {
	log := cs.log.
		WithField("func", "ClientSession.LookupIP").
		WithField("dst_addr", cs.rPK)

	dStr, err := newInitiatingStream(cs)
	if err != nil {
		return nil, err
	}

	// Close stream on failure.
	defer func() {
		if err != nil {
			log.WithError(err).
				WithField("close_error", dStr.Close()).
				Debug("Stream closed on failure.")
		}
	}()

	// Prepare deadline.
	if err = dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}

	// Do stream handshake.
	req, err := dStr.writeIPRequest(dst)
	if err != nil {
		return nil, err
	}

	myIP, err = dStr.readIPResponse(req)
	if err != nil {
		return nil, err
	}

	err = dStr.Close()
	if err != nil {
		return nil, err
	}

	return myIP, err
}

// serve accepts incoming streams from remote clients.
func (cs *ClientSession) serve() error {
	defer func() {
		if err := cs.Close(); err != nil {
			cs.log.WithError(err).
				Debug("On (*ClientSession).serve() return, close client session resulted in error.")
		}
	}()
	for {
		if _, err := cs.acceptStream(); err != nil {
			if netErr, ok := err.(net.Error); ok && netErr.Temporary() { //nolint
				cs.log.
					WithError(err).
					Debug("Failed to accept stream.")
				continue
			}

			if errors.Is(err, yamux.ErrSessionShutdown) {
				cs.log.WithError(err).Debug("Stopped accepting streams.")
				return err
			}
			cs.log.WithError(err).Warn("Stopped accepting streams.")
			return err
		}
	}
}

func (cs *ClientSession) acceptStream() (dStr *Stream, err error) {
	if dStr, err = newRespondingStream(cs); err != nil {
		return nil, err
	}

	// Close stream on failure.
	defer func() {
		if err != nil {
			if scErr := dStr.Close(); scErr != nil {
				cs.log.WithError(scErr).
					Debug("On (*ClientSession).acceptStream() failure, close stream resulted in error.")
			}
		}
	}()

	// Prepare deadline.
	if err = dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return nil, err
	}

	// Do stream handshake.
	req, err := dStr.readRequest()
	if err != nil {
		return nil, err
	}
	if err = dStr.writeResponse(req.raw.Hash()); err != nil {
		return nil, err
	}

	// Set idle timeout — refreshed on each successful read.
	if err = dStr.SetReadDeadline(time.Now().Add(StreamIdleTimeout)); err != nil {
		return nil, err
	}
	// Clear the write deadline so writes are not affected.
	if err = dStr.SetWriteDeadline(time.Time{}); err != nil {
		return nil, err
	}

	return dStr, err
}
