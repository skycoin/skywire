//go:build js && wasm

package childtty

import (
	"io"
	"sync"

	"github.com/0magnet/bottle/proc"
	"github.com/gdamore/tcell/v3/tty"

	"github.com/0magnet/websh/progressive"
)

// Open returns this program's terminal, or false when it has none. It asks
// the host what it offers first (progressive.Probe), before anything else reads
// the terminal; progressive.Current has the answer. The host's events on the
// program's placements are taken out of the input and arrive on Events.
func Open() (tty.Tty, bool) {
	t, ok := proc.Term()
	if !ok {
		return nil, false
	}
	c := &childTty{t: t}
	_, in, _ := progressive.Probe(t, t, probeWait) //nolint:errcheck // no answer means cells only; in is the input from here either way
	c.in, events = progressive.Filter(in)
	t.OnResize(c.resized)
	return c, true
}

type childTty struct {
	t  *proc.Terminal
	in io.Reader // the terminal's input, after the probe
	mu sync.Mutex
	ch chan<- bool
}

func (c *childTty) Start() error { c.t.SetRaw(true); return nil }
func (c *childTty) Stop() error  { c.t.SetRaw(false); return nil }

// Drain does nothing: tcell reads on a goroutine it leaves behind when it
// stops, so a read waiting here does not hold it up.
func (c *childTty) Drain() error { return nil }

func (c *childTty) NotifyResize(ch chan<- bool) {
	c.mu.Lock()
	c.ch = ch
	c.mu.Unlock()
}

func (c *childTty) resized(int, int) {
	c.mu.Lock()
	ch := c.ch
	c.mu.Unlock()
	if ch != nil {
		select {
		case ch <- true:
		default:
		}
	}
}

func (c *childTty) WindowSize() (tty.WindowSize, error) {
	w, h := c.t.Size()
	return tty.WindowSize{Width: w, Height: h}, nil
}

func (c *childTty) Read(p []byte) (int, error)  { return c.in.Read(p) }
func (c *childTty) Write(p []byte) (int, error) { return c.t.Write(p) }
func (c *childTty) Close() error                { return nil }
