// Package cipher — DNS-label encoding for PubKey.
//
// Skynet URLs of the form `<subdomain>.<pk>.skynet` need to fit each
// component into a DNS label. The hex form is 66 chars (33 bytes × 2),
// which exceeds two limits:
//
//   - RFC 1035 caps each DNS label at 63 octets.
//   - X.520 ub-common-name caps the X.509 Subject.CommonName at 64.
//
// Either limit causes strict TLS stacks (NSS / Firefox / Chrome) to
// reject the leaf cert minted by the resolver's TLS-MITM mode as
// "improperly encoded," so HTTPS over skynet doesn't work at all
// with the hex form regardless of how the URL is dialled.
//
// RFC 4648 base32 of 33 bytes is 53 chars unpadded (ceil(33*8/5)),
// well under 63. Lowercase is canonical for DNS (case-insensitive
// but lowercase is the convention) and decodes case-insensitively
// regardless. Padding is stripped because DNS labels can't contain
// '='. We use base32 rather than base64url because DNS labels are
// case-insensitive: base64's A/a / B/b / + / / would collide or be
// rewritten by intermediaries.
package cipher

import (
	"encoding/base32"
	"fmt"
	"strings"
)

// dnsLabelEnc is RFC 4648 base32 without padding, decoded
// case-insensitively. 33-byte PubKey → 53 chars.
var dnsLabelEnc = base32.StdEncoding.WithPadding(base32.NoPadding)

// PubKeyDNSLabelLen is the number of base32 chars produced by DNSLabel
// for a 33-byte PubKey. Exposed so callers can branch between the new
// base32 form and the legacy 66-char hex form when parsing skynet URLs.
const PubKeyDNSLabelLen = 53

// DNSLabel returns the PubKey as a lowercase, unpadded base32 string
// suitable for use as a single DNS label inside a skynet hostname.
func (pk PubKey) DNSLabel() string {
	return strings.ToLower(dnsLabelEnc.EncodeToString(pk[:]))
}

// ParseDNSLabel decodes a base32 PubKey label produced by DNSLabel
// back into a PubKey. Input is treated case-insensitively. Returns
// an error when the input is not a valid 33-byte base32 encoding.
func ParseDNSLabel(label string) (PubKey, error) {
	raw, err := dnsLabelEnc.DecodeString(strings.ToUpper(label))
	if err != nil {
		return PubKey{}, fmt.Errorf("cipher: decode dns label: %w", err)
	}
	if len(raw) != 33 {
		return PubKey{}, fmt.Errorf("cipher: dns label decoded to %d bytes, want 33", len(raw))
	}
	var pk PubKey
	copy(pk[:], raw)
	return pk, nil
}
