// Package group pkg/skychat/group/invite.go c4-app-chat
// the invite payload an owner hands to a prospective member.
//
// Invite-link grammar:
//
//	skychat:invite:<base64url(json(Invite))>
//
// base64url so a single-token paste through a terminal / chat tab /
// shell variable doesn't get mangled. URL queries would have worked
// for ModePublic but the AES key in ModePrivate doesn't urlencode
// cleanly across every shell.
//
// The whole payload is plaintext on the wire — there's no signature
// over it. The trust model is "whoever you handed the invite to is
// trusted to be in the group"; the visor-level allowlist enforces
// that on the CXO subscribe side, and AES key possession + allowlist
// together gate access to private group bodies.
//
// AES key generation lives here too because the only callers that
// need to generate a key are the create + parse paths.
package group

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/skycoin/skywire/pkg/cipher"
)

// inviteURIScheme is the prefix every invite link starts with. The
// `://` form would also have worked but `:` keeps the link short
// enough to read in a one-line chat message.
const inviteURIScheme = "skychat:invite:"

// Invite is the on-the-wire content of an invite link. Mirrors the
// subset of group.Record an invitee needs to (a) bootstrap a local
// Record and (b) start the CXO subscribe.
type Invite struct {
	ID      string        `json:"id"`
	Name    string        `json:"name"`
	OwnerPK cipher.PubKey `json:"owner_pk"`
	Port    uint16        `json:"port"`
	Mode    Mode          `json:"mode"`

	// Kind is the group type, carried because Mode alone can no longer
	// name it: a public group and a channel are both ModePublic, and
	// differ only in who may publish. Without this a channel joined
	// from a link would come up as an ordinary public group and its
	// members' composers would be unlocked.
	//
	// Absent in links minted before channels existed; a joiner reading
	// one derives the kind from Mode, which is exactly the old
	// behavior. Untrusted like the rest of the link, and cross-checked
	// against Mode on decode — the group's own answer to the join
	// request is what a joiner ultimately records.
	Kind Kind `json:"kind,omitempty"`

	// Admins are further PKs with roster authority that a joiner may ask
	// for admission when the founder doesn't answer. Excludes OwnerPK,
	// which is carried above and is always tried first.
	//
	// Without this the founder was a single point of failure in the one
	// place a group can least afford one: every invite pointed join
	// requests at OwnerPK alone, so a founder that was offline meant
	// nobody could join, and a founder whose key was lost meant the
	// group could never admit anyone again — while other admins sat
	// there, authorized and idle. The founder stays the recovery anchor;
	// it just stops being the only door.
	//
	// Absent in links minted before this field existed. A joiner reading
	// one falls back to the founder alone, which is exactly the old
	// behavior; an older joiner reading a link that HAS the field
	// ignores it and dials the founder, same as it always did.
	Admins []cipher.PubKey `json:"admins,omitempty"`

	// PoWBits is how much proof of work the group wants with a join
	// request. Carried so the common case costs no extra round trip: the
	// joiner pays before it dials, and only a stale or absent value turns
	// into a challenge from the responder.
	//
	// Untrusted, like the rest of the link — it is clamped to
	// MaxJoinPoWBits on read so a hostile invite cannot make whoever
	// pastes it grind for hours. The responder checks against its own
	// requirement regardless, so a link understating the price costs the
	// joiner one extra round trip rather than getting anyone in cheaply.
	PoWBits uint8 `json:"pow_bits,omitempty"`

	// AESKey is the group key, and is now normally ABSENT even for
	// ModePrivate: the key is handed to a joiner in the approval
	// response over the encrypted relay stream, once an admin has said
	// yes. See JoinResponseMsg.AESKey.
	//
	// It used to travel here, which made the invite itself the secret —
	// anyone the link reached could decrypt the feed, a denied
	// requester included, and the key could never be rotated because
	// links already in circulation would keep working. Links minted by
	// older builds still carry it and are still accepted; the legacy
	// join path uses it when a group doesn't answer the join request.
	AESKey []byte `json:"aes_key,omitempty"`
}

// AdmissionTargets returns the ordered PKs this invite says may admit
// us: the founder, then the named admins. self is excluded so we never
// dial ourselves. See admissionOrder for the ordering rationale and the
// cap.
func (inv Invite) AdmissionTargets(self cipher.PubKey) []cipher.PubKey {
	return admissionOrder(inv.OwnerPK, self, inv.Admins)
}

// EncodeInvite returns the printable invite-link form.
func EncodeInvite(inv Invite) (string, error) {
	if !inv.Mode.IsValid() {
		return "", fmt.Errorf("group invite: invalid mode %q", inv.Mode)
	}
	// A private invite may legitimately carry no key — that is the
	// key-on-approval shape. If one IS present (a legacy link being
	// re-encoded) it still has to be the right size.
	if len(inv.AESKey) > 0 && len(inv.AESKey) != 32 {
		return "", fmt.Errorf("group invite: AES key must be 32 bytes, got %d", len(inv.AESKey))
	}
	if inv.Mode == ModePublic && len(inv.AESKey) > 0 {
		return "", fmt.Errorf("group invite: public mode must not carry an AES key")
	}
	if err := checkInviteKind(inv); err != nil {
		return "", err
	}
	body, err := json.Marshal(inv)
	if err != nil {
		return "", fmt.Errorf("group invite: marshal: %w", err)
	}
	return inviteURIScheme + base64.RawURLEncoding.EncodeToString(body), nil
}

// DecodeInvite parses an invite-link string back into an Invite.
// Tolerates leading/trailing whitespace because operators paste
// these from terminals.
func DecodeInvite(s string) (Invite, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, inviteURIScheme) {
		return Invite{}, fmt.Errorf("group invite: missing %q prefix", inviteURIScheme)
	}
	payload := strings.TrimPrefix(s, inviteURIScheme)
	body, err := base64.RawURLEncoding.DecodeString(payload)
	if err != nil {
		return Invite{}, fmt.Errorf("group invite: base64 decode: %w", err)
	}
	var inv Invite
	if err := json.Unmarshal(body, &inv); err != nil {
		return Invite{}, fmt.Errorf("group invite: json unmarshal: %w", err)
	}
	if !inv.Mode.IsValid() {
		return Invite{}, fmt.Errorf("group invite: invalid mode %q", inv.Mode)
	}
	if len(inv.AESKey) > 0 && len(inv.AESKey) != 32 {
		return Invite{}, fmt.Errorf("group invite: AES key must be 32 bytes, got %d", len(inv.AESKey))
	}
	if inv.Mode == ModePublic && len(inv.AESKey) > 0 {
		return Invite{}, fmt.Errorf("group invite: public mode must not carry an AES key")
	}
	if err := checkInviteKind(inv); err != nil {
		return Invite{}, err
	}
	if inv.ID == "" {
		return Invite{}, fmt.Errorf("group invite: empty ID")
	}
	if inv.OwnerPK == (cipher.PubKey{}) {
		return Invite{}, fmt.Errorf("group invite: empty OwnerPK")
	}
	if inv.Port == 0 {
		return Invite{}, fmt.Errorf("group invite: zero Port")
	}
	inv.PoWBits = clampJoinPoWBits(inv.PoWBits)
	return inv, nil
}

// checkInviteKind validates the optional Kind field against Mode.
//
// An empty Kind is fine — that is a link from before channels existed,
// and the reader derives the kind from Mode. A Kind that IS present has
// to agree with Mode, because the two are read by different code paths:
// Mode decides whether bodies are decrypted, Kind decides who may post.
// A link asserting kind=private/mode=public would produce a record that
// expects encrypted bodies on a plaintext feed and never renders
// anything, so it is rejected here rather than persisted.
func checkInviteKind(inv Invite) error {
	if inv.Kind == "" {
		return nil
	}
	if !inv.Kind.IsValid() {
		return fmt.Errorf("group invite: invalid kind %q", inv.Kind)
	}
	if modeForKind(inv.Kind) != inv.Mode {
		return fmt.Errorf("group invite: kind %q does not match mode %q", inv.Kind, inv.Mode)
	}
	return nil
}

// InviteKind returns the group type this invite names, falling back to
// what Mode implies for links minted before Kind was carried. The single
// accessor callers should use so the legacy fallback lives in one place.
func (inv Invite) InviteKind() Kind {
	if inv.Kind == "" {
		return kindForMode(inv.Mode)
	}
	return inv.Kind
}

// GenerateAESKey returns a fresh 32-byte key sourced from
// crypto/rand. Used at create time for ModePrivate groups.
func GenerateAESKey() ([]byte, error) {
	k := make([]byte, 32)
	if _, err := rand.Read(k); err != nil {
		return nil, fmt.Errorf("group invite: gen AES key: %w", err)
	}
	return k, nil
}
