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

	// JoinStatusUnavailable — "not my decision to make, ask someone
	// else". Answered by a visor that holds no roster authority on the
	// group, or no longer holds the group at all. It is NOT a refusal:
	// the requester moves to the next admin the invite named rather
	// than treating the group as closed.
	//
	// This status exists because a multi-admin invite means a joiner can
	// legitimately dial a PK that has since been demoted or has left. If
	// that answered "denied", one stale name in a link would sink a join
	// the group's other admins would happily have approved.
	//
	// Only ever sent to a requester that asked more than one PK — see
	// handleJoinRequest for why an older joiner never sees it.
	JoinStatusUnavailable JoinStatus = "unavailable"
)

// IsTerminal reports whether the requester should stop re-asking.
func (s JoinStatus) IsTerminal() bool {
	return s == JoinStatusAdmitted || s == JoinStatusDenied || s == JoinStatusBanned
}

// IsDecision reports whether this status is an answer about the
// requester ("you're in", "wait", "no") as opposed to an answer about
// the responder ("I can't rule on this"). The fan-out in
// SendJoinRequestAny keeps asking until it gets a decision.
func (s JoinStatus) IsDecision() bool {
	return s == JoinStatusAdmitted || s == JoinStatusPending ||
		s == JoinStatusDenied || s == JoinStatusBanned
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

	// KeyEpoch numbers the key above. Carried so a joiner starts out
	// agreeing with the group about which generation it holds — without
	// it a joiner would sit at epoch 0 and treat the group's next
	// rotation as if it were the first, which is harmless for reading but
	// makes the epoch meaningless as a diagnostic.
	//
	// Only the CURRENT key is handed over. A new member cannot read
	// history published under retired keys, which is the same forward-only
	// rule rotation itself follows: admission grants access from now on,
	// not retroactively.
	KeyEpoch uint64 `json:"key_epoch,omitempty"`

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

	// ErrJoinNoAdmin means every admin the invite named was reachable
	// but unable to rule (demoted, or no longer holds the group), or was
	// not reachable at all. Retryable: an admin coming back online is
	// all it takes, which is why the requester's record stays in
	// awaiting-approval rather than going terminal.
	ErrJoinNoAdmin = errors.New("group: join: no admin able to admit is reachable")
)

// maxAdmissionTargets caps how many PKs one join attempt will ever dial.
//
// Two reasons for a cap rather than "however many the link lists". An
// invite is attacker-supplied text, so an unbounded admin list is an
// unbounded fan-out of dials from whoever pastes it. And the attempt is
// bounded in time by its slowest candidate, so every extra name widens
// the worst case an operator waits on a join click — with the stagger in
// SendJoinRequestAny, four keeps that inside ~20s.
//
// Truncation is not a loss of authority: it only limits which admins a
// single attempt asks. The group's real admin set arrives with the
// admission response and converges through gossip afterwards.
const maxAdmissionTargets = 4

// admissionOrder builds the ordered candidate list a joiner walks when
// asking to be let in: the founder first, then everyone else in the
// order they were named, minus zero keys, duplicates, and self.
//
// Founder-first is deliberate. It keeps the single-dial latency of the
// pre-multi-admin path for the overwhelmingly common case (founder
// online, answers, done), and the founder is the one PK that can never
// be demoted — so it is the candidate least likely to answer "not my
// group". The rest of the list is what makes the founder's absence
// survivable rather than terminal.
//
// self is dropped because dialing our own relay listener would at best
// waste a round trip on an answer we could have computed locally, and an
// invite naming us as an admin is a normal thing to receive (we may have
// been an admin, lost the record, and been re-invited).
func admissionOrder(founder, self cipher.PubKey, more ...[]cipher.PubKey) []cipher.PubKey {
	out := make([]cipher.PubKey, 0, maxAdmissionTargets)
	seen := make(map[cipher.PubKey]struct{}, maxAdmissionTargets)
	add := func(pk cipher.PubKey) {
		if pk == (cipher.PubKey{}) || pk == self || len(out) >= maxAdmissionTargets {
			return
		}
		if _, dup := seen[pk]; dup {
			return
		}
		seen[pk] = struct{}{}
		out = append(out, pk)
	}
	add(founder)
	for _, list := range more {
		for _, pk := range list {
			add(pk)
		}
	}
	return out
}

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
	case JoinStatusAdmitted, JoinStatusPending, JoinStatusDenied, JoinStatusBanned, JoinStatusUnavailable:
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
