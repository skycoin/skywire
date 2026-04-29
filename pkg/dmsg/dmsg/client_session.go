// Package dmsg pkg/dmsg/client_session.go
package dmsg

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"time"

	"github.com/hashicorp/yamux"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/netutil"
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
			s.closeMu.Lock()
			closeFn := s.close
			s.closeMu.Unlock()
			if closeFn != nil {
				closeFn()
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
	// Synchronization between the watcher and main-flow defer is
	// non-trivial. A naive watcher that just `select`s on ctx.Done()
	// vs ctxDone races with the post-handshake cleanup when ctx is
	// canceled around the same time the handshake finishes: both
	// select cases become ready and the runtime picks one randomly.
	// Picking ctx.Done() in that instant closes the stream that
	// DialStream is about to return successfully, and the caller
	// sees the Write fail with "stream closed" or reads EOF.
	//
	// A mutex serializes the decision: the watcher grabs `mu` before
	// inspecting `finished`; the main-flow defer grabs `mu` first and
	// sets `finished=true`. Whichever wins the mutex decides. If the
	// watcher won and closed the stream, it also sets
	// `closedByWatcher=true` so the defer can surface the race as a
	// proper error to the caller rather than handing them a dead
	// stream.
	//
	// IMPORTANT: this mutex is necessary but NOT sufficient for
	// callers that derive a per-attempt WithTimeout+defer cancel
	// pattern around DialStream. The handshake REQUEST has already
	// reached the destination and been introduced into its listener
	// accept queue by the time the watcher fires; converting the
	// dial-side result to an error does not undo that introduction.
	// Closing the dial-side stream propagates FIN through the server
	// bridge, leaving a DEAD stream in the destination's accept
	// buffer that subsequent AcceptStream calls dequeue — breaking
	// any caller that expects consecutive dials to produce
	// consecutive, usable streams on the destination. So callers
	// must NOT derive a timeout shorter than HandshakeTimeout —
	// use the parent ctx directly and rely on the stream's own
	// SetDeadline (set to HandshakeTimeout below) to bound the dial.
	// See sequentialPhaseDial in client_dial.go for the caller-side
	// contract.
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
//
// The accept loop is split into two error classes:
//
//   - Session-level errors (yamux.ErrSessionShutdown, or any error from
//     the yamux AcceptStream call itself): the underlying yamux session
//     is gone, there's nothing to accept anymore. Return the error so
//     the enclosing dialSession goroutine cleans up via delSession and
//     the reconnect loop re-dials.
//
//   - Stream-level errors (a single incoming stream failed its noise
//     handshake — bad peer, handshake deadline hit, peer disconnected
//     mid-handshake, garbled request bytes, etc.): the session itself
//     is still alive and should continue accepting other streams. Log
//     at Debug level and loop. Previously any such error terminated
//     the whole session, causing long-running visors to decay their
//     dmsg session count as random bad peers triggered session kills
//     — the root cause of the "zero delegated servers" partial-failure
//     pattern seen in production (e.g. visors at sequence >1000
//     accumulating from 6 servers down to 0 over days of uptime).
func (cs *ClientSession) serve() error {
	defer func() {
		if err := cs.Close(); err != nil {
			cs.log.WithError(err).
				Debug("On (*ClientSession).serve() return, close client session resulted in error.")
		}
	}()
	for {
		dStr, err := cs.acceptYamuxStream()
		if err != nil {
			// yamux-level failure. Shutdown / closed / EOF are
			// terminal — the underlying session is dead. Everything
			// else is treated as potentially-transient and retried
			// with a short backoff, mirroring the server-side loop
			// in ServerSession.Serve (streamErrorBackoff = 50ms).
			if errors.Is(err, yamux.ErrSessionShutdown) || errors.Is(err, io.EOF) || cs.sm.yamux.IsClosed() {
				cs.log.WithError(err).Debug("Session shut down, stopping accept loop.")
				return err
			}
			cs.log.WithError(err).Warn("Failed to accept yamux stream, continuing...")
			time.Sleep(streamErrorBackoff)
			continue
		}

		if err := cs.handshakeResponder(dStr); err != nil {
			// Stream-level handshake failure: close the one bad
			// stream and keep the session alive. Previously
			// returned from serve, killing the whole session —
			// the root cause of the dmsg session rot seen in
			// long-running visors (sessions decayed from 6 to 0
			// as random bad peers triggered session kills).
			//
			// Include src/dst from the partially-populated stream so
			// listener-miss diagnostics show the originator PK and
			// the destination port. Fields default to zero values when
			// the failure happens before prepareFields runs.
			cs.log.WithError(err).
				WithField("src_pk", dStr.rAddr.PK).
				WithField("src_port", dStr.rAddr.Port).
				WithField("dst_port", dStr.lAddr.Port).
				Debug("Stream handshake failed, continuing.")
			if closeErr := dStr.Close(); closeErr != nil {
				cs.log.WithError(closeErr).
					Debug("On handshakeResponder failure, close stream resulted in error.")
			}
			continue
		}
	}
}

// acceptYamuxStream performs only the yamux-level accept. Errors from
// this function indicate the session itself is dead (or about to be).
func (cs *ClientSession) acceptYamuxStream() (*Stream, error) {
	return newRespondingStream(cs)
}

// handshakeResponder performs the noise handshake on an already-
// accepted yamux stream. Errors from this function are stream-level
// and should NOT terminate the enclosing session; the caller is
// responsible for closing dStr on failure.
func (cs *ClientSession) handshakeResponder(dStr *Stream) error {
	// Prepare deadline.
	if err := dStr.SetDeadline(time.Now().Add(HandshakeTimeout)); err != nil {
		return err
	}

	// Do stream handshake.
	req, err := dStr.readRequest()
	if err != nil {
		return err
	}
	if err := dStr.writeResponse(req.raw.Hash()); err != nil {
		return err
	}

	// Set idle timeout — refreshed on each successful read.
	if err := dStr.SetReadDeadline(time.Now().Add(StreamIdleTimeout)); err != nil {
		return err
	}
	// Clear the write deadline so writes are not affected.
	if err := dStr.SetWriteDeadline(time.Time{}); err != nil {
		return err
	}

	return nil
}
