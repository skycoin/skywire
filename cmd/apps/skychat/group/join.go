// Package group cmd/apps/skychat/group/join.go c4-app-chat
// the join-request protocol: how a PK holding an invite actually gets
// an allowlist seat.
//
// # Why this exists
//
// Before admission control, an invite link was not sufficient to join
// anything. A joiner persisted a member Record and dialed the owner's
// CXO publisher, whose SubscriberAllowlist is exactly Record.Members —
// so unless an admin had already run AddMember for that PK, the
// subscribe was refused and Join failed. The only working flow was
// out-of-band: hand the admin your PK, have them add you, then use the
// link. The link's whole promise was unbacked.
//
// This file adds the missing step. The joiner asks; the group answers.
//
// # Wire
//
// Requests ride the owner's EXISTING relay listener at
// Record.Port+relayPortOffset — the same dmsg/skynet listener members
// already dial to submit messages — so no new port, listener, or
// discovery is introduced. handleRelay dispatches on the envelope's
// "kind" field.
//
// Backward compatibility is by careful field naming rather than by
// version negotiation: JoinRequestMsg deliberately has NO "sender_pk"
// key. An unpatched owner unmarshals the frame into RelayMessage, gets
// the zero PubKey, and takes its existing "relay rejected: empty
// sender PK" path — logged and dropped. It cannot mistake a join
// request for a chat message and publish an empty leaf. The requester
// simply reads no response and times out, which the manager surfaces
// as "this group's owner is running an older build".
//
// # Identity
//
// The requester's PK is taken from the AUTHENTICATED TRANSPORT, never
// from the envelope. Every transport under this listener completes a
// Noise handshake that proves possession of the peer's secret key
// (dmsg KK, skynet), so conn.RemoteAddr() is a cryptographic claim
// while a JSON field is just an assertion. requesterPK cross-checks
// the envelope against the transport and refuses on mismatch, so a
// mismatched pair is a hard error rather than a silent preference.
//
// Without this, anyone could submit join requests naming a third
// party: junk on every admin's approval queue at best, and in an open
// group, unsolicited membership for PKs that never asked.
package group

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sort"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
)

// Relay frame kinds. Chat submissions (RelayMessage) carry no "kind"
// at all, so an empty kind means "legacy chat frame" and keeps the
// pre-admission wire working untouched.
const (
	frameKindJoinRequest  = "join_request"
	frameKindJoinResponse = "join_response"
)

// relayFrameProbe sniffs the discriminator without committing to a
// concrete envelope type. Decoding twice costs one extra pass over a
// frame that is at most a few hundred bytes.
type relayFrameProbe struct {
	Kind string `json:"kind"`
}

// JoinStatus is the outcome the group reports for a join request.
type JoinStatus string

const (
	// JoinStatusAdmitted — the PK now holds an allowlist seat and may
	// subscribe. For an encrypted group the response also carries the
	// AES key.
	JoinStatusAdmitted JoinStatus = "admitted"

	// JoinStatusPending — queued for an admin. The requester re-asks
	// on a timer until the answer changes.
	JoinStatusPending JoinStatus = "pending"

	// JoinStatusDenied — an admin refused. Terminal; the requester
	// stops asking.
	JoinStatusDenied JoinStatus = "denied"

	// JoinStatusBanned — the PK is barred. Terminal, and deliberately
	// distinguished from denied so the UI can say which happened.
	JoinStatusBanned JoinStatus = "banned"
)

// IsTerminal reports whether the requester should stop re-asking.
func (s JoinStatus) IsTerminal() bool {
	return s == JoinStatusAdmitted || s == JoinStatusDenied || s == JoinStatusBanned
}

// JoinRequestMsg is the frame a prospective member writes to the
// group's relay listener.
//
// The PK field is named requester_pk, NOT sender_pk — see the package
// comment; that naming is what makes an unpatched owner reject the
// frame cleanly instead of publishing it as chat. It is carried at all
// only as a cross-check against the transport identity.
type JoinRequestMsg struct {
	Kind        string        `json:"kind"`
	GroupID     string        `json:"group_id"`
	RequesterPK cipher.PubKey `json:"requester_pk"`

	// Note is optional free text ("hi, I'm from the ops team") shown
	// to the approving admin. Bounded by maxJoinNoteLen on receipt so
	// a hostile requester can't push a megabyte into the admin's
	// pending list.
	Note string `json:"note,omitempty"`

	TS time.Time `json:"ts"`
}

// JoinResponseMsg is the group's answer, written back on the same
// stream. On admission it doubles as the roster + key bootstrap, so
// the joiner converges immediately instead of waiting for the first
// gossip leaf to arrive.
type JoinResponseMsg struct {
	Kind    string     `json:"kind"`
	GroupID string     `json:"group_id"`
	Status  JoinStatus `json:"status"`

	// Name lets the joiner display the real group name rather than
	// whatever the invite link said, which may be stale.
	Name string `json:"name,omitempty"`

	// AESKey is the group key, present iff Status is admitted AND the
	// group is encrypted. This is the delivery channel that replaced
	// putting the key in the invite link: a forwarded link is inert
	// until an admin approves, and the owner stays a live distributor
	// so the key can be rotated later rather than being frozen into
	// links already in circulation.
	AESKey []byte `json:"aes_key,omitempty"`

	// Members / Admins seed the joiner's roster. Sent on admission
	// only — a pending or refused requester learns nothing about who
	// is in the group.
	Members []cipher.PubKey `json:"members,omitempty"`
	Admins  []cipher.PubKey `json:"admins,omitempty"`

	// Muted / ReadOnly seed the moderation state so a joiner who is
	// admitted into a quieted room sees the correct composer state
	// immediately rather than after the first mod/ leaf lands.
	Muted    []cipher.PubKey `json:"muted,omitempty"`
	ReadOnly bool            `json:"read_only,omitempty"`

	// Reason is operator-facing text for the non-admitted cases.
	Reason string `json:"reason,omitempty"`
}

// maxJoinNoteLen bounds the free-text note. Long enough for a sentence
// of context, short enough that a thousand queued requests stay a
// trivial amount of storage.
const maxJoinNoteLen = 280

// joinResponseReadTimeout caps how long a requester waits for the
// group's answer. More generous than relayAckReadTimeout because the
// owner side may do a store write plus a roster publish before
// replying, and because a join is a deliberate user action where a
// spurious timeout is more annoying than a slow success.
const joinResponseReadTimeout = 15 * time.Second

// Sentinel errors for the requester side.
var (
	// ErrJoinNoResponse means the group's relay accepted our frame but
	// never answered — the signature of an owner running a build that
	// predates admission control.
	ErrJoinNoResponse = errors.New("group: join: no response from group (owner may be running an older build)")

	// ErrJoinDenied is returned when an admin refused the request.
	ErrJoinDenied = errors.New("group: join: request denied")

	// ErrJoinBanned is returned when the PK is barred from the group.
	ErrJoinBanned = errors.New("group: join: banned from this group")

	// ErrJoinIdentityMismatch means the envelope's claimed PK did not
	// match the authenticated transport identity.
	ErrJoinIdentityMismatch = errors.New("group: join: requester PK does not match authenticated connection")

	// ErrJoinNoTransportIdentity means the connection carried no
	// authenticated peer key, so the requester could not be
	// identified. Only reachable on a transport that isn't dmsg or
	// skynet, which the relay listener never binds.
	ErrJoinNoTransportIdentity = errors.New("group: join: connection has no authenticated peer identity")
)

// NewJoinRequest builds a well-formed request frame.
func NewJoinRequest(groupID string, requester cipher.PubKey, note string) JoinRequestMsg {
	if len(note) > maxJoinNoteLen {
		note = note[:maxJoinNoteLen]
	}
	return JoinRequestMsg{
		Kind:        frameKindJoinRequest,
		GroupID:     groupID,
		RequesterPK: requester,
		Note:        note,
		TS:          time.Now().UTC(),
	}
}

// remotePKOf extracts the cryptographically authenticated peer key
// from a relay connection. Both transports the relay listener binds
// expose the Noise-verified peer key through their address type;
// anything else yields false and the caller refuses the request rather
// than guessing.
func remotePKOf(c net.Conn) (cipher.PubKey, bool) {
	if c == nil {
		return cipher.PubKey{}, false
	}
	switch a := c.RemoteAddr().(type) {
	case dmsg.Addr:
		return a.PK, a.PK != cipher.PubKey{}
	case *dmsg.Addr:
		if a == nil {
			return cipher.PubKey{}, false
		}
		return a.PK, a.PK != cipher.PubKey{}
	case appnet.Addr:
		return a.PubKey, a.PubKey != cipher.PubKey{}
	case *appnet.Addr:
		if a == nil {
			return cipher.PubKey{}, false
		}
		return a.PubKey, a.PubKey != cipher.PubKey{}
	default:
		return cipher.PubKey{}, false
	}
}

// requesterPK resolves the identity to attribute a join request to.
// The transport's authenticated key is the answer; the envelope's
// claim is only allowed to agree with it.
func requesterPK(c net.Conn, msg JoinRequestMsg) (cipher.PubKey, error) {
	pk, ok := remotePKOf(c)
	if !ok {
		return cipher.PubKey{}, ErrJoinNoTransportIdentity
	}
	if msg.RequesterPK != (cipher.PubKey{}) && msg.RequesterPK != pk {
		return cipher.PubKey{}, fmt.Errorf("%w: claimed %s, authenticated %s",
			ErrJoinIdentityMismatch, msg.RequesterPK, pk)
	}
	return pk, nil
}

// JoinRequestHandler decides one authenticated join request and
// returns the answer to write back. Installed on a Session by the
// Manager, which owns the admission policy, the roster mutation an
// admission implies, and the store the pending queue lives in.
//
// The session has already proven the requester's identity against the
// transport before this runs, so req.PK can be trusted. Implementations
// must not block for long: the requester is holding a stream open on
// joinResponseReadTimeout.
type JoinRequestHandler func(req JoinRequest) JoinResponseMsg

// JoinRequest is one entry in an admin's pending-approval queue. The
// persisted form of a JoinRequestMsg plus its decision state.
//
// Kept after a decision (rather than deleted) so an admin can see that
// they already refused a PK instead of re-litigating the same request
// every time it is re-sent, and so a denial can be reversed without
// waiting for the requester to ask again.
type JoinRequest struct {
	GroupID string        `json:"group_id"`
	PK      cipher.PubKey `json:"pk"`
	Note    string        `json:"note,omitempty"`
	AskedAt time.Time     `json:"asked_at"`

	// Status is pending until an admin rules. Approved requests are
	// recorded as admitted AND added to Members; the queue entry stays
	// as an audit trail.
	Status JoinStatus `json:"status"`

	DecidedAt time.Time     `json:"decided_at,omitempty"`
	DecidedBy cipher.PubKey `json:"decided_by,omitempty"`
}

// IsPending reports whether this request still awaits a decision.
func (r JoinRequest) IsPending() bool { return r.Status == JoinStatusPending }

// sortJoinRequests orders a queue newest-first, tie-broken by PK so the
// order is total and stable. Shared by both Store backends (bbolt
// iterates by key, the js map iterates arbitrarily) so an admin's queue
// reads the same on every platform.
func sortJoinRequests(reqs []JoinRequest) {
	sort.Slice(reqs, func(i, j int) bool {
		if !reqs[i].AskedAt.Equal(reqs[j].AskedAt) {
			return reqs[i].AskedAt.After(reqs[j].AskedAt)
		}
		return reqs[i].PK.Hex() < reqs[j].PK.Hex()
	})
}

// encodeJoinRequest / decodeJoinRequest / encodeJoinResponse /
// decodeJoinResponse wrap the JSON codec so the frame kind is set and
// checked in exactly one place.

func encodeJoinRequest(m JoinRequestMsg) ([]byte, error) {
	m.Kind = frameKindJoinRequest
	return json.Marshal(m)
}

func decodeJoinRequest(b []byte) (JoinRequestMsg, error) {
	var m JoinRequestMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return JoinRequestMsg{}, fmt.Errorf("group: decode join request: %w", err)
	}
	if m.Kind != frameKindJoinRequest {
		return JoinRequestMsg{}, fmt.Errorf("group: decode join request: unexpected kind %q", m.Kind)
	}
	if m.GroupID == "" {
		return JoinRequestMsg{}, errors.New("group: decode join request: empty group id")
	}
	if len(m.Note) > maxJoinNoteLen {
		m.Note = m.Note[:maxJoinNoteLen]
	}
	return m, nil
}

func encodeJoinResponse(m JoinResponseMsg) ([]byte, error) {
	m.Kind = frameKindJoinResponse
	return json.Marshal(m)
}

func decodeJoinResponse(b []byte) (JoinResponseMsg, error) {
	var m JoinResponseMsg
	if err := json.Unmarshal(b, &m); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: decode join response: %w", err)
	}
	if m.Kind != frameKindJoinResponse {
		return JoinResponseMsg{}, fmt.Errorf("group: decode join response: unexpected kind %q", m.Kind)
	}
	switch m.Status {
	case JoinStatusAdmitted, JoinStatusPending, JoinStatusDenied, JoinStatusBanned:
	default:
		return JoinResponseMsg{}, fmt.Errorf("group: decode join response: unknown status %q", m.Status)
	}
	return m, nil
}

// errForStatus maps a terminal non-admitted status onto its sentinel
// error, so callers can errors.Is rather than string-match.
func errForStatus(s JoinStatus, reason string) error {
	var base error
	switch s {
	case JoinStatusDenied:
		base = ErrJoinDenied
	case JoinStatusBanned:
		base = ErrJoinBanned
	default:
		return nil
	}
	if reason == "" {
		return base
	}
	return fmt.Errorf("%w: %s", base, reason)
}
