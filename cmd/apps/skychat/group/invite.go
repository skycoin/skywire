// Package group — cmd/apps/skychat/group/invite.go: encode + decode
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

	// AESKey is set iff Mode == ModePrivate. Anybody with this link
	// can decrypt the feed; the invite IS the key. Owner-side key
	// rotation = create a fresh group with a fresh invite.
	AESKey []byte `json:"aes_key,omitempty"`
}

// EncodeInvite returns the printable invite-link form.
func EncodeInvite(inv Invite) (string, error) {
	if !inv.Mode.IsValid() {
		return "", fmt.Errorf("group invite: invalid mode %q", inv.Mode)
	}
	if inv.Mode == ModePrivate && len(inv.AESKey) != 32 {
		return "", fmt.Errorf("group invite: private mode needs 32-byte AES key, got %d", len(inv.AESKey))
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
	if inv.Mode == ModePrivate && len(inv.AESKey) != 32 {
		return Invite{}, fmt.Errorf("group invite: private mode requires 32-byte AES key, got %d", len(inv.AESKey))
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
