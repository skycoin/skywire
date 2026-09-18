// Package emu pkg/router/emu/conn.go
package emu

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	types "github.com/skycoin/skywire/pkg/transport/types"
)

var (
	errClosed  error = net.ErrClosed
	errTimeout error = timeoutError{}
)

type timeoutError struct{}

func (timeoutError) Error() string   { return "emu: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

// inbox is one endpoint's arrival queue. Frames keep their boundaries: one
// push is one Read.
type inbox struct {
	mu     sync.Mutex
	q      [][]byte
	bytes  int64
	notify chan struct{}
	closed chan struct{}
	once   sync.Once
}

func newInbox() *inbox {
	return &inbox{notify: make(chan struct{}, 1), closed: make(chan struct{})}
}

func (i *inbox) push(b []byte) {
	i.mu.Lock()
	select {
	case <-i.closed:
		i.mu.Unlock()
		return
	default:
	}
	i.q = append(i.q, b)
	i.bytes += int64(len(b))
	i.mu.Unlock()
	select {
	case i.notify <- struct{}{}:
	default:
	}
}

func (i *inbox) pop() ([]byte, bool) {
	i.mu.Lock()
	defer i.mu.Unlock()
	if len(i.q) == 0 {
		return nil, false
	}
	b := i.q[0]
	i.q[0] = nil
	i.q = i.q[1:]
	i.bytes -= int64(len(b))
	return b, true
}

func (i *inbox) close() {
	i.once.Do(func() {
		i.mu.Lock()
		close(i.closed)
		i.mu.Unlock()
		select {
		case i.notify <- struct{}{}:
		default:
		}
	})
}

// Conn is one end of an emulated leg. It satisfies network.Transport, so
// transport.NewManagedTransportForTest wraps it exactly as it wraps a real
// stcpr or dmsg conn.
//
// It is MESSAGE oriented: one Write is one frame, one Read returns one whole
// frame. The router writes exactly one routing.Packet per Write, so this
// preserves packet boundaries — and it is what lets a direction reorder or
// drop an individual packet, which a TCP-like leg never does.
type Conn struct {
	name     string
	lpk, rpk cipher.PubKey
	netType  types.Type

	out *link  // this end's egress
	in  *inbox // this end's arrivals

	rdl, wdl atomic.Int64 // deadlines, unix nanos; 0 = none

	closed    chan struct{}
	closeOnce sync.Once
	peerClose func()

	cur []byte // unread remainder of a short-buffered frame
}

// PairConfig describes an emulated leg: the conditions in each direction and
// the identities the two ends present.
type PairConfig struct {
	// Name labels the leg in reports.
	Name string
	// AtoB governs frames written by the A end; BtoA those written by B.
	AtoB, BtoA LinkConfig
	// APK and BPK are the public keys each end reports as local; each end
	// reports the other's as remote.
	APK, BPK cipher.PubKey
	// Type is the transport type string the leg claims.
	Type types.Type
}

// NewPair builds one emulated leg and returns its two ends.
func NewPair(cfg PairConfig) (a, b *Conn) {
	if cfg.Type == "" {
		cfg.Type = "emu"
	}
	inA, inB := newInbox(), newInbox()
	ab := newLink(cfg.AtoB, inB)
	ba := newLink(cfg.BtoA, inA)

	a = &Conn{name: cfg.Name + ":a", lpk: cfg.APK, rpk: cfg.BPK, netType: cfg.Type,
		out: ab, in: inA, closed: make(chan struct{})}
	b = &Conn{name: cfg.Name + ":b", lpk: cfg.BPK, rpk: cfg.APK, netType: cfg.Type,
		out: ba, in: inB, closed: make(chan struct{})}
	a.peerClose = b.shutdown
	b.peerClose = a.shutdown
	return a, b
}

// Egress returns the direction this end writes into, for Cut/Restore/stats.
func (c *Conn) Egress() *Direction { return &Direction{l: c.out} }

// Direction exposes one direction's switches and counters.
type Direction struct{ l *link }

// Cut black-holes the direction, in-flight frames included.
func (d *Direction) Cut() { d.l.Cut() }

// Restore lifts a Cut.
func (d *Direction) Restore() { d.l.Restore() }

// IsCut reports whether the direction is cut.
func (d *Direction) IsCut() bool { return d.l.IsCut() }

// Stats returns the direction's lifetime counters.
func (d *Direction) Stats() LinkStats { return d.l.stats() }

// Write sends one frame.
func (c *Conn) Write(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, errClosed
	default:
	}
	var dl time.Time
	if v := c.wdl.Load(); v != 0 {
		dl = time.Unix(0, v)
		if !time.Now().Before(dl) {
			return 0, errTimeout
		}
	}
	if err := c.out.send(p, dl); err != nil {
		return 0, err
	}
	return len(p), nil
}

// Read returns one whole frame. A buffer too small for the frame is filled
// and the remainder is returned by the following Read, so a byte-stream
// reader still sees every byte in order.
func (c *Conn) Read(p []byte) (int, error) {
	if len(c.cur) > 0 {
		n := copy(p, c.cur)
		c.cur = c.cur[n:]
		return n, nil
	}
	for {
		if b, ok := c.in.pop(); ok {
			n := copy(p, b)
			if n < len(b) {
				c.cur = b[n:]
			}
			return n, nil
		}
		var wait <-chan time.Time
		if v := c.rdl.Load(); v != 0 {
			d := time.Until(time.Unix(0, v))
			if d <= 0 {
				return 0, errTimeout
			}
			t := time.NewTimer(d)
			defer t.Stop()
			wait = t.C
		}
		select {
		case <-c.closed:
			return 0, io.EOF
		case <-c.in.closed:
			return 0, io.EOF
		case <-c.in.notify:
		case <-wait:
			return 0, errTimeout
		}
	}
}

// Close shuts both ends of the leg down.
func (c *Conn) Close() error {
	c.shutdown()
	if c.peerClose != nil {
		c.peerClose()
	}
	return nil
}

func (c *Conn) shutdown() {
	c.closeOnce.Do(func() {
		close(c.closed)
		c.in.close()
		c.out.close()
	})
}

// SetDeadline implements net.Conn.
func (c *Conn) SetDeadline(t time.Time) error {
	_ = c.SetReadDeadline(t) //nolint:errcheck // the emulated deadline setters cannot fail
	return c.SetWriteDeadline(t)
}

// SetReadDeadline implements net.Conn.
func (c *Conn) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		c.rdl.Store(0)
	} else {
		c.rdl.Store(t.UnixNano())
	}
	return nil
}

// SetWriteDeadline implements net.Conn.
func (c *Conn) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		c.wdl.Store(0)
	} else {
		c.wdl.Store(t.UnixNano())
	}
	return nil
}

// LocalAddr implements net.Conn.
func (c *Conn) LocalAddr() net.Addr { return emuAddr(c.name) }

// RemoteAddr implements net.Conn.
func (c *Conn) RemoteAddr() net.Addr { return emuAddr(c.name + ":peer") }

// LocalRawAddr implements network.Transport.
func (c *Conn) LocalRawAddr() net.Addr { return emuAddr(c.name) }

// RemoteRawAddr implements network.Transport.
func (c *Conn) RemoteRawAddr() net.Addr { return emuAddr(c.name + ":peer") }

// LocalPK implements network.Transport.
func (c *Conn) LocalPK() cipher.PubKey { return c.lpk }

// RemotePK implements network.Transport.
func (c *Conn) RemotePK() cipher.PubKey { return c.rpk }

// LocalPort implements network.Transport.
func (c *Conn) LocalPort() uint16 { return 0 }

// RemotePort implements network.Transport.
func (c *Conn) RemotePort() uint16 { return 0 }

// Network implements network.Transport.
func (c *Conn) Network() types.Type { return c.netType }

type emuAddr string

func (a emuAddr) Network() string { return "emu" }
func (a emuAddr) String() string  { return string(a) }

// SetRateBps changes the direction's rate limit mid-flight.
func (d *Direction) SetRateBps(v int64) { d.l.setCfg(func(c *LinkConfig) { c.RateBps = v }) }

// SetLossPct changes the direction's loss rate mid-flight.
func (d *Direction) SetLossPct(v float64) { d.l.setCfg(func(c *LinkConfig) { c.LossPct = v }) }

// SetReorderPct changes the direction's reorder rate mid-flight.
func (d *Direction) SetReorderPct(v float64) { d.l.setCfg(func(c *LinkConfig) { c.ReorderPct = v }) }

// SetDelay changes the direction's one-way delay mid-flight.
func (d *Direction) SetDelay(v time.Duration) { d.l.setCfg(func(c *LinkConfig) { c.Delay = v }) }
