// Package proxystatus pkg/proxystatus/requests.go c4-app-web
package proxystatus

import (
	"context"
	"sync"
	"time"
)

// Request is one connection a resolving proxy opened for the browser: which
// layer took it, how it traveled, and how the dial ended.
type Request struct {
	At     time.Time `json:"at"`
	Layer  string    `json:"layer"` // "dmsg" or "skynet"
	Host   string    `json:"host"`
	Port   uint16    `json:"port"`
	Path   string    `json:"path,omitempty"` // e.g. "dmsg via <server-pk> over skynet", "skynet direct transport"
	Err    string    `json:"err,omitempty"`
	DialMS int64     `json:"dial_ms"`
}

// RequestLog keeps the most recent requests of every resolving proxy on a
// visor, oldest dropped first. A nil log records nothing.
type RequestLog struct {
	mu   sync.Mutex
	ring []Request
	next int
	full bool
}

// NewRequestLog keeps the last n requests.
func NewRequestLog(n int) *RequestLog { return &RequestLog{ring: make([]Request, n)} }

// Add records r.
func (l *RequestLog) Add(r Request) {
	if l == nil || len(l.ring) == 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.ring[l.next] = r
	l.next = (l.next + 1) % len(l.ring)
	if l.next == 0 {
		l.full = true
	}
}

// Recent returns the recorded requests of layer, newest first; every layer's
// when layer is empty.
func (l *RequestLog) Recent(layer string) []Request {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	n := l.next
	if l.full {
		n = len(l.ring)
	}
	out := make([]Request, 0, n)
	for i := 1; i <= n; i++ {
		r := l.ring[(l.next-i+len(l.ring))%len(l.ring)]
		if layer == "" || r.Layer == layer {
			out = append(out, r)
		}
	}
	return out
}

type pathKey struct{}

// PathNote is where a dialer reports how a dial traveled. Safe for
// concurrent use.
type PathNote struct {
	mu   sync.Mutex
	path string
}

// String returns the reported path, empty when none was reported.
func (n *PathNote) String() string {
	n.mu.Lock()
	defer n.mu.Unlock()
	return n.path
}

// WithPathNote returns a context a dialer can report its path into with
// NotePath, and the note it will be written to.
func WithPathNote(ctx context.Context) (context.Context, *PathNote) {
	note := new(PathNote)
	return context.WithValue(ctx, pathKey{}, note), note
}

// NotePath records how a dial traveled, for the request log. It does nothing
// when ctx carries no note.
func NotePath(ctx context.Context, path string) {
	if note, ok := ctx.Value(pathKey{}).(*PathNote); ok {
		note.mu.Lock()
		note.path = path
		note.mu.Unlock()
	}
}
