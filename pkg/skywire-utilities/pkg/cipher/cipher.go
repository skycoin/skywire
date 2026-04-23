// Package cipher implements common golang encoding interfaces for
// github.com/skycoin/skycoin/src/cipher
package cipher

import (
	"bytes"
	"fmt"
	"math/big"
	"strings"
	"sync"

	"github.com/skycoin/skycoin/src/cipher"
	"github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

// verifyCache caches recent signature verification results to avoid
// repeated expensive secp256k1 RecoverPublicKey operations. The setup
// node verifies the same PK's stream response signatures thousands of
// times per minute — caching saves ~20% CPU.
var (
	verifyCacheMu   sync.RWMutex
	verifyCacheMap  = make(map[[98]byte]struct{}, 1024) // key: PK(33) + Sig(65) = 98 bytes
	verifyCacheSize int
)

const maxVerifyCacheSize = 4096

func verifyCacheKey(pk cipher.PubKey, sig cipher.Sig) [98]byte {
	var key [98]byte
	copy(key[:33], pk[:])
	copy(key[33:], sig[:])
	return key
}

func verifyCacheCheck(pk cipher.PubKey, sig cipher.Sig) bool {
	key := verifyCacheKey(pk, sig)
	verifyCacheMu.RLock()
	_, ok := verifyCacheMap[key]
	verifyCacheMu.RUnlock()
	return ok
}

func verifyCacheStore(pk cipher.PubKey, sig cipher.Sig) {
	key := verifyCacheKey(pk, sig)
	verifyCacheMu.Lock()
	if verifyCacheSize >= maxVerifyCacheSize {
		// Simple eviction: clear entire cache.
		verifyCacheMap = make(map[[98]byte]struct{}, 1024)
		verifyCacheSize = 0
	}
	verifyCacheMap[key] = struct{}{}
	verifyCacheSize++
	verifyCacheMu.Unlock()
}

func init() {
	cipher.DebugLevel2 = false // DebugLevel2 causes ECDH to be really slow
}

// GenerateKeyPair creates key pair
func GenerateKeyPair() (PubKey, SecKey) {
	pk, sk := cipher.GenerateKeyPair()
	return PubKey(pk), SecKey(sk)
}

// GenerateDeterministicKeyPair generates deterministic key pair
func GenerateDeterministicKeyPair(seed []byte) (PubKey, SecKey, error) {
	pk, sk, err := cipher.GenerateDeterministicKeyPair(seed)
	return PubKey(pk), SecKey(sk), err
}

// NewPubKey converts []byte to a PubKey
func NewPubKey(b []byte) (PubKey, error) {
	pk, err := cipher.NewPubKey(b)
	return PubKey(pk), err
}

// PubKey is a wrapper type for cipher.PubKey that implements common
// golang interfaces.
type PubKey cipher.PubKey

// Hex returns a hex encoded PubKey string
func (pk PubKey) Hex() string {
	return cipher.PubKey(pk).Hex()
}

// Null returns true if PubKey is the null PubKey
func (pk PubKey) Null() bool {
	return cipher.PubKey(pk).Null()
}

// String implements fmt.Stringer for PubKey. Returns Hex representation.
func (pk PubKey) String() string {
	return pk.Hex()
}

// Big returns the big.Int representation of the public key.
func (pk PubKey) Big() *big.Int {
	return new(big.Int).SetBytes(pk[:])
}

// Set implements pflag.Value for PubKey.
func (pk *PubKey) Set(s string) error {
	cPK, err := cipher.PubKeyFromHex(s)
	if err != nil {
		return err
	}
	*pk = PubKey(cPK)
	return nil
}

// Type implements pflag.Value for PubKey.
func (pk PubKey) Type() string {
	return "cipher.PubKey"
}

// MarshalText implements encoding.TextMarshaler.
func (pk PubKey) MarshalText() ([]byte, error) {
	return []byte(pk.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (pk *PubKey) UnmarshalText(data []byte) error {
	if bytes.Count(data, []byte("0")) == len(data) {
		return nil
	}

	dPK, err := cipher.PubKeyFromHex(string(data))
	if err == nil {
		*pk = PubKey(dPK)
	}
	return err
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (pk PubKey) MarshalBinary() ([]byte, error) {
	return pk[:], nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (pk *PubKey) UnmarshalBinary(data []byte) error {
	dPK, err := cipher.NewPubKey(data)
	if err == nil {
		*pk = PubKey(dPK)
	}
	return err
}

// PubKeys represents a slice of PubKeys.
type PubKeys []PubKey

// String implements stringer for PubKeys.
func (p PubKeys) String() string {
	res := "public keys:\n"
	for _, pk := range p {
		res += fmt.Sprintf("\t%s\n", pk)
	}
	return res
}

// Set implements pflag.Value for PubKeys.
func (p *PubKeys) Set(list string) error {
	*p = PubKeys{}
	for _, s := range strings.Split(list, ",") {
		var pk PubKey
		if err := pk.Set(strings.TrimSpace(s)); err != nil {
			return err
		}
		*p = append(*p, pk)
	}
	return nil
}

// Type implements pflag.Value for PubKeys.
func (p PubKeys) Type() string {
	return "cipher.PubKeys"
}

// SecKey is a wrapper type for cipher.SecKey that implements common
// golang interfaces.
type SecKey cipher.SecKey

// Hex returns a hex encoded SecKey string
func (sk SecKey) Hex() string {
	return cipher.SecKey(sk).Hex()
}

// Null returns true if SecKey is the null SecKey.
func (sk SecKey) Null() bool {
	return cipher.SecKey(sk).Null()
}

// String implements fmt.Stringer for SecKey. Returns Hex representation.
func (sk SecKey) String() string {
	return sk.Hex()
}

// Set implements pflag.Value for SecKey.
func (sk *SecKey) Set(s string) error {
	cSK, err := cipher.SecKeyFromHex(s)
	if err != nil {
		return err
	}
	*sk = SecKey(cSK)
	return nil
}

// Type implements pflag.Value for SecKey.
func (sk *SecKey) Type() string {
	return "cipher.SecKey"
}

// MarshalText implements encoding.TextMarshaler.
func (sk SecKey) MarshalText() ([]byte, error) {
	return []byte(sk.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (sk *SecKey) UnmarshalText(data []byte) error {
	dSK, err := cipher.SecKeyFromHex(string(data))
	if err == nil {
		*sk = SecKey(dSK)
	}
	return err
}

// MarshalBinary implements encoding.BinaryMarshaler.
func (sk SecKey) MarshalBinary() ([]byte, error) {
	return sk[:], nil
}

// UnmarshalBinary implements encoding.BinaryUnmarshaler.
func (sk *SecKey) UnmarshalBinary(data []byte) error {
	dSK, err := cipher.NewSecKey(data)
	if err == nil {
		*sk = SecKey(dSK)
	}
	return err
}

// PubKey recovers the public key for a secret key
func (sk SecKey) PubKey() (PubKey, error) {
	pk, err := cipher.PubKeyFromSecKey(cipher.SecKey(sk))
	return PubKey(pk), err
}

// Sig is a wrapper type for cipher.Sig that implements common golang interfaces.
type Sig cipher.Sig

// Hex returns a hex encoded Sig string
func (sig Sig) Hex() string {
	return cipher.Sig(sig).Hex()
}

// String implements fmt.Stringer for Sig. Returns Hex representation.
func (sig Sig) String() string {
	return sig.Hex()
}

// Null returns true if Sig is a null Sig
func (sig Sig) Null() bool {
	return sig == Sig{}
}

// MarshalText implements encoding.TextMarshaler.
func (sig Sig) MarshalText() ([]byte, error) {
	return []byte(sig.Hex()), nil
}

// UnmarshalText implements encoding.TextUnmarshaler.
func (sig *Sig) UnmarshalText(data []byte) error {
	dSig, err := cipher.SigFromHex(string(data))
	if err == nil {
		*sig = Sig(dSig)
	}
	return err
}

// SignPayload creates Sig for payload using SHA256
func SignPayload(payload []byte, sec SecKey) (Sig, error) {
	sig, err := cipher.SignHash(cipher.SumSHA256(payload), cipher.SecKey(sec))
	return Sig(sig), err
}

// VerifyPubKeySignedPayload verifies that SHA256 hash of the payload was signed by PubKey
func VerifyPubKeySignedPayload(pubkey PubKey, sig Sig, payload []byte) error {
	return VerifyPubKeySignedHashLight(cipher.PubKey(pubkey), cipher.Sig(sig), cipher.SumSHA256(payload))
}

// RandByte returns rand N bytes
func RandByte(n int) []byte {
	return cipher.RandByte(n)
}

// SHA256 is a wrapper type for cipher.SHA256 that implements common
// golang interfaces.
type SHA256 cipher.SHA256

// SHA256FromBytes converts []byte to SHA256
func SHA256FromBytes(b []byte) (SHA256, error) {
	h, err := cipher.SHA256FromBytes(b)
	return SHA256(h), err
}

// SumSHA256 sum sha256
func SumSHA256(b []byte) SHA256 {
	return SHA256(cipher.SumSHA256(b))
}

// VerifyPubKeySignedHashLight uses standard Skycoin implementation
// This is your original optimized version that skips pubkey recovery
func VerifyPubKeySignedHashLight(pubkey cipher.PubKey, sig cipher.Sig, hash cipher.SHA256) error {
	// Check cache first — avoids expensive secp256k1 operations for
	// recently verified (PK, sig) pairs.
	if verifyCacheCheck(pubkey, sig) {
		return nil
	}

	// Validate pubkey format (fast)
	if secp256k1.VerifyPubkey(pubkey[:]) != 1 {
		return cipher.ErrInvalidSigInvalidPubKey
	}

	// Validate signature format (fast)
	if secp256k1.VerifySignatureValidity(sig[:]) != 1 {
		return cipher.ErrInvalidSigValidity
	}

	// Verify signature (expensive)
	if secp256k1.VerifySignature(hash[:], sig[:], pubkey[:]) != 1 {
		return cipher.ErrInvalidSigForMessage
	}

	verifyCacheStore(pubkey, sig)
	return nil
}
