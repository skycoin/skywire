//go:build js && wasm

package web

import (
	"io"
	"sync"

	"github.com/0magnet/websh/progressive"
)

// inQueue is the running command's stdin: what is typed, in order, and what
// the terminal answers to the command's own queries.
//
// It was an io.Pipe fed by one goroutine, which kept the order but could not
// take anything back: a write blocks until some command reads it. A reply to
// a query is the one thing that must be taken back. The command that asked
// may never read it (printf '\e[c'), and the next command to read stdin would
// get it instead, as though typed. So each reply carries the command it
// arrived during, and is dropped once that command has ended. Typed keys
// carry over, as type-ahead does in any terminal.
//
// Adding never blocks — it is called from the JS callback that delivers a
// keystroke, and blocking there freezes the page — and a full queue drops.
type inQueue struct {
	mu     sync.Mutex
	more   *sync.Cond
	items  []inItem
	size   int // bytes queued
	cmd    int // the command running now
	closed bool
}

type inItem struct {
	b     []byte
	reply bool // the terminal's own, for command cmd only
	cmd   int
	wake  bool // makes a waiting Read return with nothing
	// interrupt ends a Read the command cmd is waiting in, as at the end
	// of its input: Ctrl+C, which cancels the command, cannot reach one
	// blocked in a read otherwise.
	interrupt bool
}

// inQueueLimit is more than a person can type ahead.
const inQueueLimit = 1 << 20

func newInQueue() *inQueue {
	q := &inQueue{}
	q.more = sync.NewCond(&q.mu)
	return q
}

func (q *inQueue) push(it inItem) {
	q.mu.Lock()
	defer q.mu.Unlock()
	if q.closed || q.size+len(it.b) > inQueueLimit {
		return
	}
	it.cmd = q.cmd
	q.items = append(q.items, it)
	q.size += len(it.b)
	q.more.Signal()
}

// next starts a new command: replies meant for the last one go.
func (q *inQueue) next() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.cmd++
}

func (q *inQueue) close() {
	q.mu.Lock()
	defer q.mu.Unlock()
	q.closed = true
	q.more.Broadcast()
}

// Read waits for input and returns it.
func (q *inQueue) Read(p []byte) (int, error) {
	q.mu.Lock()
	defer q.mu.Unlock()
	for {
		for len(q.items) > 0 && (q.items[0].reply || q.items[0].interrupt) && q.items[0].cmd != q.cmd {
			q.size -= len(q.items[0].b)
			q.items = q.items[1:]
		}
		if len(q.items) > 0 {
			it := &q.items[0]
			if it.interrupt {
				q.items = q.items[1:]
				return 0, io.EOF
			}
			if it.wake {
				q.items = q.items[1:]
				return 0, nil
			}
			n := copy(p, it.b)
			it.b = it.b[n:]
			q.size -= n
			if len(it.b) == 0 {
				q.items = q.items[1:]
			}
			return n, nil
		}
		if q.closed {
			return 0, io.EOF
		}
		q.more.Wait()
	}
}

// event reports something that happened to placement id to the program on
// the terminal, on its input. Like a reply, it is for the command running
// now: none is queued at the prompt, and one it never reads goes with it.
func (s *Session) event(id string, e *progressive.Event) {
	if s.running && s.in != nil {
		s.in.push(inItem{b: []byte(progressive.EventSeq(id, e)), reply: true})
	}
}
