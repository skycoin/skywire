// Package router pkg/router/datagram_crypto.go: per-datagram AEAD
// for DatagramRouteGroup. Stage 3 of #2607.
//
// Stream Noise (what RouteGroup uses) generates a continuous
// keystream that desyncs on lost frames — every subsequent
// decryption fails. UDP drops packets by design, so the datagram
// path needs a cipher that survives loss: each datagram independently
// encrypted, authenticated, replay-protected, AEAD-bound to its
// route + receiver.
//
// Per-datagram AEAD model (matches WireGuard / IPsec ESP / DTLS 1.3
// data-plane):
//
//   - Cipher: ChaCha20-Poly1305-IETF (RFC 7539). Same primitive the
//     existing skywire crypto stack uses for Noise; reusing keeps
//     the dependency surface flat. 256-bit key, 96-bit nonce,
//     128-bit auth tag.
//
//   - Master key: derived from the Noise session master via HKDF-SHA256.
//     One subkey per direction (initiator→responder vs the reverse)
//     and per route within the session.
//
//   - Nonce: 96 bits, constructed as zero-padding(32) || counter(64).
//     Counter is 64-bit, monotonic per-sender, starts at 0 at
//     handshake. On the wire only the 8-byte counter portion is
//     transmitted; the receiver reconstructs the full nonce from
//     the implicit zero-padding. Saves 4 bytes/datagram vs sending
//     the full 12-byte nonce. WireGuard precedent.
//
//   - AAD: routeID (4 bytes, big-endian) || destPK (33 bytes,
//     compressed secp256k1). Binds the sealed datagram to (a) this
//     route within the session and (b) this specific receiver.
//     Cross-route substitution and cross-receiver replay both
//     blocked even if an attacker captures ciphertext from a
//     parallel route and tries to inject it elsewhere.
//
//   - Anti-replay: RFC 6479 sliding window, 2048-bit (32 × uint64
//     bitmap). The counter IS the sequence — no redundant `seq` on
//     the wire. The window's "have I seen this counter" check IS
//     the replay-protection sequence check.
//
//   - Rekey: triggered on time (default 2 min since last rekey) or
//     packet count (default 2^32 packets sent on this direction).
//     Below the 2^60 ChaCha bound by many orders of magnitude;
//     comfortable safety margin. Rekey resets the counter to 0
//     on both sides under the new master.
//
// Wire format of one sealed datagram body (what goes inside a
// DatagramPacket's payload):
//
//   [counter (8 bytes, big-endian uint64)] [ciphertext + 16-byte tag]
//
// Plaintext max = MaxDatagramPayload - 8 (counter) - 16 (tag) = 65511.
// Exported via DatagramCipher.MaxPlaintext() so DatagramRouteGroup.MaxPayload
// can return the right number to callers.

package router

import (
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"

	"github.com/skycoin/skywire/pkg/cipher"
)

// Datagram AEAD wire constants. Public so Stage 4's MaxPayload
// computation can subtract the correct overhead.
const (
	// DatagramCounterSize is the on-wire counter prefix length.
	DatagramCounterSize = 8

	// DatagramTagSize is the Poly1305 authentication tag length.
	DatagramTagSize = chacha20poly1305.Overhead

	// DatagramOverhead is the total per-datagram wire overhead
	// (counter prefix + auth tag). Plaintext + DatagramOverhead is
	// what lands in a DatagramPacket's payload.
	DatagramOverhead = DatagramCounterSize + DatagramTagSize

	// MaxDatagramPlaintext is the largest plaintext datagram body
	// that fits in a single DatagramPacket after AEAD wrapping.
	// Computed from MaxDatagramPayload (the routing-packet payload
	// cap) minus DatagramOverhead.
	MaxDatagramPlaintext = MaxDatagramPayload - DatagramOverhead

	// DefaultRekeyTimeInterval is the time-based rekey trigger.
	// WireGuard uses 2 minutes; matching that for operational
	// familiarity.
	DefaultRekeyTimeInterval = 2 * time.Minute

	// DefaultRekeyPacketLimit is the packet-count rekey trigger.
	// 2^32 = ~4.3B packets, well below the ChaCha 2^60 bound but
	// generous compared to realistic workloads (a 100 Kpps stream
	// for ~12 hours).
	DefaultRekeyPacketLimit uint64 = 1 << 32

	// replayWindowSize is the sliding-window length in bits. RFC
	// 6479 recommends 1024+; WireGuard uses 2048. We match
	// WireGuard.
	replayWindowSize = 2048

	// replayWindowWords is the underlying bitmap length in uint64s.
	replayWindowWords = replayWindowSize / 64
)

// Errors surfaced by DatagramCipher operations.
var (
	// ErrCounterExhausted is returned by Seal when the per-direction
	// counter has reached its rekey limit and no rekey has happened
	// yet. The caller should trigger a rekey and retry.
	ErrCounterExhausted = errors.New("datagram-crypto: counter exhausted, rekey required")

	// ErrAEADAuth is returned by Open when the auth tag does not
	// verify, the counter falls outside the replay window, or the
	// counter was already seen. Indistinguishable to the caller by
	// design — leaking which failure occurred would expose timing
	// signal to an attacker probing the window.
	ErrAEADAuth = errors.New("datagram-crypto: auth failure")

	// ErrCiphertextTooShort is returned when the input to Open is
	// shorter than the minimum (counter prefix + tag).
	ErrCiphertextTooShort = errors.New("datagram-crypto: ciphertext shorter than overhead")

	// ErrPlaintextTooLong is returned when Seal's input exceeds
	// MaxDatagramPlaintext.
	ErrPlaintextTooLong = errors.New("datagram-crypto: plaintext exceeds MaxDatagramPlaintext")
)

// Role identifies which direction's subkey this cipher serves.
// A DatagramRouteGroup holds two ciphers: one for outbound (seal
// with the direction matching the local end's role at handshake)
// and one for inbound (open with the peer's direction).
type Role uint8

const (
	// RoleInitiator is the side that dialed the route.
	RoleInitiator Role = iota
	// RoleResponder is the side that accepted the route.
	RoleResponder
)

// DatagramCipher is the per-direction AEAD state. Two instances
// per DatagramRouteGroup: one for sealing outbound, one for opening
// inbound. They share the same Noise-derived master (Wire-level
// rekey moves both ciphers atomically) but each tracks its own
// counter (outbound) or replay window (inbound).
//
// Single-direction; thread-safe.
type DatagramCipher struct {
	role     Role
	destPK   cipher.PubKey // for AAD binding on outbound; sender's PK on inbound
	aead     atomic.Pointer[cipherEpoch]
	rekeyMu  sync.Mutex // serializes Rekey calls

	// Outbound-only state. The atomic counter is the sender's
	// next nonce; rekey resets it. Tracked separately from inbound
	// so two-direction ciphers can share the cipherEpoch without
	// stomping on each other.
	outCounter uint64 // atomic

	// Inbound-only state. The sliding-window bitmap tracks which
	// counters have been observed in the trailing replayWindowSize
	// values up to top. Protected by inMu.
	inMu     sync.Mutex
	inWindow [replayWindowWords]uint64
	inTop    uint64
}

// cipherEpoch bundles the AEAD instance with the time and packet-
// count thresholds for the current rekey epoch. Replaced atomically
// on Rekey so in-flight Seal/Open calls observe a consistent epoch.
type cipherEpoch struct {
	aead         interface { // chacha20poly1305.New returns this shape
		Seal(dst, nonce, plaintext, additionalData []byte) []byte
		Open(dst, nonce, ciphertext, additionalData []byte) ([]byte, error)
	}
	openedAt     time.Time
	timeLimit    time.Duration
	packetLimit  uint64
}

// NewDatagramCipher constructs an outbound-or-inbound cipher from a
// 32-byte Noise master key, the route's role, the binding PK (the
// destination PK on outbound; the sender's PK on inbound — both go
// into AAD identically). Optional config overrides time/packet
// rekey limits; zero values fall back to defaults.
func NewDatagramCipher(masterKey [32]byte, role Role, bindPK cipher.PubKey, cfg *DatagramCipherConfig) (*DatagramCipher, error) {
	dc := &DatagramCipher{role: role, destPK: bindPK}
	if err := dc.installEpoch(masterKey, cfg); err != nil {
		return nil, err
	}
	return dc, nil
}

// DatagramCipherConfig tunes the rekey thresholds. Zero values use
// the package defaults (2 min / 2^32 packets).
type DatagramCipherConfig struct {
	TimeLimit   time.Duration
	PacketLimit uint64
}

func (dc *DatagramCipher) installEpoch(masterKey [32]byte, cfg *DatagramCipherConfig) error {
	// HKDF-SHA256 expansion: one direction-tagged subkey per role
	// so initiator's outbound key ≠ responder's outbound key even
	// from the same master.
	info := []byte("skywire-datagram-v1-init")
	if dc.role == RoleResponder {
		info = []byte("skywire-datagram-v1-resp")
	}
	h := hkdf.New(sha256.New, masterKey[:], nil, info)
	subkey := make([]byte, chacha20poly1305.KeySize)
	if _, err := io.ReadFull(h, subkey); err != nil {
		return fmt.Errorf("datagram-crypto: hkdf: %w", err)
	}
	aead, err := chacha20poly1305.New(subkey)
	if err != nil {
		return fmt.Errorf("datagram-crypto: new chacha: %w", err)
	}

	tl := DefaultRekeyTimeInterval
	pl := DefaultRekeyPacketLimit
	if cfg != nil {
		if cfg.TimeLimit > 0 {
			tl = cfg.TimeLimit
		}
		if cfg.PacketLimit > 0 {
			pl = cfg.PacketLimit
		}
	}

	dc.aead.Store(&cipherEpoch{
		aead:        aead,
		openedAt:    time.Now(),
		timeLimit:   tl,
		packetLimit: pl,
	})
	atomic.StoreUint64(&dc.outCounter, 0)
	dc.inMu.Lock()
	for i := range dc.inWindow {
		dc.inWindow[i] = 0
	}
	dc.inTop = 0
	dc.inMu.Unlock()
	return nil
}

// Rekey replaces the AEAD epoch with one derived from a fresh
// master key. The counter resets to 0; the replay window clears.
// Both sides must perform the swap in lockstep (driven by the
// outer route-setup machinery, not by this type).
func (dc *DatagramCipher) Rekey(masterKey [32]byte, cfg *DatagramCipherConfig) error {
	dc.rekeyMu.Lock()
	defer dc.rekeyMu.Unlock()
	return dc.installEpoch(masterKey, cfg)
}

// NeedsRekey reports whether the current epoch has exceeded the
// time-based or packet-count-based rekey trigger. The outer
// scheduling layer polls this and initiates a Noise rekey when it
// fires. Idempotent — calling does not change state.
func (dc *DatagramCipher) NeedsRekey() bool {
	ep := dc.aead.Load()
	if ep == nil {
		return false
	}
	if time.Since(ep.openedAt) >= ep.timeLimit {
		return true
	}
	if atomic.LoadUint64(&dc.outCounter) >= ep.packetLimit {
		return true
	}
	return false
}

// MaxPlaintext returns the largest plaintext datagram body that
// Seal will accept. Surfaced via DatagramRouteGroup.MaxPayload to
// callers.
func (dc *DatagramCipher) MaxPlaintext() int {
	return MaxDatagramPlaintext
}

// Seal AEAD-encrypts plaintext and prepends the 8-byte counter.
// Output: [counter (8)] [ciphertext + 16-byte tag]. AAD is
// constructed from routeID + dc.destPK. Returns ErrCounterExhausted
// if the counter has reached the rekey limit — the caller should
// trigger Rekey and retry.
func (dc *DatagramCipher) Seal(routeID uint32, plaintext []byte) ([]byte, error) {
	if len(plaintext) > MaxDatagramPlaintext {
		return nil, ErrPlaintextTooLong
	}
	ep := dc.aead.Load()
	if ep == nil {
		return nil, errors.New("datagram-crypto: cipher uninitialised")
	}
	counter := atomic.AddUint64(&dc.outCounter, 1) - 1 // first nonce = 0
	if counter >= ep.packetLimit {
		// Roll back — we didn't consume a counter the receiver
		// could ever observe.
		atomic.AddUint64(&dc.outCounter, ^uint64(0))
		return nil, ErrCounterExhausted
	}

	var nonce [chacha20poly1305.NonceSize]byte
	binary.BigEndian.PutUint64(nonce[4:], counter)

	aad := makeAAD(routeID, dc.destPK)

	out := make([]byte, DatagramCounterSize, DatagramCounterSize+len(plaintext)+DatagramTagSize)
	binary.BigEndian.PutUint64(out, counter)
	out = ep.aead.Seal(out, nonce[:], plaintext, aad)
	return out, nil
}

// Open verifies and decrypts a sealed datagram body. Input format
// matches Seal's output. Returns ErrAEADAuth on any of: tag mismatch,
// counter outside the replay window, counter already observed.
// Indistinguishable failure modes are intentional — leaking which
// failed would expose timing signal to an attacker.
//
// On success the counter is recorded in the replay window so the
// same datagram cannot be replayed within the window.
func (dc *DatagramCipher) Open(routeID uint32, sealed []byte) ([]byte, error) {
	if len(sealed) < DatagramOverhead {
		return nil, ErrCiphertextTooShort
	}
	ep := dc.aead.Load()
	if ep == nil {
		return nil, errors.New("datagram-crypto: cipher uninitialised")
	}

	counter := binary.BigEndian.Uint64(sealed[:DatagramCounterSize])
	ct := sealed[DatagramCounterSize:]

	// Pre-AEAD replay check: cheap reject for counters well outside
	// the window. AEAD check follows; only commit the window
	// update if auth succeeds (otherwise a forged packet could
	// poison the window).
	if !dc.replayPrecheck(counter) {
		return nil, ErrAEADAuth
	}

	var nonce [chacha20poly1305.NonceSize]byte
	binary.BigEndian.PutUint64(nonce[4:], counter)
	aad := makeAAD(routeID, dc.destPK)

	pt, err := ep.aead.Open(nil, nonce[:], ct, aad)
	if err != nil {
		return nil, ErrAEADAuth
	}
	if !dc.replayCommit(counter) {
		// Lost a race with another goroutine that committed the
		// same counter first — treat as replay.
		return nil, ErrAEADAuth
	}
	return pt, nil
}

// replayPrecheck returns false if the counter is definitely
// outside the window (too old) or already observed. A true return
// means the counter passes the cheap check; final commit happens
// only after AEAD authentication succeeds.
func (dc *DatagramCipher) replayPrecheck(counter uint64) bool {
	dc.inMu.Lock()
	defer dc.inMu.Unlock()
	if counter+replayWindowSize <= dc.inTop {
		return false // beyond the trailing edge
	}
	if counter <= dc.inTop {
		// Inside the window — check the bit.
		offset := dc.inTop - counter
		word := (replayWindowSize - 1 - offset) / 64
		bit := uint64(1) << ((replayWindowSize - 1 - offset) % 64)
		return dc.inWindow[word]&bit == 0
	}
	return true // ahead of inTop — always fresh
}

// replayCommit marks the counter as observed. Returns false if a
// concurrent goroutine committed the same counter between
// precheck and commit (effectively treats the race-loser as a
// replay so we don't deliver the same datagram twice).
func (dc *DatagramCipher) replayCommit(counter uint64) bool {
	dc.inMu.Lock()
	defer dc.inMu.Unlock()
	if counter > dc.inTop {
		shift := counter - dc.inTop
		if shift >= replayWindowSize {
			// Slide reset — far ahead of previous top.
			for i := range dc.inWindow {
				dc.inWindow[i] = 0
			}
		} else {
			// Shift the bitmap left by `shift` bits.
			shiftLeft(&dc.inWindow, uint(shift))
		}
		dc.inTop = counter
		// Mark the newly-arrived top bit.
		bit := uint64(1) << ((replayWindowSize - 1) % 64)
		dc.inWindow[(replayWindowSize-1)/64] &^= bit // clear any stale
		// Actually we want to set the bit at the position of the
		// new top, which after shift is at the most-significant
		// edge.
		dc.inWindow[replayWindowWords-1] |= 1
		return true
	}
	// counter <= dc.inTop and within window per precheck.
	offset := dc.inTop - counter
	word := (replayWindowSize - 1 - offset) / 64
	bit := uint64(1) << ((replayWindowSize - 1 - offset) % 64)
	if dc.inWindow[word]&bit != 0 {
		return false // raced
	}
	dc.inWindow[word] |= bit
	return true
}

// shiftLeft shifts the bitmap left by n bits (n < replayWindowSize).
// "Left" here means toward the most-significant bit, which is the
// most-recent end of the window — older counters fall off the
// least-significant end.
func shiftLeft(w *[replayWindowWords]uint64, n uint) {
	wordShift := n / 64
	bitShift := n % 64
	if wordShift > 0 {
		for i := 0; i < replayWindowWords; i++ {
			src := i + int(wordShift)
			if src < replayWindowWords {
				w[i] = w[src]
			} else {
				w[i] = 0
			}
		}
	}
	if bitShift > 0 {
		for i := 0; i < replayWindowWords-1; i++ {
			w[i] = (w[i] << bitShift) | (w[i+1] >> (64 - bitShift))
		}
		w[replayWindowWords-1] <<= bitShift
	}
}

// makeAAD constructs the AEAD associated-data block for a sealed
// datagram. routeID is big-endian uint32; destPK is the 33-byte
// compressed secp256k1 PK exactly as it appears on the wire.
func makeAAD(routeID uint32, destPK cipher.PubKey) []byte {
	out := make([]byte, 4+len(destPK))
	binary.BigEndian.PutUint32(out, routeID)
	copy(out[4:], destPK[:])
	return out
}
