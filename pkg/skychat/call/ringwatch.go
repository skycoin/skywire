// Package call pkg/skychat/call/ringwatch.go c4-app-chat
package call

import (
	"net"
	"sync"
)

// ringWatchBuf is how much of the caller's stream one watching read may take.
// It only ever holds a hang-up frame, or — past an answer — the first media
// bytes, which are a two-byte length and one RTP packet.
const ringWatchBuf = 2048

// ringWatch watches the signaling conn of an invite that is RINGING, so the
// callee stops ringing the moment the caller gives up.
//
// Nothing used to watch it. A ring ended on an answer, a decline, or the
// forty-five-second timeout, and a caller who hung up mid-ring was none of the
// three — so the callee went on ringing for a call nobody was placing any
// more, for whatever was left of the timeout: the twenty to thirty seconds of
// ringtone reported from a phone whose caller had already put the handset
// down. The cancel does reach the callee (the caller closes the signaling
// conn, and now sends SigHangup first), but only a reader sees it arrive, and
// until an answer there was no reader.
//
// The awkward part is the handover, because this conn becomes the MEDIA conn
// the instant the call is answered and the watching read cannot be called
// back: a read deadline is a no-op on some of the carriers voice runs over
// (appnet's directConn sets none), so a Read already blocked cannot be
// interrupted. So the watch does not try to interrupt it. It stays the conn's
// only reader until its Read returns, and whatever that read yields after an
// answer — the caller's first media bytes — it hands to the session, which
// reads THROUGH the watch and so never races it. Before an answer, anything
// it yields, bytes or error alike, means one thing: the caller is gone.
type ringWatch struct {
	net.Conn

	// gone is closed when the caller gave up before we answered.
	gone chan struct{}
	// handed is closed once the watching read has returned and the conn is
	// the session's to read.
	handed chan struct{}

	mu       sync.Mutex
	answered bool
	pending  []byte // what the watching read took, owed to the session
}

// watchRing starts watching conn and returns the wrapper the rest of the call
// must use in its place.
func watchRing(conn net.Conn) *ringWatch {
	w := &ringWatch{Conn: conn, gone: make(chan struct{}), handed: make(chan struct{})}
	go w.watch()
	return w
}

func (w *ringWatch) watch() {
	buf := make([]byte, ringWatchBuf)
	// The error is deliberately dropped: before an answer, bytes and a broken
	// conn both mean the caller is gone, and after one the session will meet
	// the same error on its own next read.
	n, _ := w.Conn.Read(buf) //nolint:errcheck // deliberately dropped, per the comment above
	w.mu.Lock()
	answered := w.answered
	if answered && n > 0 {
		w.pending = buf[:n]
	}
	w.mu.Unlock()
	close(w.handed)
	if !answered {
		close(w.gone)
	}
}

// answer marks the call accepted. From here the conn is the media conn and
// what the watch reads belongs to the session rather than to the ring.
//
// A read that returns at the same instant still closes gone, which is
// harmless: the only waiter on it is the ring, and the ring is over — that is
// what called this.
func (w *ringWatch) answer() {
	w.mu.Lock()
	w.answered = true
	w.mu.Unlock()
}

// Read serves the session. It must not touch the raw conn until the watching
// read has finished with it: two readers on one stream would split a media
// frame between them. So it waits for the handover, returns the bytes the
// watch already took, and is a straight pass-through after that.
func (w *ringWatch) Read(p []byte) (int, error) {
	<-w.handed
	w.mu.Lock()
	if len(w.pending) > 0 {
		n := copy(p, w.pending)
		w.pending = w.pending[n:]
		w.mu.Unlock()
		return n, nil
	}
	w.mu.Unlock()
	return w.Conn.Read(p)
}
