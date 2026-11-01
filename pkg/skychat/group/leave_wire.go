// Package group pkg/skychat/group/leave_wire.go c4-app-chat
// telling a group's admins that a member left.
//
// Leave used to be local only: the member closed its session and nobody
// heard, so every admin went on listing it. A member cannot drop itself
// from the roster, because roster changes are signed by an admin
// (roster_reconcile.go), so it asks one on the relay listener a join
// request uses. The asker is the authenticated transport key, so a leave
// is only ever for the asker. A build that predates the frame drops it
// (an unknown kind with no sender), which leaves the member listed, as
// before.
package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
)

const (
	frameKindLeave    = "leave"
	frameKindLeaveAck = "leave_ack"
)

// LeaveMsg is the frame a departing member writes to an admin's relay listener.
type LeaveMsg struct {
	Kind    string    `json:"kind"`
	GroupID string    `json:"group_id"`
	TS      time.Time `json:"ts"`
}

// LeaveAck answers a LeaveMsg. Removed is false when the responder could
// not take the member off the roster, for instance because it is no admin.
type LeaveAck struct {
	Kind    string `json:"kind"`
	Removed bool   `json:"removed"`
}

// LeaveHandler takes pk, which asked to leave, off the roster and reports
// whether it is off.
type LeaveHandler func(pk cipher.PubKey) bool

// ErrLeaveNotHeard means no admin took the member off the roster.
var ErrLeaveNotHeard = errors.New("group: no admin acknowledged the leave")

// SetLeaveHandler installs the callback that acts on a member's leave.
func (s *Session) SetLeaveHandler(h LeaveHandler) {
	s.membersMu.Lock()
	s.onLeave = h
	s.membersMu.Unlock()
}

// handleLeave is the admin side: authenticate the asker, remove it, and answer.
func (s *Session) handleLeave(c net.Conn, payload []byte) {
	var msg LeaveMsg
	if err := json.Unmarshal(payload, &msg); err != nil || msg.GroupID != s.cfg.Record.ID {
		return
	}
	pk, ok := remotePKOf(c)
	if !ok {
		s.log.Warn("group: leave: refusing unauthenticated request")
		return
	}
	s.membersMu.RLock()
	handler := s.onLeave
	s.membersMu.RUnlock()
	removed := handler != nil && handler(pk)
	body, err := json.Marshal(LeaveAck{Kind: frameKindLeaveAck, Removed: removed})
	if err != nil {
		return
	}
	if err := message.WriteFrame(c, body); err != nil {
		s.log.WithError(err).Debug("group: leave: write answer")
	}
	s.log.WithField("member", pk.Hex()).WithField("removed", removed).Info("group: leave: member left")
}

// AnnounceLeave asks each candidate admin in turn to take this visor off
// the roster, and stops at the first that does.
func AnnounceLeave(ctx context.Context, dmsgC *dmsg.Client, groupID string, candidates []cipher.PubKey, port uint16) error {
	body, err := json.Marshal(LeaveMsg{Kind: frameKindLeave, GroupID: groupID, TS: time.Now().UTC()})
	if err != nil {
		return err
	}
	err = ErrLeaveNotHeard
	for _, admin := range candidates {
		ack, rtErr := leaveRoundTrip(ctx, dmsgC, admin, port+relayPortOffset, body)
		if rtErr == nil && ack.Removed {
			return nil
		}
		if rtErr != nil {
			err = rtErr
		}
		if ctx.Err() != nil {
			break
		}
	}
	return err
}

func leaveRoundTrip(ctx context.Context, dmsgC *dmsg.Client, admin cipher.PubKey, relayPort uint16, body []byte) (LeaveAck, error) {
	conn, err := dialSkynetRelay(ctx, appnet.Addr{Net: appnet.TypeSkynet, PubKey: admin, Port: routing.Port(relayPort)})
	if err != nil {
		if dmsgC == nil {
			return LeaveAck{}, err
		}
		dialCtx, cancel := context.WithTimeout(ctx, joinResponseReadTimeout)
		defer cancel()
		if conn, err = dmsgC.DialStream(dialCtx, dmsg.Addr{PK: admin, Port: relayPort}); err != nil {
			return LeaveAck{}, fmt.Errorf("group: leave: dial %s: %w", admin, err)
		}
	}
	defer conn.Close()                                            //nolint:errcheck
	_ = conn.SetDeadline(time.Now().Add(joinResponseReadTimeout)) //nolint:errcheck
	if err := message.WriteFrame(conn, body); err != nil {
		return LeaveAck{}, fmt.Errorf("group: leave: write: %w", err)
	}
	raw, err := message.ReadFrame(conn)
	if err != nil {
		return LeaveAck{}, fmt.Errorf("group: leave: read answer: %w", err)
	}
	var ack LeaveAck
	if err := json.Unmarshal(raw, &ack); err != nil || ack.Kind != frameKindLeaveAck {
		return LeaveAck{}, errors.New("group: leave: unexpected answer")
	}
	return ack, nil
}
