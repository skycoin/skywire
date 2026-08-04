// Package profile pkg/skychat/profile/profile_test.go c4-app-chat
//
// The rules that matter here are the ones a stranger's input has to clear:
// an avatar is decoded before it is believed, and a name is bounded before
// it is rendered. Both are exercised from the receiving side's point of
// view, because that is the side that cannot trust its input.
package profile

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
	"time"
)

// pngOf encodes a solid w×h PNG — a stand-in for whatever the browser's
// canvas produced.
func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for x := 0; x < w; x++ {
		for y := 0; y < h; y++ {
			img.Set(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 0x40, A: 0xff}) //nolint:gosec // bounded by the test's own dimensions
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func jpegOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestValidateAvatarAcceptsSmallPNGAndJPEG(t *testing.T) {
	mime, err := ValidateAvatar(pngOf(t, MaxAvatarDim, MaxAvatarDim))
	if err != nil {
		t.Fatalf("%dx%d png rejected: %v", MaxAvatarDim, MaxAvatarDim, err)
	}
	if mime != MimePNG {
		t.Fatalf("mime = %q, want %q", mime, MimePNG)
	}

	mime, err = ValidateAvatar(jpegOf(t, 16, 16))
	if err != nil {
		t.Fatalf("16x16 jpeg rejected: %v", err)
	}
	if mime != MimeJPEG {
		t.Fatalf("mime = %q, want %q", mime, MimeJPEG)
	}
}

// The size ceiling is the whole point of the format: it is what stops the
// describe port becoming a way to make a visor serve arbitrary bytes.
// Written against MaxAvatarDim rather than a literal: the cap is a product
// decision that has moved once already, and a test that pins the number
// pins the wrong thing — what must hold is "one pixel over is refused, and
// the refusal says what the limit is".
func TestValidateAvatarRejectsOversizeDimensions(t *testing.T) {
	over := MaxAvatarDim + 1
	limit := fmt.Sprintf("%dx%d", MaxAvatarDim, MaxAvatarDim)
	for _, dim := range [][2]int{{over, MaxAvatarDim}, {MaxAvatarDim, over}, {over, over}} {
		_, err := ValidateAvatar(pngOf(t, dim[0], dim[1]))
		if err == nil {
			t.Fatalf("%dx%d accepted, want rejection", dim[0], dim[1])
		}
		if !errors.Is(err, ErrAvatarDimensions) {
			t.Fatalf("%dx%d: err = %v, want ErrAvatarDimensions", dim[0], dim[1], err)
		}
		if !strings.Contains(err.Error(), limit) {
			t.Fatalf("%dx%d: error %q does not say what the limit is", dim[0], dim[1], err)
		}
	}
}

// The app-side HTTP handler maps a rejected picture to 400 by matching this
// prefix across the net/rpc boundary, where the error is a plain string and
// errors.Is cannot help. Rewording either sentinel past this point silently
// turns a bad picture into a 500 — so the contract is pinned here, beside
// the errors, rather than left to be discovered in the browser.
func TestAvatarErrorsKeepTheirClassifiablePrefixes(t *testing.T) {
	for _, err := range []error{ErrAvatarDimensions, ErrAvatarTooLarge} {
		if !strings.HasPrefix(err.Error(), "skychat profile: avatar ") {
			t.Fatalf("%q lost the prefix cmd/apps/skychat/commands/profile.go matches on", err)
		}
	}
	if !strings.Contains(ErrAvatarDimensions.Error(), "avatar must be") {
		t.Fatalf("ErrAvatarDimensions %q no longer matches isProfileInputError", ErrAvatarDimensions)
	}
	if !strings.Contains(ErrAvatarTooLarge.Error(), "avatar too large") {
		t.Fatalf("ErrAvatarTooLarge %q no longer matches isProfileInputError", ErrAvatarTooLarge)
	}
}

func TestValidateAvatarRejectsNonImage(t *testing.T) {
	if _, err := ValidateAvatar([]byte("not an image at all")); err == nil {
		t.Fatal("garbage accepted as an avatar")
	}
	if _, err := ValidateAvatar(nil); err == nil {
		t.Fatal("empty input accepted as an avatar")
	}
}

func TestValidateAvatarRejectsOversizeBytes(t *testing.T) {
	big := make([]byte, MaxAvatarBytes+1)
	copy(big, pngOf(t, 8, 8))
	if _, err := ValidateAvatar(big); err == nil {
		t.Fatal("oversize payload accepted")
	}
}

// A GIF decodes fine as an image but is not in the accepted set, and the
// rejection must come from the format check rather than from a decode
// failure — otherwise adding a decoder import would silently widen what
// this port accepts.
func TestValidateAvatarRejectsUnregisteredFormat(t *testing.T) {
	gif := []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x00;")
	if _, err := ValidateAvatar(gif); err == nil {
		t.Fatal("gif accepted; only png and jpeg should be")
	}
}

func TestNormalizeNameCollapsesAndTruncates(t *testing.T) {
	cases := []struct{ in, want string }{
		{"  Alice  ", "Alice"},
		{"Alice\t\n  Smith", "Alice Smith"},
		{"", ""},
		{"   ", ""},
		{strings.Repeat("a", MaxNameLen+20), strings.Repeat("a", MaxNameLen)},
	}
	for _, c := range cases {
		if got := NormalizeName(c.in); got != c.want {
			t.Fatalf("NormalizeName(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

// Truncation counts runes, not bytes: a byte-wise cut through a multi-byte
// character renders as a replacement glyph, which is a worse outcome than
// the long name it was protecting against.
func TestNormalizeNameTruncatesOnRuneBoundaries(t *testing.T) {
	got := NormalizeName(strings.Repeat("é", MaxNameLen+5))
	if n := len([]rune(got)); n != MaxNameLen {
		t.Fatalf("got %d runes, want %d", n, MaxNameLen)
	}
	if strings.ContainsRune(got, '�') {
		t.Fatalf("truncation split a rune: %q", got)
	}
}

func TestNormalizeStampsUpdatedAndKeepsMime(t *testing.T) {
	p, err := Normalize(Profile{Name: "  Bob ", Avatar: pngOf(t, 32, 32)})
	if err != nil {
		t.Fatalf("Normalize: %v", err)
	}
	if p.Name != "Bob" {
		t.Fatalf("name = %q", p.Name)
	}
	if p.AvatarMime != MimePNG {
		t.Fatalf("mime = %q, want %q", p.AvatarMime, MimePNG)
	}
	if p.Updated.IsZero() {
		t.Fatal("Updated not stamped")
	}
	if p.Updated.Location() != time.UTC {
		t.Fatalf("Updated is %v, want UTC", p.Updated.Location())
	}
}

// An avatar that fails validation fails the whole call: a caller that asked
// to publish a picture must not be told it succeeded while the picture was
// silently dropped.
func TestNormalizeRejectsBadAvatar(t *testing.T) {
	if _, err := Normalize(Profile{Name: "Bob", Avatar: []byte("nope")}); err == nil {
		t.Fatal("bad avatar accepted by Normalize")
	}
}

func TestDecodeAvatarAcceptsDataURIAndBareBase64(t *testing.T) {
	raw := pngOf(t, 32, 32)
	enc := base64.StdEncoding.EncodeToString(raw)

	got, err := DecodeAvatar("data:image/png;base64," + enc)
	if err != nil {
		t.Fatalf("data URI: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("data URI round trip changed the bytes")
	}

	got, err = DecodeAvatar(enc)
	if err != nil {
		t.Fatalf("bare base64: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Fatal("bare base64 round trip changed the bytes")
	}
}

// Clearing an avatar goes through the same field as setting one, so empty
// input has to be a legitimate value rather than a decode failure.
func TestDecodeAvatarEmptyIsNotAnError(t *testing.T) {
	got, err := DecodeAvatar("   ")
	if err != nil {
		t.Fatalf("empty input: %v", err)
	}
	if got != nil {
		t.Fatalf("empty input decoded to %d bytes", len(got))
	}
}

func TestDecodeAvatarRejectsMalformed(t *testing.T) {
	for _, in := range []string{
		"data:image/png;base64",       // no comma
		"data:image/png,rawbytes",     // not base64
		"!!!! not base64 at all !!!!", // undecodable
	} {
		if _, err := DecodeAvatar(in); err == nil {
			t.Fatalf("%q accepted", in)
		}
	}
}

// The MIME type a data URI declares is decoration; the one that ends up on
// the wire must come from decoding the image. A JPEG mislabeled as PNG has
// to survive as a JPEG rather than being published under the wrong type.
func TestDeclaredMimeIsIgnored(t *testing.T) {
	raw := jpegOf(t, 16, 16)
	b, err := DecodeAvatar("data:image/png;base64," + base64.StdEncoding.EncodeToString(raw))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	p, err := Normalize(Profile{Avatar: b})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	if p.AvatarMime != MimeJPEG {
		t.Fatalf("mime = %q, want %q — the declared type was believed", p.AvatarMime, MimeJPEG)
	}
}

func TestAvatarDataURIRoundTrips(t *testing.T) {
	raw := pngOf(t, 32, 32)
	p, err := Normalize(Profile{Avatar: raw})
	if err != nil {
		t.Fatalf("normalize: %v", err)
	}
	uri := p.AvatarDataURI()
	if !strings.HasPrefix(uri, "data:"+MimePNG+";base64,") {
		t.Fatalf("data URI has the wrong prefix: %.40s", uri)
	}
	back, err := DecodeAvatar(uri)
	if err != nil {
		t.Fatalf("decode own URI: %v", err)
	}
	if !bytes.Equal(back, raw) {
		t.Fatal("AvatarDataURI/DecodeAvatar do not round trip")
	}
}

func TestIsZeroAndHasAvatar(t *testing.T) {
	if !(Profile{}).IsZero() {
		t.Fatal("empty profile is not IsZero")
	}
	if (Profile{Name: "x"}).IsZero() {
		t.Fatal("named profile reported as zero")
	}
	if (Profile{Avatar: []byte{1}}).HasAvatar() {
		t.Fatal("avatar with no mime reported as usable")
	}
	if !(Profile{Avatar: []byte{1}, AvatarMime: MimePNG}).HasAvatar() {
		t.Fatal("complete avatar reported as absent")
	}
}
