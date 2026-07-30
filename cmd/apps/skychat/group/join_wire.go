// Package group cmd/apps/skychat/group/join_wire.go c4-app-chat
// the two halves of the join round trip on the relay listener:
// handleJoinRequest (group side) and RequestJoin (requester side).
//
// Split from join.go so that file stays pure types + codec, testable
// without standing up a transport — the same separation gossip.go keeps
// from roster_reconcile.go.
package group

import (
	"context"
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

// handleJoinRequest is the group side of the round trip. Runs on the
// owner's relay listener goroutine for one accepted stream.
//
// Order of operations is deliberate:
//
//  1. Decode, so a malformed frame costs nothing.
//  2. Resolve identity from the AUTHENTICATED transport (requesterPK),
//     because everything downstream is an authorization decision and
//     authorizing a self-asserted PK would be meaningless.
//  3. Check the ban list before consulting the handler, so a banned PK
//     can never re-enter an open group and never lands on an admin's
//     approval queue — being banned should not also mean being able to
//     spam the moderation UI.
//  4. Delegate the policy decision to the Manager's handler.
//  5. Write the answer back on the same stream.
//
// Every rejection path still writes a response. Silence would be
// indistinguishable from an unpatched owner, and the requester would
// retry on a timer forever against a group that has already refused it.
func (s *Session) handleJoinRequest(c net.Conn, payload []byte) {
	req, err := decodeJoinRequest(payload)
	if err != nil {
		s.log.WithError(err).Debug("group: join: undecodable request frame")
		return
	}
	if req.GroupID != s.cfg.Record.ID {
		s.log.WithField("want", s.cfg.Record.ID).WithField("got", req.GroupID).
			Debug("group: join: request for a different group")
		return
	}
	pk, err := requesterPK(c, req)
	if err != nil {
		s.log.WithError(err).Warn("group: join: refusing unauthenticated request")
		return
	}

	s.membersMu.RLock()
	handler := s.onJoinRequest
	isBanned := s.isBannedLocked(pk)
	name := s.cfg.Record.Name
	s.membersMu.RUnlock()

	resp := JoinResponseMsg{GroupID: s.cfg.Record.ID, Name: name}
	switch {
	case isBanned:
		resp.Status = JoinStatusBanned
		resp.Reason = "this public key is banned from the group"
	case handler == nil:
		// No Manager owns this session — the native-TCP standalone path
		// builds Sessions directly, with membership fixed by flags. It
		// has no store to queue a request in and no policy to apply, so
		// refuse rather than pretend.
		resp.Status = JoinStatusDenied
		resp.Reason = "this group is not accepting join requests"
	default:
		resp = handler(JoinRequest{
			GroupID: s.cfg.Record.ID,
			PK:      pk,
			Note:    req.Note,
			AskedAt: time.Now().UTC(),
			Status:  JoinStatusPending,
		})
		resp.GroupID = s.cfg.Record.ID
		if resp.Name == "" {
			resp.Name = name
		}
	}

	body, err := encodeJoinResponse(resp)
	if err != nil {
		s.log.WithError(err).Debug("group: join: encode response")
		return
	}
	if err := message.WriteFrame(c, body); err != nil {
		s.log.WithError(err).Debug("group: join: write response")
		return
	}
	s.log.WithField("requester", pk.Hex()).WithField("status", string(resp.Status)).
		Info("group: join: answered join request")
}

// joinCandidateStagger is how long the fan-out gives one candidate to
// answer before also asking the next.
//
// The point is to ask as few admins as possible while never letting one
// dead admin hold up a join. A healthy relay answers in tens of
// milliseconds, so in the normal case the founder wins before the second
// dial is ever attempted and the group sees exactly the single request it
// saw before this existed. Only a candidate that is slow or gone costs
// the group a second (and third) queue entry, which is the trade the
// whole feature is for.
const joinCandidateStagger = 2 * time.Second

// joinAttempt is one candidate's outcome inside the fan-out.
type joinAttempt struct {
	pk   cipher.PubKey
	resp JoinResponseMsg
	err  error
}

// SendJoinRequestAny asks every candidate admin, staggered, and returns
// the first answer that is an actual decision about the requester.
//
// Answers that are about the RESPONDER rather than the requester
// (JoinStatusUnavailable — demoted, or doesn't hold the group) and
// candidates that don't answer at all are passed over: the next name gets
// its turn. A group is joinable as long as one admin in the invite is
// alive and still authorized.
//
// Error precedence when nobody decides:
//
//   - ErrJoinNoResponse if any candidate took the frame and stayed silent.
//     That is the fingerprint of a build predating admission control, and
//     the caller's fallback to the legacy direct-subscribe join is the
//     right response — it has to outrank the others or a group with one
//     old founder and one unreachable admin would stop working.
//   - ErrJoinNoAdmin if some candidate answered "not mine to rule on".
//     The group exists and is reachable; nobody present can admit.
//   - otherwise the first transport error, verbatim.
func SendJoinRequestAny(ctx context.Context, dmsgC *dmsg.Client, groupID string, candidates []cipher.PubKey, port uint16, requester cipher.PubKey, note string) (JoinResponseMsg, error) {
	switch len(candidates) {
	case 0:
		return JoinResponseMsg{}, ErrJoinNoAdmin
	case 1:
		// No fan-out, no stagger, no goroutine — byte-for-byte the
		// pre-multi-admin path, which is what every link minted by an
		// older build resolves to.
		return SendJoinRequest(ctx, dmsgC, groupID, candidates[0], port, requester, note)
	}
	if ctx == nil {
		ctx = context.Background()
	}
	// Cancel on return so the candidates we didn't wait for stop dialing
	// as soon as one of them has answered.
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Buffered for every candidate: a goroutine whose answer arrives
	// after we've returned must not block on the send.
	results := make(chan joinAttempt, len(candidates))
	for i, pk := range candidates {
		go func(i int, pk cipher.PubKey) {
			if i > 0 {
				select {
				case <-ctx.Done():
					results <- joinAttempt{pk: pk, err: ctx.Err()}
					return
				case <-time.After(time.Duration(i) * joinCandidateStagger):
				}
			}
			resp, err := SendJoinRequest(ctx, dmsgC, groupID, pk, port, requester, note)
			results <- joinAttempt{pk: pk, resp: resp, err: err}
		}(i, pk)
	}

	var (
		silent      bool   // some candidate accepted the frame and never answered
		unavailable string // some candidate answered "ask another admin", with which one
		firstErr    error
	)
	for range candidates {
		att := <-results
		switch {
		case att.err != nil:
			if errors.Is(att.err, ErrJoinNoResponse) {
				silent = true
			}
			if firstErr == nil {
				firstErr = att.err
			}
		case att.resp.Status == JoinStatusUnavailable:
			unavailable = fmt.Sprintf("%s: %s", att.pk, att.resp.Reason)
		case att.resp.Status.IsDecision():
			return att.resp, nil
		default:
			// Unknown status; decodeJoinResponse already rejects these,
			// so this is unreachable in practice. Treat as "no decision".
			unavailable = fmt.Sprintf("%s: unknown status %q", att.pk, att.resp.Status)
		}
	}

	switch {
	case silent:
		return JoinResponseMsg{}, fmt.Errorf("%w (asked %d admins)", ErrJoinNoResponse, len(candidates))
	case unavailable != "":
		return JoinResponseMsg{}, fmt.Errorf("%w: %s", ErrJoinNoAdmin, unavailable)
	case firstErr != nil:
		return JoinResponseMsg{}, firstErr
	default:
		return JoinResponseMsg{}, ErrJoinNoAdmin
	}
}

// SendJoinRequest is the requester side: dial the group's relay listener,
// write a join request, read the answer.
//
// Transport preference mirrors SubmitToOwner — skynet first so groups
// work over arbitrary skywire transports, dmsg as the bootstrap and
// recovery fallback. Unlike SubmitToOwner this is called on a session
// that may not exist yet, so it takes the coordinates explicitly rather
// than reading them off a Record.
//
// A nil dmsg client with no reachable skynet networker yields
// ErrJoinNoResponse rather than a nil-pointer panic, because this is
// reachable from the standalone build where DmsgC is legitimately nil.
func SendJoinRequest(ctx context.Context, dmsgC *dmsg.Client, groupID string, ownerPK cipher.PubKey, port uint16, requester cipher.PubKey, note string) (JoinResponseMsg, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := NewJoinRequest(groupID, requester, note)
	body, err := encodeJoinRequest(req)
	if err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: SendJoinRequest: encode: %w", err)
	}
	relayPort := port + relayPortOffset

	skyAddr := appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: ownerPK,
		Port:   routing.Port(relayPort),
	}
	if conn, dialErr := dialSkynetRelay(ctx, skyAddr); dialErr == nil {
		resp, rtErr := joinRoundTrip(conn, body)
		_ = conn.Close() //nolint:errcheck
		if rtErr == nil {
			return resp, nil
		}
		// Fall through to dmsg. A skynet round trip that reached a live
		// listener but got no answer is the "owner runs an older build"
		// case — but it is equally consistent with a half-open route, so
		// retrying on dmsg costs one dial and removes the ambiguity.
	}

	if dmsgC == nil {
		return JoinResponseMsg{}, ErrJoinNoResponse
	}
	dialCtx, cancel := context.WithTimeout(ctx, joinResponseReadTimeout)
	defer cancel()
	stream, err := dmsgC.DialStream(dialCtx, dmsg.Addr{PK: ownerPK, Port: relayPort})
	if err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: SendJoinRequest: dial group relay: %w", err)
	}
	defer stream.Close() //nolint:errcheck
	resp, err := joinRoundTrip(stream, body)
	if err != nil {
		return JoinResponseMsg{}, err
	}
	return resp, nil
}

// joinRoundTrip writes the request frame and reads the response frame
// under joinResponseReadTimeout.
//
// A read that fails after a successful write is reported as
// ErrJoinNoResponse: the distinguishing symptom of an owner whose
// handleRelay predates frame-kind dispatch, which drops the frame as an
// empty-sender chat message and never replies.
func joinRoundTrip(c net.Conn, body []byte) (JoinResponseMsg, error) {
	if err := c.SetWriteDeadline(time.Now().Add(joinResponseReadTimeout)); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: SendJoinRequest: set write deadline: %w", err)
	}
	if err := message.WriteFrame(c, body); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: SendJoinRequest: write: %w", err)
	}
	if err := c.SetReadDeadline(time.Now().Add(joinResponseReadTimeout)); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: SendJoinRequest: set read deadline: %w", err)
	}
	raw, err := message.ReadFrame(c)
	if err != nil {
		return JoinResponseMsg{}, fmt.Errorf("%w: %v", ErrJoinNoResponse, err)
	}
	resp, err := decodeJoinResponse(raw)
	if err != nil {
		return JoinResponseMsg{}, err
	}
	return resp, nil
}

// errIsTerminalJoin reports whether a RequestJoin error means "stop
// asking". Transport failures are retryable; an explicit refusal is not.
func errIsTerminalJoin(err error) bool {
	return errors.Is(err, ErrJoinDenied) || errors.Is(err, ErrJoinBanned)
}
