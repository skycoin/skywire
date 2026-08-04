// Package profile pkg/skychat/profile/profile.go c4-app-chat
// what a visor says about itself: a display name and a tiny avatar.
//
// # Why this exists
//
// A public key is the only thing skychat has ever been able to show for a
// person, and 66 hex characters identify without describing. The address
// book (localStorage nicknames in the app's UI) fixed that one device at a
// time: everybody who ever wanted to talk to Alice had to be told, out of
// band, that this key is Alice. A profile lets Alice answer that question
// once, so pasting her address into "add by address" shows a name and a
// face before the first message rather than after the tenth.
//
// # What a profile is NOT
//
// It is not an identity claim and nothing here is verified. A visor states
// its own name; nobody countersigns it, and two visors may state the same
// one. The key remains the identity — a profile is decoration on top of it,
// which is why the UI keeps showing the abbreviated key beside a name and
// why a locally-set nickname always wins over a fetched one. Treat every
// field as untrusted display text from a stranger, because that is exactly
// what it is.
//
// # Why the avatar is capped, and where the cap comes from
//
// Two reasons, and the small one is bandwidth. The real one is that this
// travels inline in a single response frame on a read-only port that any
// stranger may dial: a picture with no size ceiling turns "ask who this is"
// into a way to make a visor serve arbitrary bytes to anybody who asks.
// So there is a ceiling, and it is enforced by decoding the image rather
// than by trusting a declared size.
//
// The number itself is set by the largest box the UI ever renders a
// published avatar in — the 120px profile dialog — doubled for a retina
// screen. That is the whole justification: 256 is small, not tiny. An
// earlier cap of 32 was small enough to be visibly a thumbnail everywhere
// it appeared, which is not what "keep it small" was asking for.
//
// The byte cap is the one that actually holds the wire promise, and it is
// the tighter of the two: a photographic 256x256 PNG is ~150 KB and would
// never fit, so the browser encodes JPEG and re-encodes down until it does.
// See MaxAvatarBytes for the frame arithmetic.
//
// Cropping and scaling happen in the browser before upload — the user is
// the one who knows which part of their photo is their face. This package
// only enforces the result.
package profile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"strings"
	"time"

	// Registered for image.DecodeConfig below. PNG and JPEG only: the
	// browser's canvas produces both, they are the two formats every
	// renderer handles, and a decoder this port exposes to strangers is a
	// place to be conservative rather than accommodating.
	_ "image/jpeg"
	_ "image/png"
)

const (
	// MaxNameLen bounds a display name. Matches the cap the UI has always
	// used for a local nickname, so a fetched name and a hand-typed one
	// cannot render differently.
	MaxNameLen = 40

	// MaxAvatarDim is the edge length an avatar may not exceed, in pixels.
	// Enforced by decoding the image, never by trusting a caller.
	//
	// 256 covers the largest box the UI renders a published avatar in (the
	// 120px profile dialog) at 2x, so a picture is never visibly upscaled
	// on the screens people actually use.
	MaxAvatarDim = 256

	// MaxAvatarBytes bounds the encoded image, and it is the cap that does
	// the real work: dimensions alone say nothing about how many bytes a
	// stranger can make this visor send.
	//
	// The arithmetic: the profile answer is one message.MaxFrameSize (64 KB)
	// frame of JSON, and Avatar is []byte, so it rides as base64 at 4/3 the
	// raw size. 32 KB raw is ~44 KB of frame, leaving ~21 KB of slack for the
	// name and the envelope. An oversize frame is not an error the asker can
	// see — WriteFrame refuses and the peer looks like it has no profile at
	// all — so the slack is deliberate, not spare.
	//
	// 32 KB also holds a 256x256 JPEG at a quality nobody squints at
	// (~10-25 KB for a photograph). It does not hold a 256x256 photographic
	// PNG, which is why the browser tries PNG first and falls back to JPEG
	// rather than committing to either.
	MaxAvatarBytes = 32 * 1024
)

// Avatar MIME types. Derived from the decoded image rather than taken from
// the sender, so a JPEG cannot arrive labeled as something else.
const (
	MimePNG  = "image/png"
	MimeJPEG = "image/jpeg"
)

var (
	// ErrAvatarTooLarge means the encoded image exceeds MaxAvatarBytes.
	ErrAvatarTooLarge = errors.New("skychat profile: avatar too large")

	// ErrAvatarDimensions means the image is bigger than MaxAvatarDim on
	// one or both edges. Reported separately from ErrAvatarTooLarge
	// because the fixes differ: one is "crop it", the other "compress it".
	//
	// Formatted from the constant so the two cannot drift apart. The
	// "avatar must be" prefix is load-bearing: the app-side handler
	// classifies a bad picture as HTTP 400 by matching it across the
	// net/rpc boundary, where errors.Is cannot reach (see
	// cmd/apps/skychat/commands/profile.go, isProfileInputError).
	ErrAvatarDimensions = fmt.Errorf("skychat profile: avatar must be at most %dx%d pixels", MaxAvatarDim, MaxAvatarDim)

	// ErrAvatarFormat means the bytes did not decode as PNG or JPEG.
	ErrAvatarFormat = errors.New("skychat profile: avatar must be a PNG or JPEG image")
)

// Profile is what a visor publishes about itself.
//
// Every field is optional. An empty profile is the default and a perfectly
// good answer: it means "I have not said anything about myself", which is
// what a visor that never opened the dialog should report rather than an
// error.
type Profile struct {
	// Name is the display name, already trimmed and length-bounded.
	Name string `json:"name,omitempty"`

	// Avatar is the raw encoded image (PNG or JPEG), at most
	// MaxAvatarDim on each edge and MaxAvatarBytes long.
	//
	// Raw bytes rather than a data URI: a data URI is a display
	// convenience that would put a second, unvalidated copy of the MIME
	// type on the wire, and the one place that needs the URI form (the
	// browser) can build it from these two fields. See AvatarDataURI.
	Avatar []byte `json:"avatar,omitempty"`

	// AvatarMime is the image's type as DECODED, one of the Mime
	// constants. Never trusted from input.
	AvatarMime string `json:"avatar_mime,omitempty"`

	// Updated is when this profile was last written, UTC. Carried so a
	// caller can cache a fetched profile and tell a stale copy from a
	// fresh one; it is self-reported, so it orders one visor's own
	// revisions and nothing else.
	Updated time.Time `json:"updated,omitzero"`
}

// IsZero reports whether this profile says nothing at all.
func (p Profile) IsZero() bool {
	return p.Name == "" && len(p.Avatar) == 0
}

// HasAvatar reports whether a usable image is present.
func (p Profile) HasAvatar() bool {
	return len(p.Avatar) > 0 && p.AvatarMime != ""
}

// AvatarDataURI renders the avatar for direct use in an <img src>, or ""
// when there is none.
//
// Built here rather than in the UI so the MIME type in the URI is always
// the decoded one — the browser will happily render whatever a data URI
// claims, and the point of validating the format was to decide that.
func (p Profile) AvatarDataURI() string {
	if !p.HasAvatar() {
		return ""
	}
	return "data:" + p.AvatarMime + ";base64," + base64.StdEncoding.EncodeToString(p.Avatar)
}

// Normalize cleans and validates a profile on the way in — from the local
// user setting theirs, and from the wire on receipt.
//
// One function for both directions deliberately: a peer's profile is
// display text arriving from a stranger and deserves at least the checks
// the local one gets, and two code paths with "the same" rules is how the
// receiving side ends up laxer than the sending side.
//
// Whitespace is collapsed and the name truncated rather than rejected,
// because a name is decoration: a user who pastes 200 characters wants a
// name, not an error dialog. The avatar IS rejected on failure — a picture
// that cannot be decoded has no salvageable half, and silently dropping it
// would leave the user believing an avatar was published.
func Normalize(p Profile) (Profile, error) {
	out := Profile{
		Name:    NormalizeName(p.Name),
		Updated: p.Updated,
	}
	if len(p.Avatar) > 0 {
		mime, err := ValidateAvatar(p.Avatar)
		if err != nil {
			return Profile{}, err
		}
		out.Avatar = p.Avatar
		out.AvatarMime = mime
	}
	if out.Updated.IsZero() {
		out.Updated = time.Now().UTC()
	} else {
		out.Updated = out.Updated.UTC()
	}
	return out, nil
}

// NormalizeName trims, collapses runs of whitespace, and truncates to
// MaxNameLen runes.
//
// Runes, not bytes: truncating mid-rune would produce a name that renders
// as a replacement character, and a cap meant to bound a display string
// should be counted in the units the user sees.
func NormalizeName(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) > MaxNameLen {
		r = r[:MaxNameLen]
	}
	return strings.TrimSpace(string(r))
}

// DecodeAvatar turns what a browser hands over into raw image bytes.
//
// Accepts a `data:` URI — which is what `canvas.toDataURL()` produces and
// therefore what the avatar cropper in the UI has in hand — or bare base64,
// or nothing. The declared MIME type in a data URI is deliberately IGNORED
// rather than parsed out and kept: ValidateAvatar decides the type by
// decoding the image, and a second answer from an untrusted string could
// only ever disagree with the real one.
//
// An empty input decodes to no bytes and no error: clearing an avatar is a
// legitimate edit, not a malformed one.
func DecodeAvatar(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if strings.HasPrefix(s, "data:") {
		i := strings.Index(s, ",")
		if i < 0 {
			return nil, fmt.Errorf("%w: malformed data URI", ErrAvatarFormat)
		}
		if !strings.Contains(strings.ToLower(s[:i]), ";base64") {
			return nil, fmt.Errorf("%w: data URI must be base64", ErrAvatarFormat)
		}
		s = s[i+1:]
	}
	// Whitespace inside base64 is what a hand-wrapped or pretty-printed
	// payload looks like; strip it rather than failing on a cosmetic
	// difference.
	s = strings.Join(strings.Fields(s), "")
	b, err := base64.StdEncoding.DecodeString(s)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrAvatarFormat, err)
	}
	return b, nil
}

// ValidateAvatar checks size, format and dimensions, and returns the
// decoded image's MIME type.
//
// Order matters: the byte cap is checked first because it is the one test
// that costs nothing and bounds the work every later step does. Decoding
// only the config — header and dimensions — rather than the full image
// keeps this a constant-cost check on a port strangers can reach.
func ValidateAvatar(b []byte) (string, error) {
	if len(b) == 0 {
		return "", ErrAvatarFormat
	}
	if len(b) > MaxAvatarBytes {
		return "", fmt.Errorf("%w: %d bytes, max %d", ErrAvatarTooLarge, len(b), MaxAvatarBytes)
	}
	cfg, format, err := image.DecodeConfig(bytes.NewReader(b))
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrAvatarFormat, err)
	}
	var mime string
	switch format {
	case "png":
		mime = MimePNG
	case "jpeg":
		mime = MimeJPEG
	default:
		return "", fmt.Errorf("%w: got %q", ErrAvatarFormat, format)
	}
	if cfg.Width <= 0 || cfg.Height <= 0 {
		return "", fmt.Errorf("%w: got %dx%d", ErrAvatarDimensions, cfg.Width, cfg.Height)
	}
	if cfg.Width > MaxAvatarDim || cfg.Height > MaxAvatarDim {
		return "", fmt.Errorf("%w: got %dx%d", ErrAvatarDimensions, cfg.Width, cfg.Height)
	}
	return mime, nil
}
