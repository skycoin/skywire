// Package visor pkg/visor/pairing_hv.go c3-vis-api
//
// Hypervisor pairing (#4484 stage 5). A peer that holds a same-origin
// transport to this visor (a desk tab the hypervisor serves) or that tried the
// transport RPC without being whitelisted is a PENDING hypervisor: listed by
// fingerprint for `skywire cli visor hv pair`, and approved either there or
// with a one-time code the operator makes on the board and types into the
// tab. Approval is the existing AddHypervisor path, so the host dials the
// peer as its hypervisor and lets it drive RPC and pty — and nothing else
// grants that.
package visor

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/skycoin/skywire/pkg/cipher"
	"github.com/skycoin/skywire/pkg/transport"
	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

const (
	// pairPendingMax bounds the pending list; the oldest entry goes first.
	pairPendingMax = 64
	// pairCodeLen is the one-time code length; the alphabet omits 0/O/1/I.
	pairCodeLen      = 8
	pairCodeAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// pairCodeDefaultTTL is how long a code stays valid unless the CLI says.
	pairCodeDefaultTTL = 10 * time.Minute
	// pairCodeMaxTries is the number of wrong codes accepted before every
	// outstanding code is revoked (a new one has to be made on the board).
	pairCodeMaxTries = 5
)

// ErrPairCodeInvalid is returned for any code that does not pair: unknown,
// expired, already used, or revoked. Deliberately one error.
var ErrPairCodeInvalid = errors.New("pairing: invalid code")

// ErrNoPendingMatch is returned when a selection matches no pending key.
var ErrNoPendingMatch = errors.New("pairing: no pending key matches")

// PendingHypervisor is a peer that asked to drive this visor but is not yet
// in its hypervisor list.
type PendingHypervisor struct {
	PK          cipher.PubKey `json:"pk"`
	Fingerprint string        `json:"fingerprint"`
	FirstSeen   time.Time     `json:"first_seen"`
	LastSeen    time.Time     `json:"last_seen"`
	// Via is how the peer was noticed: "transport" (it holds a same-origin
	// transport to us) or "rpc" (it tried the transport RPC and was refused).
	Via string `json:"via"`
}

// PairCode is a one-time pairing code and when it stops working.
type PairCode struct {
	Code    string    `json:"code"`
	Expires time.Time `json:"expires"`
}

// PairStatus is the pre-auth view a tab gets of its own standing.
type PairStatus struct {
	PK          cipher.PubKey `json:"pk"`
	Fingerprint string        `json:"fingerprint"`
	Paired      bool          `json:"paired"`
	Pending     bool          `json:"pending"`
}

// HypervisorFingerprint is the short, stable name a pending key is approved
// by: the first 40 bits of sha256(pk) as two hex groups ("a1b2c-3d4e5").
func HypervisorFingerprint(pk cipher.PubKey) string {
	sum := sha256.Sum256(pk[:])
	s := hex.EncodeToString(sum[:5])
	return s[:5] + "-" + s[5:]
}

type hvPairing struct {
	mu      sync.Mutex
	pending map[cipher.PubKey]*PendingHypervisor
	codes   map[string]time.Time // code → expiry
	tries   int
}

func newHVPairing() *hvPairing {
	return &hvPairing{
		pending: make(map[cipher.PubKey]*PendingHypervisor),
		codes:   make(map[string]time.Time),
	}
}

// note records pk as pending (or refreshes it). Returns true when new.
func (p *hvPairing) note(pk cipher.PubKey, via string, now time.Time) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if e, ok := p.pending[pk]; ok {
		e.LastSeen = now
		if via == "rpc" {
			e.Via = via // an RPC attempt is the stronger signal
		}
		return false
	}
	for len(p.pending) >= pairPendingMax {
		var oldest cipher.PubKey
		var oldestAt time.Time
		for k, e := range p.pending {
			if oldestAt.IsZero() || e.LastSeen.Before(oldestAt) {
				oldest, oldestAt = k, e.LastSeen
			}
		}
		delete(p.pending, oldest)
	}
	p.pending[pk] = &PendingHypervisor{
		PK: pk, Fingerprint: HypervisorFingerprint(pk), FirstSeen: now, LastSeen: now, Via: via,
	}
	return true
}

func (p *hvPairing) forget(pk cipher.PubKey) {
	p.mu.Lock()
	delete(p.pending, pk)
	p.mu.Unlock()
}

func (p *hvPairing) isPending(pk cipher.PubKey) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	_, ok := p.pending[pk]
	return ok
}

func (p *hvPairing) list() []PendingHypervisor {
	p.mu.Lock()
	out := make([]PendingHypervisor, 0, len(p.pending))
	for _, e := range p.pending {
		out = append(out, *e)
	}
	p.mu.Unlock()
	sort.Slice(out, func(i, j int) bool { return out[i].LastSeen.After(out[j].LastSeen) })
	return out
}

// resolve turns a selection — a full public key, or a fingerprint or unique
// fingerprint prefix of a pending key — into the key.
func (p *hvPairing) resolve(sel string) (cipher.PubKey, error) {
	sel = strings.ToLower(strings.TrimSpace(sel))
	if sel == "" {
		return cipher.PubKey{}, ErrNoPendingMatch
	}
	var pk cipher.PubKey
	if err := pk.Set(sel); err == nil {
		return pk, nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	var found *PendingHypervisor
	for _, e := range p.pending {
		if e.Fingerprint == sel || strings.HasPrefix(e.Fingerprint, sel) {
			if found != nil {
				return cipher.PubKey{}, fmt.Errorf("pairing: %q matches more than one pending key", sel)
			}
			found = e
		}
	}
	if found == nil {
		return cipher.PubKey{}, ErrNoPendingMatch
	}
	return found.PK, nil
}

func (p *hvPairing) newCode(ttl time.Duration, now time.Time) (PairCode, error) {
	if ttl <= 0 {
		ttl = pairCodeDefaultTTL
	}
	buf := make([]byte, pairCodeLen)
	if _, err := rand.Read(buf); err != nil {
		return PairCode{}, err
	}
	code := make([]byte, pairCodeLen)
	for i, b := range buf {
		code[i] = pairCodeAlphabet[int(b)%len(pairCodeAlphabet)]
	}
	c := PairCode{Code: string(code), Expires: now.Add(ttl)}
	p.mu.Lock()
	p.pruneLocked(now)
	p.codes[c.Code] = c.Expires
	p.tries = 0
	p.mu.Unlock()
	return c, nil
}

// consume checks code (case- and separator-insensitive) against the
// outstanding codes; a match is used up. Wrong codes count towards revoking
// every code.
func (p *hvPairing) consume(code string, now time.Time) error {
	norm := normalizePairCode(code)
	p.mu.Lock()
	defer p.mu.Unlock()
	p.pruneLocked(now)
	var matched string
	for c := range p.codes {
		if subtle.ConstantTimeCompare([]byte(c), []byte(norm)) == 1 {
			matched = c
		}
	}
	if matched == "" {
		p.tries++
		if p.tries >= pairCodeMaxTries {
			p.codes = make(map[string]time.Time)
			p.tries = 0
		}
		return ErrPairCodeInvalid
	}
	delete(p.codes, matched)
	p.tries = 0
	return nil
}

func (p *hvPairing) pruneLocked(now time.Time) {
	for c, exp := range p.codes {
		if !exp.After(now) {
			delete(p.codes, c)
		}
	}
}

func normalizePairCode(code string) string {
	var b strings.Builder
	for _, r := range strings.ToUpper(code) {
		if r == ' ' || r == '-' || r == '_' {
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// hvPairingState returns the hypervisor pairing state, made on first use.
func (v *Visor) hvPairingState() *hvPairing {
	v.hvPairOnce.Do(func() { v.hvPair = newHVPairing() })
	return v.hvPair
}

// isTrustedPeer reports whether pk already drives this visor: itself, a
// configured hypervisor, or a whitelisted pty/RPC peer.
func (v *Visor) isTrustedPeer(pk cipher.PubKey) bool {
	if v.conf != nil && pk == v.conf.PK {
		return true
	}
	for _, hv := range v.configuredHypervisors() {
		if hv == pk {
			return true
		}
	}
	if v.peerWhitelist != nil {
		if ok, err := v.peerWhitelist.Get(pk); err == nil && ok {
			return true
		}
	}
	return false
}

// notePendingHypervisor records a peer that wants to drive this visor and is
// not yet allowed to. via names how it was noticed.
func (v *Visor) notePendingHypervisor(pk cipher.PubKey, via string) {
	if pk.Null() || v.isTrustedPeer(pk) {
		return
	}
	if v.hvPairingState().note(pk, via, time.Now()) {
		v.log.WithField("pk", pk.String()).WithField("fingerprint", HypervisorFingerprint(pk)).
			WithField("via", via).Info("Pending hypervisor: approve with `skywire cli visor hv pair <fingerprint>`")
	}
}

// scanPendingPairs notices peers holding a same-origin (swsr, tptypes.WS) transport to
// this visor that are not yet trusted — a desk tab this hypervisor serves.
func (v *Visor) scanPendingPairs() {
	if v.tpM == nil {
		return
	}
	v.tpM.WalkTransports(func(mt *transport.ManagedTransport) bool {
		if mt.Type() == tptypes.WS && !mt.IsClosed() {
			v.notePendingHypervisor(mt.Remote(), "transport")
		}
		return true
	})
}

// PendingHypervisors implements API: the peers waiting to be approved.
func (v *Visor) PendingHypervisors() ([]PendingHypervisor, error) {
	return v.hvPairingState().list(), nil
}

// ApproveHypervisor implements API: sel is a public key, or a fingerprint (or
// unique prefix) of a pending key. Approval is AddHypervisor.
func (v *Visor) ApproveHypervisor(sel string) (cipher.PubKey, error) {
	pk, err := v.hvPairingState().resolve(sel)
	if err != nil {
		return cipher.PubKey{}, err
	}
	if err := v.AddHypervisor(pk); err != nil && !strings.Contains(err.Error(), "already connected") {
		return pk, err
	}
	return pk, nil
}

// NewPairCode implements API: a one-time code a tab can present to be
// approved without the CLI seeing its fingerprint.
func (v *Visor) NewPairCode(ttl time.Duration) (PairCode, error) {
	return v.hvPairingState().newCode(ttl, time.Now())
}

// PairWithCode approves pk if code is an outstanding one-time code.
func (v *Visor) PairWithCode(pk cipher.PubKey, code string) error {
	if pk.Null() {
		return ErrPairCodeInvalid
	}
	if v.isTrustedPeer(pk) {
		return nil
	}
	if err := v.hvPairingState().consume(code, time.Now()); err != nil {
		v.log.WithField("pk", pk.String()).Warn("Pairing: rejected code")
		return err
	}
	if err := v.AddHypervisor(pk); err != nil && !strings.Contains(err.Error(), "already connected") {
		return err
	}
	return nil
}

// PairStatusOf reports whether pk is paired (trusted) or pending.
func (v *Visor) PairStatusOf(pk cipher.PubKey) PairStatus {
	return PairStatus{
		PK: pk, Fingerprint: HypervisorFingerprint(pk),
		Paired:  v.isTrustedPeer(pk),
		Pending: v.hvPairingState().isPending(pk),
	}
}
