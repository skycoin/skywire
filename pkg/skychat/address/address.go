// Package address pkg/skychat/address/address.go c4-app-chat
// the skychat:// address grammar — one shareable, scannable identifier
// for the three things a user can open: a person, a group, a channel.
//
// Grammar:
//
//	skychat://<pk>              a person — opens a 1:1 DM
//	skychat://<pk>/<group-id>   a group or channel hosted by that visor
//
// Both forms are short enough to print on a card and to encode in a
// low-density QR code, which is the whole reason they exist: an invite
// link (skychat:invite:<base64url(json)>) is a few hundred characters
// because it carries the group's port, mode, admin list and proof-of-work
// price inline, and a code that dense is unpleasant to scan on a phone.
//
// The trade is that an address is NOT self-contained. A group address
// names a group but does not describe it, so acting on one requires
// asking the host visor what it is — see Manager.ProbeGroup. That
// round trip is also what makes "type a key and let the app work out what
// it is" possible at all: a bare public key looks identical whether it
// belongs to a person or to the host of a channel, so nothing can be
// decided by inspection.
//
// Which of the two forms to hand out is therefore a real choice, not a
// preference: an address is short and stays valid as the group changes,
// an invite works while the host is offline. Both are accepted
// everywhere a user can paste — see the note on Parse and ErrIsInvite.
//
// Why the group ID is a path segment and not, say, a query parameter:
// a query needs escaping rules that QR alphanumeric mode does not cover
// cheaply, and "host/resource" is the shape every user already reads
// correctly from a URL.
package address

import (
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Scheme is the URI prefix of a skychat address.
//
// The `//` form is deliberate and distinguishes this from the older
// `skychat:invite:` links: an authority-style `//<pk>` says the part
// after it is a host, which is exactly what a visor public key is here.
const Scheme = "skychat://"

// inviteScheme is the invite-link prefix. Mirrored (not imported) from
// pkg/skychat/group so this package stays a leaf with no dependency on
// the group model — an address names a group, it does not know one.
// Parse only needs it to tell a caller "that is the other kind of link",
// so a copy of a constant that can never change without breaking every
// link already in circulation is the cheaper coupling.
const inviteScheme = "skychat:invite:"

// pkHexLen is the length of a hex-encoded 33-byte compressed secp256k1
// public key.
const pkHexLen = 66

var (
	// ErrEmpty is returned for input that is blank once trimmed.
	ErrEmpty = errors.New("skychat address: empty")

	// ErrIsInvite marks input that is a group invite link rather than an
	// address. Returned as a distinct sentinel so a caller holding one
	// paste box can try both parsers and route accordingly, rather than
	// telling a user who pasted a perfectly good invite that their
	// address is malformed.
	ErrIsInvite = errors.New("skychat address: this is an invite link, not an address")
)

// Kind is what an address points at.
type Kind string

const (
	// KindDM — a bare public key: a person, opened as a 1:1 chat.
	KindDM Kind = "dm"

	// KindGroup — a public key plus a group ID. Whether the group is an
	// ordinary group or a broadcast channel is NOT knowable from the
	// address; only the host visor can say, so a resolver reports the
	// group kind separately. Calling this KindGroup rather than
	// KindGroupOrChannel keeps the grammar honest: the address form is
	// the same for both, and pretending otherwise would invite callers
	// to trust a distinction the string cannot carry.
	KindGroup Kind = "group"
)

// Address is a parsed skychat address.
type Address struct {
	// PK is the visor the address names: the peer for a DM, the group's
	// host for a group address.
	PK cipher.PubKey

	// GroupID is the group's UUID, empty for a DM address.
	GroupID string
}

// Kind reports what this address points at.
func (a Address) Kind() Kind {
	if a.GroupID != "" {
		return KindGroup
	}
	return KindDM
}

// IsGroup reports whether this address names a group or channel rather
// than a person.
func (a Address) IsGroup() bool { return a.GroupID != "" }

// String renders the canonical form. Lowercase hex, because an address
// is compared and deduplicated as a string in several places (the UI's
// contact list keys on it) and mixed case would make two spellings of
// one identity look like two identities.
func (a Address) String() string {
	s := Scheme + strings.ToLower(a.PK.Hex())
	if a.GroupID != "" {
		s += "/" + a.GroupID
	}
	return s
}

// DM returns the address of a peer's 1:1 chat.
func DM(pk cipher.PubKey) Address { return Address{PK: pk} }

// Group returns the address of a group or channel hosted by pk.
func Group(pk cipher.PubKey, groupID string) Address {
	return Address{PK: pk, GroupID: groupID}
}

// Parse reads any of the accepted spellings of an address:
//
//	skychat://<pk>
//	skychat://<pk>/<group-id>
//	<pk>
//	<pk>/<group-id>
//
// The bare forms are accepted because the field this feeds used to take
// nothing but a raw public key, and every key a user has already saved
// or shared is in that shape. Requiring the scheme would have broken
// every one of them for no gain.
//
// Whitespace is trimmed and a trailing slash ignored — both are what a
// terminal copy, a QR decode, or a chat client's link detector tend to
// leave behind. A group invite link returns ErrIsInvite so the caller
// can hand it to the invite parser instead.
func Parse(s string) (Address, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return Address{}, ErrEmpty
	}
	if strings.HasPrefix(strings.ToLower(s), inviteScheme) {
		return Address{}, ErrIsInvite
	}
	// Case-insensitive on the scheme only. The key that follows is hex,
	// where case is not meaningful either, but the group ID is compared
	// byte-for-byte against a stored UUID so it is left exactly as given.
	if len(s) >= len(Scheme) && strings.EqualFold(s[:len(Scheme)], Scheme) {
		s = s[len(Scheme):]
	}
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return Address{}, ErrEmpty
	}

	pkPart, groupPart, _ := strings.Cut(s, "/")
	if strings.Contains(groupPart, "/") {
		return Address{}, fmt.Errorf("skychat address: unexpected extra path segment in %q", s)
	}

	// Length first: a truncated key is by far the most common paste
	// error, and "66 characters, got 64" tells the user what to fix
	// where cipher.PubKey.Set's decode error would not.
	pkPart = strings.ToLower(pkPart)
	if len(pkPart) != pkHexLen {
		return Address{}, fmt.Errorf("skychat address: public key must be %d hex characters, got %d", pkHexLen, len(pkPart))
	}
	var pk cipher.PubKey
	if err := pk.Set(pkPart); err != nil {
		return Address{}, fmt.Errorf("skychat address: invalid public key: %w", err)
	}

	if groupPart == "" {
		return Address{PK: pk}, nil
	}
	// Group IDs are uuid.NewString() values (see group.Manager.Create).
	// Validating here means a mistyped address fails locally instead of
	// spending a dial and a 15s probe timeout to be told the group does
	// not exist.
	if _, err := uuid.Parse(groupPart); err != nil {
		return Address{}, fmt.Errorf("skychat address: invalid group id %q", groupPart)
	}
	return Address{PK: pk, GroupID: groupPart}, nil
}

// IsInvite reports whether s looks like a group invite link. Exported so
// a caller can route input before parsing, without depending on the
// error value.
func IsInvite(s string) bool {
	return strings.HasPrefix(strings.ToLower(strings.TrimSpace(s)), inviteScheme)
}
