// Package wisp pkg/wisp/stream.go c4-app-proxy
//
// One Wisp stream: the socket at the far end, the two pumps that move bytes
// across it, and the credit accounting that paces the client.
package wisp

import (
	"context"
	"errors"
	"io"
	"net"
	"sync"
	"sync/atomic"
)

// readChunk is how much is taken off a TCP socket per DATA frame. It is a
// compromise: large enough that a bulk download is not shredded into tiny
// frames, small enough that one slow stream does not sit on the session's
// write queue for long.
const readChunk = 32 * 1024

// stream is one multiplexed connection within a session.
type stream struct {
	id   uint32
	typ  uint8
	sess *session

	inbound chan []byte
	done    chan struct{}

	closeOnce sync.Once

	// silent suppresses the CLOSE this end would otherwise send when the
	// far socket goes away. It is set when the client closed the stream
	// itself, or when the whole session is being torn down: in both cases
	// the client already knows, and answering its CLOSE with another one
	// makes a well-behaved client warn about a packet for a stream it has
	// already forgotten.
	silent atomic.Bool

	mu    sync.Mutex
	conn  net.Conn
	dgram DatagramStream
}

// sendClose emits a CLOSE for this stream unless the client already knows.
func (st *stream) sendClose(ctx context.Context, reason uint8) {
	if st.silent.Load() {
		return
	}
	st.sess.send(ctx, EncodeClose(st.id, reason))
}

// dialAndServe opens the far end and then runs the stream until either side
// finishes. It always removes the stream from the session before returning.
func (st *stream) dialAndServe(ctx context.Context, payload ConnectPayload) {
	defer st.sess.removeStream(st.id)

	dialCtx, cancel := context.WithTimeout(ctx, st.sess.srv.cfg.DialTimeout)
	defer cancel()

	var err error
	switch st.typ {
	case StreamTCP:
		var c net.Conn
		c, err = st.sess.srv.cfg.Egress.DialTCP(dialCtx, payload.Host, payload.Port)
		if err == nil {
			st.mu.Lock()
			st.conn = c
			st.mu.Unlock()
		}
	case StreamUDP:
		var d DatagramStream
		d, err = st.sess.srv.cfg.Egress.DialUDP(dialCtx, payload.Host, payload.Port)
		if err == nil {
			st.mu.Lock()
			st.dgram = d
			st.mu.Unlock()
		}
	}
	if err != nil {
		reason := closeReasonFor(err)
		st.sess.log.WithError(err).Debugf("stream %d: dial %s:%d failed (close 0x%02x)",
			st.id, payload.Host, payload.Port, reason)
		st.sendClose(ctx, reason)
		st.closeOnce.Do(func() { close(st.done) })
		return
	}

	st.sess.log.Debugf("stream %d open: %s %s:%d", st.id, streamTypeName(st.typ), payload.Host, payload.Port)

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		st.pumpFromSocket(ctx)
	}()

	st.pumpToSocket(ctx)
	st.close()
	wg.Wait()
}

// pumpToSocket moves client DATA onto the socket and refreshes credit.
//
// The credit is counted as packets are drained rather than as they arrive, so
// a client can never get a fresh buffer for data the far end has not yet
// accepted. UDP streams carry no credit at all and so get no CONTINUE.
func (st *stream) pumpToSocket(ctx context.Context) {
	var drained uint32
	buffer := st.sess.srv.cfg.Buffer

	for {
		var payload []byte
		select {
		case <-ctx.Done():
			return
		case <-st.done:
			return
		case payload = <-st.inbound:
		}

		st.mu.Lock()
		conn, dgram := st.conn, st.dgram
		st.mu.Unlock()

		var err error
		switch {
		case conn != nil:
			_, err = conn.Write(payload)
		case dgram != nil:
			err = dgram.WriteDatagram(payload)
		default:
			return
		}
		if err != nil {
			if !isExpectedClose(err) {
				st.sess.log.WithError(err).Debugf("stream %d: write to socket failed", st.id)
			}
			st.sendClose(ctx, closeReasonFor(err))
			return
		}

		if st.typ != StreamTCP {
			continue
		}
		drained++
		if drained >= buffer {
			drained -= buffer
			st.sess.send(ctx, EncodeContinue(st.id, buffer))
		}
	}
}

// pumpFromSocket moves socket bytes back to the client as DATA frames. There
// is no credit in this direction by design — see the note in server.go.
func (st *stream) pumpFromSocket(ctx context.Context) {
	st.mu.Lock()
	conn, dgram := st.conn, st.dgram
	st.mu.Unlock()

	for {
		var (
			payload []byte
			err     error
		)
		switch {
		case conn != nil:
			buf := make([]byte, readChunk)
			var n int
			n, err = conn.Read(buf)
			if n > 0 {
				payload = buf[:n]
			}
		case dgram != nil:
			payload, err = dgram.ReadDatagram()
		default:
			return
		}

		if len(payload) > 0 {
			select {
			case st.sess.writes <- EncodeData(st.id, payload):
			case <-ctx.Done():
				return
			case <-st.done:
				return
			}
		}

		if err != nil {
			reason := CloseVoluntary
			if !errors.Is(err, io.EOF) {
				reason = closeReasonFor(err)
				if !isExpectedClose(err) {
					st.sess.log.WithError(err).Debugf("stream %d: read from socket failed", st.id)
				}
			}
			st.sendClose(ctx, reason)
			st.close()
			return
		}
	}
}

// close tears down the far end. It is safe to call more than once and from
// either pump.
func (st *stream) close() {
	st.closeOnce.Do(func() {
		close(st.done)
		st.mu.Lock()
		conn, dgram := st.conn, st.dgram
		st.mu.Unlock()
		if conn != nil {
			conn.Close() //nolint:errcheck,gosec // tearing down regardless
		}
		if dgram != nil {
			dgram.Close() //nolint:errcheck,gosec // tearing down regardless
		}
	})
}

// isExpectedClose reports whether an error is the ordinary end of a
// connection rather than something worth a log line.
func isExpectedClose(err error) bool {
	return errors.Is(err, io.EOF) ||
		errors.Is(err, net.ErrClosed) ||
		errors.Is(err, context.Canceled)
}

func streamTypeName(t uint8) string {
	if t == StreamUDP {
		return "udp"
	}
	return "tcp"
}
