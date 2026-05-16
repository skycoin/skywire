package node

import (
	"errors"
	"fmt"
	"time"

	"github.com/skycoin/skywire/pkg/cxo/node/msg"
)

func (c *Conn) handshake() error {
	c.n.Debugf(ConnHskPin, "[%s] handshake", c.String())

	if c.incoming == true { //nolint:staticcheck
		return c.acceptHandshake()
	} else {
		return c.performHandshake()
	}
}

func (c *Conn) performHandshake() error {
	c.n.Debugf(ConnHskPin, "[%s] perform  handshake", c.String())

	// Send Syn message.
	var (
		seq = c.nextSeq()

		syn = &msg.Syn{
			Protocol: msg.Version,
			NodeID:   c.n.idpk,
		}
	)
	if err := c.sendMsg(seq, 0, syn); err != nil { //nolint:errcheck,gosec
		return err
	}

	// Check specified response timeout.
	// Handshake uses connection-level timeout; a dedicated parameter could give finer control.
	var (
		rt time.Duration
		tm *time.Timer
	)
	if c.IsTCP() == true { //nolint:staticcheck
		rt = c.n.config.TCP.ResponseTimeout
	} else {
		rt = c.n.config.UDP.ResponseTimeout
	}
	if rt > 0 {
		tm = time.NewTimer(rt)
		defer tm.Stop()
	}

	// Wait for response message with timeout.
	select {
	case raw, ok := <-c.GetChanIn():
		if !ok {
			return errors.New("factory.Connection closed")
		}

		// Decode message and check rseq.
		_, rseq, m, err := c.decodeRaw(raw)
		if err != nil {
			return err
		}
		if rseq != seq {
			return errors.New("invlaid resposne: wrong seq")
		}

		switch x := m.(type) {
		case *msg.Ack:
			// A stale Conn for this peer (whose run() loop wedged or whose
			// receiveMsg is blocked on a half-dead transport) silently
			// rejects every rejoin attempt with ErrAlreadyHaveConnection.
			// On a fresh SYN from the same peer, evict the prior record:
			// the new conn is the source of truth and the old will tear
			// itself down via Close → receiveMsg unblock → removeConn.
			if existing, ok := c.n.hasPeer(x.NodeID); ok {
				c.n.evictStalePeer(existing, x.NodeID)
			}
			c.peerID = x.NodeID

		case *msg.Err:
			return errors.New(x.Err)

		default:
			return fmt.Errorf("invalid response type: %T", m)
		}

	case <-tm.C:
		return ErrTimeout

	case <-c.closeq:
		return ErrClosed
	}

	return nil
}

func (c *Conn) acceptHandshake() (err error) {
	c.n.Debugf(ConnHskPin, "[%s] accept handshake", c.String())

	// Check specified response timeout.
	// Handshake uses connection-level timeout; a dedicated parameter could give finer control.
	var (
		rt time.Duration
		tm *time.Timer
	)
	if c.IsTCP() == true { //nolint:staticcheck
		rt = c.n.config.TCP.ResponseTimeout
	} else {
		rt = c.n.config.UDP.ResponseTimeout
	}
	if rt > 0 {
		tm = time.NewTimer(rt)
		defer tm.Stop()
	}

	// Wait for incoming message with timeout.
	select {
	case raw, ok := <-c.GetChanIn():
		if !ok {
			return errors.New("factory.Connection closed")
		}

		// Decode message and check its type.
		seq, _, m, err := c.decodeRaw(raw)
		if err != nil {
			return err
		}
		syn, ok := m.(*msg.Syn)
		if !ok {
			return fmt.Errorf("invalid message type")
		}

		// Check protocol version.
		if syn.Protocol != msg.Version {
			var (
				err = fmt.Errorf("incompatible protocol version: %d, want %d",
					syn.Protocol, msg.Version)

				errMsg = &msg.Err{
					Err: err.Error(),
				}
			)
			if sendErr := c.sendMsg(c.nextSeq(), seq, errMsg); sendErr != nil { //nolint:errcheck,gosec
				c.n.Error(sendErr, "failed to send err message")
			}

			return err
		}

		// A stale Conn for this peer (whose run() loop wedged or whose
		// receiveMsg is blocked on a half-dead transport) silently
		// rejects every rejoin attempt with ErrAlreadyHaveConnection.
		// On a fresh SYN from the same peer, evict the prior record:
		// the new conn is the source of truth and the old will tear
		// itself down via Close → receiveMsg unblock → removeConn.
		if existing, ok := c.n.hasPeer(syn.NodeID); ok {
			c.n.evictStalePeer(existing, syn.NodeID)
		}

		// Send Ack message.
		ack := &msg.Ack{
			NodeID: c.n.idpk,
		}
		if err := c.sendMsg(c.nextSeq(), seq, ack); err != nil { //nolint:errcheck,gosec
			return fmt.Errorf("failed to send ack message: %s", err)
		}

		// Handshake is complete.
		c.peerID = syn.NodeID

	case <-tm.C:
		return ErrTimeout

	case <-c.closeq:
		return ErrClosed
	}

	return nil
}
