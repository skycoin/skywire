// Package group cmd/apps/skychat/group/invite.go c4-app-chat
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
	if inv.ID == "" {
		return Invite{}, fmt.Errorf("group invite: empty ID")
	}
	if inv.OwnerPK == (cipher.PubKey{}) {
		return Invite{}, fmt.Errorf("group invite: empty OwnerPK")
	}
	if inv.Port == 0 {
		return Invite{}, fmt.Errorf("group invite: zero Port")
	}
	return inv, nil
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
