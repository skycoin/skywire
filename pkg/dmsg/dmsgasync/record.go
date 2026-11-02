// Package dmsgasync pkg/dmsg/dmsgasync/record.go c1-net-dmsg
package dmsgasync

import (
	"encoding/json"
	"fmt"
	"slices"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Record is a recipient's signed statement of where to deposit for it. It
// names hosts, not senders, so publishing it does not reveal its contacts.
type Record struct {
	PK         cipher.PubKey   `json:"pk"`
	Hosts      []cipher.PubKey `json:"hosts"`
	MaxMessage int             `json:"max_message"`
	Seq        uint64          `json:"seq"`
	Expires    time.Time       `json:"expires"`
	Sig        cipher.Sig      `json:"sig"`
}

// signedPart is what Sig covers: every field but Sig, in a fixed order.
func (r Record) signedPart() ([]byte, error) {
	return json.Marshal(struct {
		PK         cipher.PubKey   `json:"pk"`
		Hosts      []cipher.PubKey `json:"hosts"`
		MaxMessage int             `json:"max_message"`
		Seq        uint64          `json:"seq"`
		Expires    int64           `json:"expires"`
	}{r.PK, r.Hosts, r.MaxMessage, r.Seq, r.Expires.UTC().Unix()})
}

// Sign fills Sig with sk, which must be the key of r.PK.
func (r *Record) Sign(sk cipher.SecKey) error {
	b, err := r.signedPart()
	if err != nil {
		return err
	}
	sig, err := cipher.SignPayload(b, sk)
	if err != nil {
		return err
	}
	r.Sig = sig
	return nil
}

// Verify checks the signature, the bounds and the expiry at now.
func (r Record) Verify(now time.Time) error {
	if r.PK.Null() {
		return fmt.Errorf("%w: no key", ErrBadRecord)
	}
	if len(r.Hosts) > MaxRecordHosts {
		return fmt.Errorf("%w: %d hosts, at most %d", ErrBadRecord, len(r.Hosts), MaxRecordHosts)
	}
	if r.MaxMessage < 0 || r.MaxMessage > MaxMessageLimit {
		return fmt.Errorf("%w: max message %d", ErrBadRecord, r.MaxMessage)
	}
	if !now.Before(r.Expires) {
		return fmt.Errorf("%w: expired", ErrBadRecord)
	}
	if r.Expires.Sub(now) > MaxRecordLifetime+time.Hour {
		return fmt.Errorf("%w: expires too far ahead", ErrBadRecord)
	}
	b, err := r.signedPart()
	if err != nil {
		return err
	}
	if err := cipher.VerifyPubKeySignedPayload(r.PK, r.Sig, b); err != nil {
		return fmt.Errorf("%w: %v", ErrBadRecord, err)
	}
	return nil
}

// Names reports whether host is one of r's hosts.
func (r Record) Names(host cipher.PubKey) bool {
	return slices.Contains(r.Hosts, host)
}

// NewRecord builds and signs a record for pk.
func NewRecord(pk cipher.PubKey, sk cipher.SecKey, hosts []cipher.PubKey, maxMessage int, seq uint64, lifetime time.Duration) (Record, error) {
	if lifetime <= 0 || lifetime > MaxRecordLifetime {
		lifetime = MaxRecordLifetime
	}
	r := Record{
		PK:         pk,
		Hosts:      append([]cipher.PubKey(nil), hosts...),
		MaxMessage: maxMessage,
		Seq:        seq,
		Expires:    time.Now().Add(lifetime).UTC().Truncate(time.Second),
	}
	return r, r.Sign(sk)
}

// Policy is the recipient's rule for who may deposit. Only its hosts see it.
type Policy struct {
	// Allow lists the senders that may deposit. Empty allows every key.
	Allow []cipher.PubKey `json:"allow,omitempty"`
	// Deny lists senders that may never deposit.
	Deny []cipher.PubKey `json:"deny,omitempty"`
	// MaxPerSender bounds what one sender may have waiting. Zero takes
	// DefaultMaxPerSender.
	MaxPerSender int `json:"max_per_sender,omitempty"`
}

// Allows reports whether sender may deposit.
func (p Policy) Allows(sender cipher.PubKey) bool {
	if slices.Contains(p.Deny, sender) {
		return false
	}
	return len(p.Allow) == 0 || slices.Contains(p.Allow, sender)
}

func (p Policy) perSender() int {
	if p.MaxPerSender <= 0 {
		return DefaultMaxPerSender
	}
	return p.MaxPerSender
}
