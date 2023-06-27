// Package dmsgctrl pkg/dmsgctrl/control.go
package dmsgctrl

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"
)

// Associated errors.
var (
	ErrClosed = errors.New("control is closed")
)

// PacketType represents the packet type.
type PacketType byte

// Packet types
const (
	PingType = PacketType(0x9)
	PongType = PacketType(0xA)
)

// Control wraps and takes over a dmsg.Stream and provides control features.
type Control struct {
	conn    net.Conn
	pongCh  chan time.Time
	doneCh  chan struct{}
	err     error // the resultant error after control stops serving
	errOnce sync.Once
}

// ControlStream wraps a dmsg.Stream and returns the Control.
func ControlStream(conn net.Conn) *Control {
	const pongChSize = 10

	ctrl := &Control{
		conn:   conn,
		pongCh: make(chan time.Time, pongChSize),
		doneCh: make(chan struct{}),
	}
	go ctrl.serve()

	return ctrl
}

func (c *Control) serve() {
	defer close(c.pongCh)

	for {
		rawType := make([]byte, 1)
		if _, err := io.ReadFull(c.conn, rawType); err != nil {
			c.reportErr(err)
			return
		}

		switch pt := PacketType(rawType[0]); pt {
		case PingType:
			if _, err := c.conn.Write([]byte{byte(PongType)}); err != nil {
				c.reportErr(fmt.Errorf("failed to write pong: %w", err))
				return
			}

		case PongType:
			select {
			case c.pongCh <- time.Now():
			default:
			}

		default:
			c.reportErr(fmt.Errorf("received unknown packet type '%v'", pt))
			return
		}
	}
}

// Ping sends a ping and blocks until a pong is received from remote.
// Context can be specified for early cancellation.
// Would also return early if Control stops serving.
func (c *Control) Ping(ctx context.Context) (time.Duration, error) {
	start := time.Now()

	if _, err := c.conn.Write([]byte{byte(PingType)}); err != nil {
		return 0, err
	}

	select {
	case <-ctx.Done():
		return 0, ctx.Err()

	case t, ok := <-c.pongCh:
		if !ok {
			return 0, c.err
		}
		return t.Sub(start), nil
	}
}

// Conn returns the internal net.Conn
func (c *Control) Conn() net.Conn {
	return c.conn
}

// Close implements io.Closer
func (c *Control) Close() error {
	if isDone(c.doneCh) {
		return c.err
	}

	c.reportErr(ErrClosed)
	return c.conn.Close()
}

// Done the returned channel unblocks when the control stops serving.
func (c *Control) Done() <-chan struct{} {
	return c.doneCh
}

// Err returns the resultant error (if any).
// If Control has not stopped, Err always returns nil.
func (c *Control) Err() error {
	if !isDone(c.doneCh) {
		return nil
	}
	return c.err
}

func (c *Control) reportErr(err error) {
	c.errOnce.Do(func() {
		c.err = err
		close(c.doneCh)
	})
}

func isDone(done <-chan struct{}) bool {
	select {
	case <-done:
		return true
	default:
		return false
	}
}
