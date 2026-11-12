// Package alerts pkg/alerts/push.go c3-vis-core
package alerts

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// DefaultPushSubject is the VAPID contact (RFC 8292 section 2.1) when no
// email address is set.
const DefaultPushSubject = "https://github.com/skycoin/skywire"

// pushRecordSize is the aes128gcm record size (RFC 8188 section 2.1).
const pushRecordSize = 4096

// maxPushPayload keeps the payload inside the 4096 byte record with room
// for the delimiter and tag.
const maxPushPayload = 3000

// PushKeys are a browser subscription's keys, base64url as the browser
// gives them.
type PushKeys struct {
	P256DH string `json:"p256dh,omitempty"`
	Auth   string `json:"auth,omitempty"`
}

// PushSubscription is one browser that gets Web Push alerts. The fields
// endpoint and keys match PushSubscription.toJSON() in the browser.
type PushSubscription struct {
	ID       string    `json:"id,omitempty"`
	Endpoint string    `json:"endpoint"`
	Keys     PushKeys  `json:"keys"`
	Label    string    `json:"label,omitempty"`
	Created  time.Time `json:"created,omitzero"`
	// ExpirationTime is the browser's, in ms since the epoch, if any.
	ExpirationTime *int64 `json:"expirationTime,omitempty"`
}

// PushID is the stable id of a subscription endpoint, so the same
// browser subscribing again replaces its old entry.
func PushID(endpoint string) string {
	h := sha256.Sum256([]byte(endpoint))
	return hex.EncodeToString(h[:4])
}

// Validate checks the endpoint and the key lengths.
func (p *PushSubscription) Validate() error {
	u, err := url.Parse(p.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("push endpoint %q is not an https URL", p.Endpoint)
	}
	if k, err := b64(p.Keys.P256DH); err != nil || len(k) != 65 {
		return errors.New("push subscription p256dh key is not a P-256 point")
	}
	if a, err := b64(p.Keys.Auth); err != nil || len(a) != 16 {
		return errors.New("push subscription auth secret is not 16 bytes")
	}
	return nil
}

// Host is the push service host, for display.
func (p *PushSubscription) Host() string {
	if u, err := url.Parse(p.Endpoint); err == nil {
		return u.Host
	}
	return ""
}

func (p *PushSubscription) name() string {
	if p.Label != "" {
		return p.Label + " (" + p.ID + ")"
	}
	return p.Host() + " (" + p.ID + ")"
}

// b64 decodes base64url with or without padding.
func b64(s string) ([]byte, error) {
	return base64.RawURLEncoding.DecodeString(strings.TrimRight(s, "="))
}

// PushPayload is the JSON a subscribed browser receives.
type PushPayload struct {
	Title   string    `json:"title"`
	Body    string    `json:"body"`
	Event   string    `json:"event"`
	PK      string    `json:"pk,omitempty"`
	Watcher string    `json:"watcher"`
	At      time.Time `json:"at"`
	// Tag groups the notifications of one visor or site.
	Tag string `json:"tag"`
}

func (s *Sender) pushPayload(ev Event) []byte {
	body, _, _ := strings.Cut(s.Body(ev), "\n")
	if ev.Diagnosis != "" {
		body += " " + ev.Diagnosis + "."
	}
	if ev.Target.Hostname != "" && ev.Scope != ScopeSite {
		body += " Host " + ev.Target.Hostname + "."
	}
	p := PushPayload{
		Title: strings.TrimPrefix(Subject(ev), "[skywire] "), Body: body, Event: ev.Kind,
		Watcher: s.Watcher.String(), At: ev.At, Tag: "skywire-" + ev.Kind,
	}
	switch {
	case ev.Scope == ScopeSite:
		p.Tag = "skywire-site-" + ev.Site
	case !ev.Target.PK.Null():
		p.PK = ev.Target.PK.String()
		p.Tag = "skywire-" + p.PK
	}
	b, _ := json.Marshal(p) //nolint:errcheck
	if len(b) > maxPushPayload {
		p.Body = p.Body[:min(len(p.Body), 1000)]
		b, _ = json.Marshal(p) //nolint:errcheck
	}
	return b
}

// sendPush encrypts payload for sub and posts it with a VAPID token. gone
// reports a subscription the push service no longer knows.
func (s *Sender) sendPush(ctx context.Context, subject string, sub PushSubscription, payload []byte, urgent bool) (gone bool, err error) {
	if s.VAPID == nil {
		return false, errors.New("no VAPID key")
	}
	body, err := encryptPush(sub, payload, nil, nil)
	if err != nil {
		return false, err
	}
	tok, err := vapidToken(s.VAPID, sub.Endpoint, subject, time.Now())
	if err != nil {
		return false, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, sub.Endpoint, bytes.NewReader(body))
	if err != nil {
		return false, err
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Authorization", "vapid t="+tok+", k="+VAPIDPublicKey(s.VAPID))
	if urgent {
		req.Header.Set("Urgency", "high")
	} else {
		req.Header.Set("Urgency", "normal")
	}
	hc := s.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 15 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close() //nolint:errcheck
	if resp.StatusCode/100 == 2 {
		return false, nil
	}
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 200)) //nolint:errcheck
	gone = resp.StatusCode == http.StatusNotFound || resp.StatusCode == http.StatusGone
	return gone, fmt.Errorf("push service answered %s %s", resp.Status, strings.TrimSpace(string(msg)))
}

// encryptPush builds an aes128gcm message body (RFC 8291 section 4). Nil
// asKey and salt are random; tests pass the RFC 8291 Appendix A values.
func encryptPush(sub PushSubscription, plaintext []byte, asKey *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaRaw, err := b64(sub.Keys.P256DH)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	authSecret, err := b64(sub.Keys.Auth)
	if err != nil {
		return nil, fmt.Errorf("auth: %w", err)
	}
	uaPub, err := ecdh.P256().NewPublicKey(uaRaw)
	if err != nil {
		return nil, fmt.Errorf("p256dh: %w", err)
	}
	if asKey == nil {
		if asKey, err = ecdh.P256().GenerateKey(rand.Reader); err != nil {
			return nil, err
		}
	}
	if salt == nil {
		salt = make([]byte, 16)
		if _, err := rand.Read(salt); err != nil {
			return nil, err
		}
	}
	shared, err := asKey.ECDH(uaPub)
	if err != nil {
		return nil, err
	}
	asRaw := asKey.PublicKey().Bytes()

	// RFC 8291 section 3.4, each HKDF here needs a single expand block.
	keyInfo := append(append([]byte("WebPush: info\x00"), uaRaw...), asRaw...)
	ikm := hkdf(authSecret, shared, keyInfo, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)

	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	if len(plaintext)+1+gcm.Overhead() > pushRecordSize {
		return nil, errors.New("push payload too large")
	}
	// One record, so the padding delimiter is 2 (RFC 8188 section 2).
	rec := append(append([]byte(nil), plaintext...), 2)

	out := make([]byte, 0, 16+4+1+65+pushRecordSize)
	out = append(out, salt...)
	out = binary.BigEndian.AppendUint32(out, pushRecordSize)
	out = append(out, byte(len(asRaw))) //nolint:gosec // 65, an uncompressed P-256 point
	out = append(out, asRaw...)
	return gcm.Seal(out, nonce, rec, nil), nil
}

// hkdf is HKDF-SHA-256 (RFC 5869) for an output of at most 32 bytes.
func hkdf(salt, ikm, info []byte, n int) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(ikm) //nolint:errcheck
	prk := m.Sum(nil)
	m = hmac.New(sha256.New, prk)
	m.Write(info)         //nolint:errcheck
	m.Write([]byte{0x01}) //nolint:errcheck
	return m.Sum(nil)[:n]
}

// vapidToken is the ES256 JWT of RFC 8292 section 2 for endpoint's
// origin.
func vapidToken(key *ecdsa.PrivateKey, endpoint, subject string, now time.Time) (string, error) {
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	enc := base64.RawURLEncoding
	hdr := enc.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, err := json.Marshal(map[string]any{
		"aud": u.Scheme + "://" + u.Host,
		"exp": now.Add(time.Hour).Unix(),
		"sub": subject,
	})
	if err != nil {
		return "", err
	}
	signing := hdr + "." + enc.EncodeToString(claims)
	h := sha256.Sum256([]byte(signing))
	r, sig, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", err
	}
	raw := make([]byte, 64)
	r.FillBytes(raw[:32])
	sig.FillBytes(raw[32:])
	return signing + "." + enc.EncodeToString(raw), nil
}

// VAPIDPublicKey is the uncompressed public point, base64url, as the
// browser's applicationServerKey wants it.
func VAPIDPublicKey(key *ecdsa.PrivateKey) string {
	if key == nil {
		return ""
	}
	pub, err := key.PublicKey.Bytes()
	if err != nil {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(pub)
}

// pushSubject is the VAPID contact: the first alert address, or the
// project URL.
func pushSubject(set Settings) string {
	if len(set.EmailTo) > 0 {
		return "mailto:" + set.EmailTo[0]
	}
	return DefaultPushSubject
}
