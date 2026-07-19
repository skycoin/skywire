// Package market is the skydex-client's transport to an skydex-market.
//
// It does not implement dmsg. The connection is a Skywire app net.Conn obtained
// by dialing the market's public key over appnet.TypeDmsg; this package only
// speaks the exchange protocol on top of it: one length-prefixed frame per
// JSON Envelope, request/response correlated by Envelope.ID.
package market

import (
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/internal/skydex-market/protocol"
	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// Dialer is the subset of *app.Client used to reach the market. *app.Client
// (and the skydex-client's app wrapper) satisfies it.
type Dialer interface {
	Dial(remote appnet.Addr) (net.Conn, error)
}

// Conn is a framed, request/response connection to a market. It is safe for
// concurrent use: each Do serializes a single request/response exchange, which
// matches the client's polling model (one outstanding request at a time).
type Conn struct {
	fc  *message.Conn
	mu  sync.Mutex
	rtt time.Duration // per-request read/write deadline; 0 = none
}

// NewConn wraps an established net.Conn with exchange framing. Use this
// directly in tests over net.Pipe; production code uses Dial.
func NewConn(c net.Conn) *Conn {
	return &Conn{fc: message.NewConn(c), rtt: 30 * time.Second}
}

// Dial opens a dmsg app connection to the market at pk:port and wraps it.
func Dial(d Dialer, pk cipher.PubKey, port protocolPort) (*Conn, error) {
	raw, err := d.Dial(appnet.Addr{
		Net:    appnet.TypeDmsg,
		PubKey: pk,
		Port:   port,
	})
	if err != nil {
		return nil, fmt.Errorf("dial market: %w", err)
	}
	return NewConn(raw), nil
}

// Close closes the underlying connection.
func (c *Conn) Close() error { return c.fc.Close() }

// Do sends a request of the given type carrying data and returns the market's
// response envelope. A transport error is returned as err; an application-level
// failure is returned as a non-error response with resp.IsError() == true.
func (c *Conn) Do(msgType string, data any) (protocol.Envelope, error) {
	req, err := protocol.NewRequest(msgType, data)
	if err != nil {
		return protocol.Envelope{}, err
	}
	payload, err := req.Marshal()
	if err != nil {
		return protocol.Envelope{}, err
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	if c.rtt > 0 {
		// Best-effort deadline so a dead transport can't pin the caller.
		_ = c.fc.SetDeadline(time.Now().Add(c.rtt)) //nolint:errcheck
		defer c.fc.SetDeadline(time.Time{})         //nolint:errcheck
	}

	if err := c.fc.WriteFrame(payload); err != nil {
		return protocol.Envelope{}, fmt.Errorf("write request: %w", err)
	}
	respPayload, err := c.fc.ReadFrame()
	if err != nil {
		return protocol.Envelope{}, fmt.Errorf("read response: %w", err)
	}
	resp, err := protocol.Unmarshal(respPayload)
	if err != nil {
		return protocol.Envelope{}, fmt.Errorf("decode response: %w", err)
	}
	if resp.ID != req.ID {
		return protocol.Envelope{}, fmt.Errorf("response id mismatch: got %q want %q", resp.ID, req.ID)
	}
	return resp, nil
}

// protocolPort aliases the market listen port type so callers don't import
// routing just to call Dial.
type protocolPort = protocol.Port
