// Package dm pkg/skychat/dm/parked.go c3-app-chat
package dm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/skychat/history"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

// NetworkParked is the network of a message that went through a dmsg-async
// inbox host instead of a live conn.
const NetworkParked appnet.Type = "dmsg-async"

// Parker leaves messages for an offline peer on the peer's dmsg-async inbox
// (pkg/dmsg/dmsgasync) and fetches the ones that wait for this visor.
type Parker interface {
	// Park leaves frame for pk and returns the host that took it. ref names
	// the message across retries.
	Park(ctx context.Context, pk cipher.PubKey, frame []byte, ref string) (cipher.PubKey, error)
	// Unpark returns the messages parked for this visor without removing them.
	Unpark(ctx context.Context) ([]Parked, error)
	// Ack removes parked messages.
	Ack(ctx context.Context, done []Parked) error
}

// Parked is a frame that waited for this visor on an inbox host. From is the
// key that signed it and At is when the host took it.
type Parked struct {
	ID    string
	Host  cipher.PubKey
	From  cipher.PubKey
	Frame []byte
	At    time.Time
}

const (
	// unparkInterval is how often parked messages are fetched while online.
	unparkInterval = 2 * time.Minute
	// parkedAckTimeout bounds the chat-ack sent back for a parked message.
	parkedAckTimeout = 30 * time.Second
	// seenWindow is how far back history is checked for a repeat.
	seenWindow = 500
)

// parkRef names one message to one peer, so a retried park is stored once.
func parkRef(id string, pk cipher.PubKey) string {
	h := sha256.Sum256(append([]byte(id+"/"), pk[:]...))
	return hex.EncodeToString(h[:16])
}

// parkSend parks a send no network delivered, when a Parker is set and the
// message has an id. It returns sendErr when the message is not parked.
func (c *Controller) parkSend(ctx context.Context, pk cipher.PubKey, wire []byte, text, replyTo string, res *SendResult, sendErr error) error {
	if c.cfg.Parker == nil || res.ID == "" {
		return sendErr
	}
	host, ok := c.park(ctx, pk, wire, res.ID)
	if !ok {
		return sendErr
	}
	res.Parked, res.ParkHost, res.Network = true, host, NetworkParked
	c.recordOut(pk, text, res.ID, replyTo, NetworkParked)
	return nil
}

// park leaves wire, the message id, for pk on its inbox. A message the peer
// got after all is dropped there as a repeat.
func (c *Controller) park(ctx context.Context, pk cipher.PubKey, wire []byte, id string) (cipher.PubKey, bool) {
	if c.cfg.Parker == nil || id == "" {
		return cipher.PubKey{}, false
	}
	host, err := c.cfg.Parker.Park(ctx, pk, wire, parkRef(id, pk))
	if err != nil {
		c.log("skychat/dm: not parked for %s: %v", pk.Hex(), err)
		return cipher.PubKey{}, false
	}
	c.bump(func(s *Stats) { s.OutboundParked++; s.LastTxAt = time.Now().UTC() })
	return host, true
}

// recordOut persists and surfaces a message this visor sent.
func (c *Controller) recordOut(pk cipher.PubKey, text, id, replyTo string, network appnet.Type) {
	c.persist(history.Message{
		Peer: pk.Hex(), Outgoing: true, Text: text,
		Timestamp: time.Now().UTC(), ID: id, ReplyTo: replyTo,
	})
	c.emit(Event{
		ID: id, Dir: "out", Peer: pk.Hex(), Network: string(network),
		Text: text, ReplyTo: replyTo, TS: time.Now().UTC(),
	})
}

// RunParked files the messages parked for this visor now and every
// unparkInterval until ctx ends.
func (c *Controller) RunParked(ctx context.Context) {
	if c.cfg.Parker == nil {
		return
	}
	t := time.NewTicker(unparkInterval)
	defer t.Stop()
	last := ""
	for {
		// A visor that does not serve the chat calls fails every fetch alike.
		if _, err := c.unpark(ctx); err != nil && err.Error() != last {
			c.log("skychat/dm: fetch parked messages: %v", err)
			last = err.Error()
		} else if err == nil {
			last = ""
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Unpark takes the messages parked for this visor into their conversations
// as if they had arrived live, removes them from their hosts, and reports
// how many surfaced. A message already in history is a repeat and only
// removed.
func (c *Controller) Unpark(ctx context.Context) int {
	n, err := c.unpark(ctx)
	if err != nil {
		c.log("skychat/dm: fetch parked messages: %v", err)
	}
	return n
}

func (c *Controller) unpark(ctx context.Context) (int, error) {
	if c.cfg.Parker == nil {
		return 0, nil
	}
	list, err := c.cfg.Parker.Unpark(ctx)
	if err != nil {
		return 0, err
	}
	filed := 0
	for _, p := range list {
		if c.seen(p.From, p.Frame) {
			continue
		}
		peer := p.From
		ack := func(b []byte) error {
			go func() {
				actx, cancel := context.WithTimeout(context.Background(), parkedAckTimeout)
				defer cancel()
				if err := c.SendRaw(actx, peer, b); err != nil {
					c.log("skychat/dm: ack for a parked message to %s: %v", peer.Hex(), err)
				}
			}()
			return nil
		}
		if c.handleFrame(peer, string(NetworkParked), p.Frame, p.At.UTC(), ack) {
			filed++
			c.bump(func(s *Stats) { s.InboundParked++ })
		}
	}
	if len(list) > 0 {
		if err := c.cfg.Parker.Ack(ctx, list); err != nil {
			c.log("skychat/dm: remove parked messages: %v", err)
		}
	}
	return filed, nil
}

// seen reports whether the chat message in frame is already in peer's history.
func (c *Controller) seen(peer cipher.PubKey, frame []byte) bool {
	env, ok := message.ParseEnvelope(frame)
	if !ok || env.Type != message.TypeMsg || env.ID == "" || c.cfg.Store == nil {
		return false
	}
	msgs, err := c.cfg.Store.ListByPeer(peer.Hex(), seenWindow)
	if err != nil {
		return false
	}
	for _, m := range msgs {
		if m.ID == env.ID && !m.Outgoing {
			return true
		}
	}
	return false
}

// SendRawAcked sends payload like SendRaw and, with a Parker set, waits up to
// wait for the peer's chat-ack naming id. A write to a peer whose app is down
// can succeed, so with no ack it parks the frame for pk, and the peer's
// controller offers it to PreHandleFrame when it fetches. It reports whether
// the frame was parked.
func (c *Controller) SendRawAcked(ctx context.Context, pk cipher.PubKey, payload []byte, id string, wait time.Duration) (bool, error) {
	if c.cfg.Parker == nil || id == "" {
		return false, c.SendRaw(ctx, pk, payload)
	}
	acked, unreg := c.registerAck(id)
	defer unreg()
	err := c.SendRaw(ctx, pk, payload)
	if err == nil {
		t := time.NewTimer(wait)
		defer t.Stop()
		select {
		case <-acked:
			return false, nil
		case <-t.C:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
	if _, ok := c.park(ctx, pk, payload, id); ok {
		return true, nil
	}
	return false, err
}

// ParkText leaves a chat message with text and id for pk without trying a
// network first, and without recording it here. It reports whether it parked.
func (c *Controller) ParkText(ctx context.Context, pk cipher.PubKey, text, id string) bool {
	env := message.Envelope{Type: message.TypeMsg, ID: id, Body: text}
	wire, err := env.Marshal()
	if err != nil {
		return false
	}
	_, ok := c.park(ctx, pk, wire, id)
	return ok
}

// ParkFrame leaves a raw control frame with id for pk, as ParkText does a
// chat message. The peer offers it to PreHandleFrame when it fetches.
func (c *Controller) ParkFrame(ctx context.Context, pk cipher.PubKey, frame []byte, id string) bool {
	_, ok := c.park(ctx, pk, frame, id)
	return ok
}
