// Package group pkg/skychat/group/profile.go c4-app-chat
// "who is the person behind this public key?" — the third question the
// well-known describe port answers.
//
// # Why it lives here
//
// A profile is not a group, and pkg/skychat/profile — which owns what a
// profile IS, and every rule about what may be in one — knows nothing about
// this package. What lives here is only the transport: the frame, the
// dispatch, and the round trip.
//
// It shares the probe port with group describes and the discovery catalog
// because it is the same kind of question. All three are read-only, all
// three mutate nothing, all three are asked by someone holding a public key
// and nothing else, and all three must be answerable BEFORE any
// relationship exists — which is precisely what a per-group port cannot do.
// A fourth listener would have been a fourth bind, a fourth accept loop and
// a fourth port to keep out of the allocator's way, to serve a question the
// existing one already fits.
//
// # What it discloses
//
// Exactly what the visor's operator typed into a dialog, and only if they
// typed something. An unset profile answers empty rather than refusing:
// "nothing said" is the honest answer and, unlike an error, it does not let
// an asker distinguish a visor that has no profile from one that is
// deliberately withholding it — there is no such state.
//
// Unlike the catalog, this is not opt-in per item. Setting a profile IS the
// opt-in; a visor that has set none publishes none.
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
	"github.com/skycoin/skywire/pkg/skychat/profile"
)

// Profile frame kinds. Distinct from the join and catalog frames so the
// probe listener can dispatch on the discriminator alone.
const (
	frameKindProfileRequest  = "profile_request"
	frameKindProfileResponse = "profile_response"
)

// profileTimeout caps one dial, and then one exchange, of a profile fetch.
// A dmsg dial between two visors routinely takes several seconds, and at 5s
// most lookups failed, which the contact form showed as "no name published".
const profileTimeout = 15 * time.Second

// ErrProfileUnreachable means the host could not be reached or answered
// nothing — including a host running a build that predates profiles, which
// is indistinguishable from silence.
//
// Callers are expected to treat this as "no profile", not as a failure to
// report: every path that fetches one has a perfectly good fallback in the
// public key itself.
var ErrProfileUnreachable = errors.New("group: profile: host did not answer")

// ProfileProvider supplies this visor's published profile to the listener.
//
// An interface rather than the concrete *profile.Store so this package
// keeps depending on profile only for its data type, and so a Manager
// constructed without one (every test, and any caller that does not want to
// publish) simply answers empty.
type ProfileProvider interface {
	Load() (profile.Profile, error)
}

// ProfileRequestMsg asks a visor who it is.
//
// Carries no target public key: the answer is always about the visor being
// dialed. A "tell me about someone else" form would turn every visor into a
// directory of the people it has met, which is the opposite of a design
// where you learn about a peer from a human.
type ProfileRequestMsg struct {
	Kind string    `json:"kind"`
	TS   time.Time `json:"ts"`
}

// ProfileResponseMsg is the host's answer.
type ProfileResponseMsg struct {
	Kind string        `json:"kind"`
	PK   cipher.PubKey `json:"pk"`

	Name       string    `json:"name,omitempty"`
	Avatar     []byte    `json:"avatar,omitempty"`
	AvatarMime string    `json:"avatar_mime,omitempty"`
	Updated    time.Time `json:"updated,omitzero"`
}

// ---------------------------------------------------------------------
// host side
// ---------------------------------------------------------------------

// SetProfileProvider installs the source the describe port answers profile
// requests from. A nil provider (the default) answers empty.
func (m *Manager) SetProfileProvider(p ProfileProvider) {
	m.profileMu.Lock()
	m.profileSrc = p
	m.profileMu.Unlock()
}

// LocalProfile returns what this visor publishes about itself, so an
// operator can see their own listing without a round trip — the same
// courtesy Catalog gets for a zero host.
func (m *Manager) LocalProfile() (profile.Profile, error) {
	m.profileMu.RLock()
	src := m.profileSrc
	m.profileMu.RUnlock()
	if src == nil {
		return profile.Profile{}, nil
	}
	return src.Load()
}

// handleProfileRequest answers one profile frame on the probe listener.
//
// A read failure answers empty rather than staying silent: silence is how
// an unreachable host looks, and reporting "this visor has no profile"
// truthfully is better than making the asker retry something that will not
// improve.
func (m *Manager) handleProfileRequest(c net.Conn, asker cipher.PubKey) {
	resp := ProfileResponseMsg{Kind: frameKindProfileResponse, PK: m.myPK}
	p, err := m.LocalProfile()
	if err != nil {
		m.log.WithError(err).Debug("group: profile: read failed; answering empty")
	} else {
		resp.Name = p.Name
		resp.Avatar = p.Avatar
		resp.AvatarMime = p.AvatarMime
		resp.Updated = p.Updated
	}
	body, err := json.Marshal(&resp)
	if err != nil {
		m.log.WithError(err).Debug("group: profile: encode response")
		return
	}
	if err := c.SetWriteDeadline(time.Now().Add(profileTimeout)); err != nil {
		return
	}
	if err := message.WriteFrame(c, body); err != nil {
		m.log.WithError(err).Debug("group: profile: write response")
		return
	}
	m.log.WithField("asker", asker.Hex()).Debug("group: profile: answered")
}

// ---------------------------------------------------------------------
// caller side
// ---------------------------------------------------------------------

// FetchProfile asks host who it is.
//
// The answer is re-validated through profile.Normalize before it is
// returned, exactly as a locally-set one is: this is untrusted display text
// and an untrusted image from a stranger, and the receiving side is the
// only place that can enforce the caps. A host that sends an oversized
// avatar loses the avatar, not the name — the same partial-salvage rule the
// store applies to a hand-edited file.
//
// Answers locally without a round trip when host is this visor.
func (m *Manager) FetchProfile(ctx context.Context, host cipher.PubKey) (profile.Profile, error) {
	if host == (cipher.PubKey{}) || host == m.myPK {
		return m.LocalProfile()
	}
	if ctx == nil {
		ctx = context.Background()
	}
	req := ProfileRequestMsg{Kind: frameKindProfileRequest, TS: time.Now().UTC()}
	body, err := json.Marshal(&req)
	if err != nil {
		return profile.Profile{}, fmt.Errorf("group: profile: encode: %w", err)
	}
	frame, err := profileRoundTrip(ctx, m.dmsgC, host, body)
	if err != nil {
		return profile.Profile{}, err
	}
	var resp ProfileResponseMsg
	if err := json.Unmarshal(frame, &resp); err != nil {
		return profile.Profile{}, fmt.Errorf("group: profile: decode: %w", err)
	}
	if resp.Kind != frameKindProfileResponse {
		return profile.Profile{}, fmt.Errorf("group: profile: unexpected frame kind %q", resp.Kind)
	}
	// PK is taken from the connection we dialed, never from the body: the
	// transport authenticates the host, and a self-declared key in a
	// response could name somebody else's. Same rule as catalogAddress.
	out, err := profile.Normalize(profile.Profile{
		Name:    resp.Name,
		Avatar:  resp.Avatar,
		Updated: resp.Updated,
	})
	if err != nil {
		// The avatar was the only rejectable part; keep the name.
		return profile.Profile{
			Name:    profile.NormalizeName(resp.Name),
			Updated: resp.Updated.UTC(),
		}, nil
	}
	return out, nil
}

// profileRoundTrip asks over skynet and dmsg at once and takes the first
// answer. Tried in turn, a skynet dial that took seconds to fail left the dmsg
// dial too little of the budget to connect.
func profileRoundTrip(ctx context.Context, dmsgC *dmsg.Client, host cipher.PubKey, body []byte) ([]byte, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type result struct {
		frame []byte
		err   error
	}
	results := make(chan result, 2)
	attempts := 0
	ask := func(dial func(context.Context) (net.Conn, error)) {
		attempts++
		go func() {
			dialCtx, cancelDial := context.WithTimeout(ctx, profileTimeout)
			defer cancelDial()
			conn, err := dial(dialCtx)
			if err != nil {
				results <- result{err: err}
				return
			}
			defer conn.Close() //nolint:errcheck
			// The other path answered first: unblock this one's read.
			stop := context.AfterFunc(ctx, func() { _ = conn.Close() }) //nolint:errcheck
			defer stop()
			frame, err := profileExchange(conn, body)
			results <- result{frame: frame, err: err}
		}()
	}
	skyAddr := appnet.Addr{Net: appnet.TypeSkynet, PubKey: host, Port: routing.Port(ProbePort)}
	ask(func(ctx context.Context) (net.Conn, error) { return dialSkynetRelay(ctx, skyAddr) })
	if dmsgC != nil {
		ask(func(ctx context.Context) (net.Conn, error) {
			return dmsgC.DialStream(ctx, dmsg.Addr{PK: host, Port: ProbePort})
		})
	}
	var lastErr error
	for i := 0; i < attempts; i++ {
		select {
		case r := <-results:
			if r.err == nil {
				return r.frame, nil
			}
			lastErr = r.err
		case <-ctx.Done():
			return nil, fmt.Errorf("%w: %v", ErrProfileUnreachable, ctx.Err())
		}
	}
	return nil, fmt.Errorf("%w: %v", ErrProfileUnreachable, lastErr)
}

func profileExchange(c net.Conn, body []byte) ([]byte, error) {
	deadline := time.Now().Add(profileTimeout)
	if err := c.SetWriteDeadline(deadline); err != nil {
		return nil, fmt.Errorf("group: profile: set write deadline: %w", err)
	}
	if err := message.WriteFrame(c, body); err != nil {
		return nil, fmt.Errorf("group: profile: write: %w", err)
	}
	if err := c.SetReadDeadline(deadline); err != nil {
		return nil, fmt.Errorf("group: profile: set read deadline: %w", err)
	}
	frame, err := message.ReadFrame(c)
	if err != nil {
		// A host that takes the frame and says nothing is a build with no
		// profile support — reported as unreachable, which is both true
		// and the answer a caller should act on (fall back to the key).
		return nil, fmt.Errorf("%w: %v", ErrProfileUnreachable, err)
	}
	return frame, nil
}
