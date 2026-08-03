// Package group pkg/skychat/group/probe.go c4-app-chat
// the describe half of admission: "what is the group with this ID?".
//
// # Why this exists
//
// A group is reachable only if you know four things — its ID, its host,
// its feed port, and its mode — and three of those live nowhere but the
// invite link, because the port is allocated at random per group
// (defaultPortAlloc) and the mode is not derivable from anything public.
// That made the short address form (skychat://<pk>/<group-id>, see
// pkg/skychat/address) impossible to act on, and made "type a public key
// and let the app work out whether it's a person, a group or a channel"
// impossible to answer at all: those three are indistinguishable strings.
//
// A probe closes that gap with one round trip on a fixed per-visor port.
//
// # Shape
//
// Deliberately built out of the join round trip rather than beside it:
// same frame kinds, same codec, same identity check, same deadlines. A
// probe is a JoinRequestMsg with Probe set, answered with
// JoinStatusInfo. Nothing about it is new except the port it arrives on
// and the fact that it decides nothing.
//
// # What a probe does NOT do
//
// It never mutates: no roster change, no queue entry, no notification,
// no PoW demanded. That is what makes it safe to answer cheaply, and it
// is why the well-known port carries describes ONLY — every admission
// decision stays on the per-group listener at Record.Port+1, which the
// asker can reach once the probe has told it the port.
//
// # Enumeration
//
// A probe is answered only for an exact group ID, and a group ID is a
// v4 UUID: 122 bits. Knowing one is already a capability, so answering
// "yes, that is a private group, send a request" leaks nothing that
// holding the ID did not already imply. There is deliberately no "list
// your groups" form — that would turn one PK into a directory of every
// private group a visor hosts, which is exactly the thing the ID's
// entropy is protecting. A visor that WANTS to be listed can publish its
// addresses wherever it likes; that is a directory's job, not this port's.
//
// Two things are still withheld from a probe answer even for a group
// whose ID the asker holds: the roster (who is in it) and the key. Both
// ride the admission response, after a decision.
package group

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/app/appnet"
	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/dmsg/dmsg"
	"github.com/skycoin/skywire/pkg/routing"
	"github.com/skycoin/skywire/pkg/skychat/message"
	"github.com/skycoin/skywire/pkg/skyenv"
)

// ProbePort is the well-known dmsg/skynet port a visor answers group
// describes on. Aliased from skyenv so this package reads self-contained
// while the number stays in the one registry that the pair-feed and
// CXO-feed allocators check against.
const ProbePort = skyenv.SkychatGroupProbePort

// maxPriceHintLen bounds the free text a host may state about admission
// cost. Long enough for "5 SKY / month", short enough that it cannot be
// used to push a paragraph of copy into a dialog.
const maxPriceHintLen = 40

// probeTimeout caps one describe round trip. Shorter than
// joinResponseReadTimeout because a probe does no store write, no roster
// publish and no key wrap — it is a lookup, and it sits directly under a
// user watching a dialog for a spinner to stop.
const probeTimeout = 6 * time.Second

// ErrProbeNoSuchGroup means the host answered, and does not hold a group
// with that ID (or holds one it has left or deleted). Distinct from a
// transport failure: the address is wrong, not unreachable, so a caller
// should say "no such group" rather than "try again".
var ErrProbeNoSuchGroup = errors.New("group: probe: host does not hold that group")

// ErrProbeUnreachable means the host could not be reached, or answered
// nothing. The group may well exist — this is the retryable case, and
// also what an older host that does not serve ProbePort looks like.
var ErrProbeUnreachable = errors.New("group: probe: host did not answer")

// GroupDescriptor is what a probe learns about a group. Everything a
// caller needs to render a join affordance and then perform the join,
// and nothing that a non-member should not see.
type GroupDescriptor struct {
	// ID and HostPK are echoed back so a descriptor is self-describing
	// once it has been handed to a UI or an RPC caller.
	ID     string        `json:"id"`
	HostPK cipher.PubKey `json:"host_pk"`

	Name string `json:"name"`
	Kind Kind   `json:"kind"`
	Mode Mode   `json:"mode"`

	// Policy is what joining will involve: JoinOpen admits on request,
	// JoinApproval queues for an admin.
	Policy JoinPolicy `json:"policy"`

	// Port is the group's CXO feed port, needed to build the invite the
	// join path consumes.
	Port uint16 `json:"port"`

	// PoWBits is the proof of work a join request must carry, so the
	// joiner pays before it dials rather than being challenged.
	PoWBits uint8 `json:"pow_bits,omitempty"`

	// ReadOnly reports that posting is currently suspended for
	// non-admins. Separate from a channel: this one is reversible.
	ReadOnly bool `json:"read_only,omitempty"`

	// PriceHint is unverified display text about admission cost. See
	// JoinResponseMsg.PriceHint — never treat it as terms.
	PriceHint string `json:"price_hint,omitempty"`

	// Banned reports that the ASKING visor is barred from this group, so
	// a UI can say why rather than offering a join that will be refused.
	Banned bool `json:"banned,omitempty"`
}

// IsChannel reports whether the described group is a broadcast channel.
func (d GroupDescriptor) IsChannel() bool { return d.Kind == KindChannel }

// Invite rebuilds the invite this descriptor is equivalent to, so the
// existing join path — which speaks Invite and nothing else — can be
// driven from a short address.
//
// The AES key is absent by construction, which is correct: an encrypted
// group's key travels in the admission response, after a decision. That
// is the same shape a freshly minted link has (see Manager.BuildInvite),
// so joining from an address and joining from a link converge on
// identical code.
//
// Admins are left empty: a probe is answered by ONE host, and naming
// further admins would be repeating an unverified claim to ourselves.
// The real admin set arrives with the admission response.
func (d GroupDescriptor) Invite() Invite {
	return Invite{
		ID:      d.ID,
		Name:    d.Name,
		OwnerPK: d.HostPK,
		Port:    d.Port,
		Mode:    d.Mode,
		Kind:    d.Kind,
		PoWBits: d.PoWBits,
	}
}

// ---------------------------------------------------------------------
// requester side
// ---------------------------------------------------------------------

// ProbeGroup asks host what the group with id gid is.
//
// Answers locally without a round trip when this visor already holds the
// group: the store knows everything a probe would return, and a member
// re-opening its own address should not depend on the host being up.
func (m *Manager) ProbeGroup(ctx context.Context, host cipher.PubKey, gid string) (GroupDescriptor, error) {
	if gid == "" {
		return GroupDescriptor{}, errors.New("group: ProbeGroup: group id required")
	}
	if r, ok, err := m.store.Get(gid); err == nil && ok && !r.Status.IsTerminal() {
		return descriptorFor(r), nil
	}
	resp, err := sendProbe(ctx, m.dmsgC, host, gid, m.myPK)
	if err != nil {
		return GroupDescriptor{}, err
	}
	switch resp.Status {
	case JoinStatusInfo:
		return GroupDescriptor{
			ID:        gid,
			HostPK:    host,
			Name:      resp.Name,
			Kind:      probedKind(resp),
			Mode:      probedMode(resp),
			Policy:    probedPolicy(resp),
			Port:      resp.Port,
			PoWBits:   clampJoinPoWBits(resp.PoWBits),
			ReadOnly:  resp.ReadOnly,
			PriceHint: truncate(resp.PriceHint, maxPriceHintLen),
		}, nil
	case JoinStatusBanned:
		// Still a description — the group exists and we now know the one
		// fact that matters most about our relationship to it. Reported
		// as a descriptor rather than an error so a UI can name the group
		// and explain, instead of showing a bare failure.
		return GroupDescriptor{
			ID: gid, HostPK: host, Name: resp.Name,
			Kind: probedKind(resp), Mode: probedMode(resp),
			Policy: probedPolicy(resp), Port: resp.Port,
			Banned: true,
		}, nil
	case JoinStatusUnavailable:
		return GroupDescriptor{}, fmt.Errorf("%w: %s", ErrProbeNoSuchGroup, resp.Reason)
	default:
		// Any other status means the host understood the frame but
		// answered as though it were a real join — an older build that
		// somehow serves this port. Not usable as a description.
		return GroupDescriptor{}, fmt.Errorf("%w: unexpected status %q", ErrProbeUnreachable, resp.Status)
	}
}

// descriptorFor builds a descriptor from a record we already hold. Used
// for the local shortcut in ProbeGroup and by the responder.
func descriptorFor(r Record) GroupDescriptor {
	return GroupDescriptor{
		ID:       r.ID,
		HostPK:   r.OwnerPK,
		Name:     r.Name,
		Kind:     r.Kind,
		Mode:     r.Mode,
		Policy:   r.JoinPolicy(),
		Port:     r.Port,
		PoWBits:  r.JoinPoWRequired(),
		ReadOnly: r.ReadOnly,
	}
}

// probedKind reads the kind out of a probe answer, tolerating a
// responder that sent none, and refusing one whose kind contradicts its
// mode. Same rule and same reason as joinedKind: Mode drives decryption
// and Kind drives posting, so a record must never be built from a pair
// that disagrees.
func probedKind(resp JoinResponseMsg) Kind {
	if resp.GroupKind.IsValid() && modeForKind(resp.GroupKind) == probedMode(resp) {
		return resp.GroupKind
	}
	return kindForMode(probedMode(resp))
}

// probedMode derives the encryption mode from the answered kind, since
// the describe answer carries Kind and not Mode. An answer with no valid
// kind is treated as public — the safe direction, because it means "do
// not expect encrypted bodies" rather than "expect a key that isn't
// coming". A private group's actual mode comes back the moment its Kind
// does, and the admission response is authoritative either way.
func probedMode(resp JoinResponseMsg) Mode {
	if resp.GroupKind.IsValid() {
		return modeForKind(resp.GroupKind)
	}
	return ModePublic
}

// probedPolicy prefers the policy the host stated, falling back to what
// the kind implies. The two agree for every honest host; the fallback
// covers a responder that populated Kind but not Policy.
func probedPolicy(resp JoinResponseMsg) JoinPolicy {
	switch resp.Policy {
	case JoinOpen, JoinApproval:
		return resp.Policy
	default:
		return policyForKind(probedKind(resp))
	}
}

// JoinByAddress joins the group a short address names: probe for what it
// is, then drive the ordinary invite-based join with the answer.
//
// Idempotent through the same path a link-based join is — RequestJoin
// returns the existing record for a group we are already active in.
func (m *Manager) JoinByAddress(ctx context.Context, host cipher.PubKey, gid string) (Record, error) {
	if r, ok, err := m.store.Get(gid); err == nil && ok && r.Status == StatusActive {
		return r, nil
	}
	d, err := m.ProbeGroup(ctx, host, gid)
	if err != nil {
		return Record{}, err
	}
	if d.Banned {
		return Record{}, ErrJoinBanned
	}
	// A descriptor for a group we host is not something to join.
	if d.HostPK == m.myPK {
		return Record{}, errors.New("group: JoinByAddress: refusing to join own group")
	}
	return m.RequestJoin(d.Invite(), "")
}

// sendProbe writes one describe frame and reads the answer. Tries skynet
// first then dmsg, mirroring sendJoinRequestOnce — a probe should be
// reachable by whichever network the asker has.
func sendProbe(ctx context.Context, dmsgC *dmsg.Client, host cipher.PubKey, gid string, asker cipher.PubKey) (JoinResponseMsg, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	req := JoinRequestMsg{
		Kind:        frameKindJoinRequest,
		GroupID:     gid,
		RequesterPK: asker,
		Probe:       true,
		TS:          time.Now().UTC(),
	}
	body, err := encodeJoinRequest(req)
	if err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: probe: encode: %w", err)
	}

	skyAddr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: host, Port: routing.Port(ProbePort)}
	if conn, dialErr := dialSkynetRelay(ctx, skyAddr); dialErr == nil {
		resp, rtErr := probeRoundTrip(conn, body)
		_ = conn.Close() //nolint:errcheck
		if rtErr == nil {
			return resp, nil
		}
	}

	if dmsgC == nil {
		return JoinResponseMsg{}, ErrProbeUnreachable
	}
	dialCtx, cancel := context.WithTimeout(ctx, probeTimeout)
	defer cancel()
	stream, err := dmsgC.DialStream(dialCtx, dmsg.Addr{PK: host, Port: ProbePort})
	if err != nil {
		return JoinResponseMsg{}, fmt.Errorf("%w: %v", ErrProbeUnreachable, err)
	}
	defer stream.Close() //nolint:errcheck
	resp, err := probeRoundTrip(stream, body)
	if err != nil {
		return JoinResponseMsg{}, err
	}
	return resp, nil
}

func probeRoundTrip(c net.Conn, body []byte) (JoinResponseMsg, error) {
	deadline := time.Now().Add(probeTimeout)
	if err := c.SetWriteDeadline(deadline); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: probe: set write deadline: %w", err)
	}
	if err := message.WriteFrame(c, body); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: probe: write: %w", err)
	}
	if err := c.SetReadDeadline(deadline); err != nil {
		return JoinResponseMsg{}, fmt.Errorf("group: probe: set read deadline: %w", err)
	}
	frame, err := message.ReadFrame(c)
	if err != nil {
		// Wrapped as unreachable rather than as a decode failure: a host
		// that takes the frame and says nothing is the fingerprint of a
		// build that does not serve this port, and the caller's job is
		// then to suggest an invite link, not to report a protocol bug.
		return JoinResponseMsg{}, fmt.Errorf("%w: %v", ErrProbeUnreachable, err)
	}
	return decodeJoinResponse(frame)
}

// ---------------------------------------------------------------------
// responder side
// ---------------------------------------------------------------------

// probeListener holds the per-Manager describe listeners.
type probeListener struct {
	mu     sync.Mutex
	ctx    context.Context
	cancel context.CancelFunc
	dmsg   net.Listener
	wg     sync.WaitGroup
}

// StartProbeListener binds the well-known describe port and serves it
// until StopProbeListener (or Close) is called. Idempotent.
//
// Best effort by design, matching every other listener in this package:
// a failed bind disables short-address joins for this run and logs it,
// rather than failing group chat as a whole. The invite-link path is
// unaffected, so the degradation is "addresses need a link instead", not
// "groups are broken".
func (m *Manager) StartProbeListener() {
	m.probe.mu.Lock()
	defer m.probe.mu.Unlock()
	if m.probe.cancel != nil {
		return // already running
	}
	ctx, cancel := context.WithCancel(context.Background())
	m.probe.ctx, m.probe.cancel = ctx, cancel

	if m.dmsgC != nil {
		if lis, err := m.dmsgC.Listen(ProbePort); err != nil {
			m.log.WithError(err).WithField("port", ProbePort).
				Warn("group: probe: dmsg listen failed; short addresses will need an invite link")
		} else {
			m.probe.dmsg = lis
			m.probe.wg.Add(1)
			go m.acceptProbes(lis, "dmsg")
		}
	}
	m.probe.wg.Add(1)
	go m.bindProbeSkynet(ctx)
}

// StopProbeListener tears the describe listeners down. Idempotent.
func (m *Manager) StopProbeListener() {
	m.probe.mu.Lock()
	cancel := m.probe.cancel
	lis := m.probe.dmsg
	m.probe.cancel, m.probe.ctx, m.probe.dmsg = nil, nil, nil
	m.probe.mu.Unlock()
	if cancel == nil {
		return
	}
	cancel()
	if lis != nil {
		_ = lis.Close() //nolint:errcheck
	}
	m.probe.wg.Wait()
}

// probeCtx returns the live listener context, or nil once stopped.
func (m *Manager) probeCtx() context.Context {
	m.probe.mu.Lock()
	defer m.probe.mu.Unlock()
	return m.probe.ctx
}

func (m *Manager) acceptProbes(lis net.Listener, transport string) {
	defer m.probe.wg.Done()
	for {
		c, err := lis.Accept()
		if err != nil {
			if ctx := m.probeCtx(); ctx == nil || ctx.Err() == nil {
				m.log.WithError(err).WithField("transport", transport).
					Debug("group: probe: accept ended")
			}
			return
		}
		m.probe.wg.Add(1)
		go func(c net.Conn) {
			defer m.probe.wg.Done()
			defer c.Close() //nolint:errcheck
			m.handleProbe(c)
		}(c)
	}
}

// bindProbeSkynet waits for the skynet networker to register, then binds
// the same port there. Same deferred-bind reasoning as
// Session.bindRelaySkynet: the visor brings dmsg up before the router,
// and a listener started in between would otherwise never get a skynet
// side at all.
func (m *Manager) bindProbeSkynet(ctx context.Context) {
	defer m.probe.wg.Done()
	backoff := 200 * time.Millisecond
	const maxBackoff = 5 * time.Second
	for {
		if _, err := appnet.ResolveNetworker(appnet.TypeSkynet); err == nil {
			break
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(backoff):
			if backoff < maxBackoff {
				backoff *= 2
				if backoff > maxBackoff {
					backoff = maxBackoff
				}
			}
		}
	}
	lis, err := appnet.ListenContext(ctx, appnet.Addr{
		Net:    appnet.TypeSkynet,
		PubKey: m.myPK,
		Port:   routing.Port(ProbePort),
	})
	if err != nil {
		m.log.WithError(err).WithField("port", ProbePort).
			Debug("group: probe: skynet listen failed")
		return
	}
	go func() {
		<-ctx.Done()
		_ = lis.Close() //nolint:errcheck
	}()
	m.probe.wg.Add(1)
	m.acceptProbes(lis, "skynet")
}

// handleProbe answers one describe request.
//
// Ordering matches handleJoinRequest for the same reasons: decode first
// (a malformed frame costs nothing), then take the asker's identity from
// the AUTHENTICATED transport rather than the envelope — the ban answer
// below is about a specific PK, and answering it for a self-asserted one
// would let anybody ask "is Alice banned from this group".
//
// A frame that is NOT a probe is refused rather than treated as a join.
// This port makes no admission decisions at all, and quietly promoting a
// stray join request into one here would put a second, unrate-limited
// door on every group.
func (m *Manager) handleProbe(c net.Conn) {
	if err := c.SetReadDeadline(time.Now().Add(probeTimeout)); err != nil {
		return
	}
	frame, err := message.ReadFrame(c)
	if err != nil {
		m.log.WithError(err).Debug("group: probe: read frame")
		return
	}
	// Identity comes from the authenticated transport in both branches, so
	// take it before dispatching: a catalog answer is a disclosure and
	// should be attributable in the log even though it is not authorized
	// per-asker.
	asker, ok := remotePKOf(c)
	if !ok {
		m.log.Debug("group: probe: refusing request with no authenticated peer identity")
		return
	}
	// Two questions arrive on this port: "describe this group ID" and
	// "what do you publish?". They are separate frames rather than one
	// with an empty field because they disclose different things — see
	// catalog.go — and the discriminator keeps them from being confused.
	var probeKind relayFrameProbe
	if err := json.Unmarshal(frame, &probeKind); err == nil && probeKind.Kind == frameKindCatalogRequest {
		m.handleCatalogRequest(c, asker)
		return
	}
	req, err := decodeJoinRequest(frame)
	if err != nil {
		m.log.WithError(err).Debug("group: probe: undecodable frame")
		return
	}
	pk, err := requesterPK(c, req)
	if err != nil {
		m.log.WithError(err).Debug("group: probe: refusing unauthenticated request")
		return
	}
	resp := m.describeGroup(req, pk)
	body, err := encodeJoinResponse(resp)
	if err != nil {
		m.log.WithError(err).Debug("group: probe: encode response")
		return
	}
	if err := c.SetWriteDeadline(time.Now().Add(probeTimeout)); err != nil {
		return
	}
	if err := message.WriteFrame(c, body); err != nil {
		m.log.WithError(err).Debug("group: probe: write response")
	}
}

// describeGroup is the policy for what a describe answer reveals.
//
// Unavailable — not "denied" — for a group we do not hold, have left, or
// have deleted, so the shape matches admissionHandler's: a requester
// asking the wrong one of several admins learns to ask elsewhere rather
// than treating the group as closed. It is also the answer that says the
// least: "not from me" reveals nothing about whether the group exists.
func (m *Manager) describeGroup(req JoinRequestMsg, asker cipher.PubKey) JoinResponseMsg {
	if !req.Probe {
		return JoinResponseMsg{
			GroupID: req.GroupID,
			Status:  JoinStatusUnavailable,
			Reason:  "this port answers group describes only; join requests go to the group's own port",
		}
	}
	r, ok, err := m.store.Get(req.GroupID)
	if err != nil || !ok || r.Status.IsTerminal() {
		return JoinResponseMsg{
			GroupID: req.GroupID,
			Status:  JoinStatusUnavailable,
			Reason:  "this visor does not hold that group",
		}
	}
	// Named before the ban check so a banned asker still sees WHICH group
	// refused them. They hold its ID already, so this adds nothing they
	// could not have learned by asking about any other group.
	base := JoinResponseMsg{
		GroupID:   r.ID,
		Name:      r.Name,
		GroupKind: r.Kind,
		Policy:    r.JoinPolicy(),
		Port:      r.Port,
	}
	if r.IsBanned(asker) {
		base.Status = JoinStatusBanned
		base.Reason = "this public key is banned from the group"
		return base
	}
	base.Status = JoinStatusInfo
	base.PoWBits = r.JoinPoWRequired()
	base.ReadOnly = r.ReadOnly
	m.log.WithField("id", r.ID).WithField("asker", asker.Hex()).
		Debug("group: probe: described group")
	return base
}

// truncate bounds untrusted display text at n bytes.
func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
