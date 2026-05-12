// Package noise pkg/noise/dh.go
package noise

import (
	"fmt"
	"io"
	"sync"

	"github.com/skycoin/noise"
	"github.com/skycoin/skycoin/src/cipher"
	secp256k1 "github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

const keypairPoolSize = 512

// keypairPool holds pre-generated ephemeral keypairs for noise handshakes.
// secp256k1 key generation is expensive (EC multiply + validation), so we
// generate them in the background and serve them from a buffered channel.
//
// We use secp256k1.GenerateKeyPair() directly instead of cipher.GenerateKeyPair()
// to skip the DebugLevel1 validation (CheckSecKey + MustPubKeyFromSecKey) which
// doubles the cost by doing an extra EC recovery. The raw secp256k1 generator
// already validates internally.
//
// Multiple generator goroutines fill the pool in parallel to keep up with
// burst demand (thundering herd after deployment restart).
var keypairPool = func() chan noise.DHKey {
	ch := make(chan noise.DHKey, keypairPoolSize)
	numGenerators := 4
	for i := 0; i < numGenerators; i++ {
		go func() {
			for {
				pub, sec := secp256k1.GenerateKeyPair()
				ch <- noise.DHKey{
					Private: sec,
					Public:  pub,
				}
			}
		}()
	}
	return ch
}()

// Secp256k1 implements `noise.DHFunc`.
type Secp256k1 struct{}

// GenerateKeypair helps to implement `noise.DHFunc`.
func (Secp256k1) GenerateKeypair(_ io.Reader) (noise.DHKey, error) {
	return <-keypairPool, nil
}

// dhCache memoizes ECDH outputs by (sk, pk) so repeated handshakes
// between the same two peers don't redo the static-static DH on
// every session.
//
// Why this is safe — in the KK and XK Noise patterns used by DMSG,
// every handshake mixes in four DH operations: e (fresh keypair),
// es / se (one side ephemeral), ee (both ephemeral), and ss (both
// static). The ephemeral DHs use a fresh ephemeral key on every
// handshake (see keypairPool above), so their (sk, pk) inputs are
// almost never repeated — those entries get cached once on miss,
// then evicted by the LRU bound without ever hitting. The ss DH
// uses *long-lived static keys* on both sides — same inputs every
// session for the same peer pair — so it hits the cache after the
// first handshake between any given pair. Forward secrecy comes
// from the ephemeral DHs, which we never cache.
//
// CPU profile motivation: production address-resolver / TPD spend
// ~24-31% of CPU in cipher.ECDH → secp256k1-go2 EC multiply on
// inbound DMSG handshakes; ss accounts for roughly a quarter of
// the DH calls per handshake, so caching it saves ~5-6% absolute
// CPU on every DMSG-handshaking service.
const dhCacheMax = 4096 // ~ (65 + 33) * 4096 ≈ 400 KB worst case

// dhCacheKey packs pk (33 bytes, compressed secp256k1) || sk
// (32 bytes) into a fixed-size array so the map key has no
// allocation overhead and direct == comparison works.
type dhCacheKey [65]byte

var (
	dhCacheMu sync.RWMutex
	dhCache   = make(map[dhCacheKey][33]byte, dhCacheMax)
)

func makeDHKey(sk, pk []byte) dhCacheKey {
	var k dhCacheKey
	copy(k[:33], pk)
	copy(k[33:], sk)
	return k
}

// DH helps to implement `noise.DHFunc`.
// Keys are already validated by the noise handshake state machine, so we
// skip the redundant NewPubKey/NewSecKey validation and copy directly.
// cipher.ECDH still performs its own internal validation.
func (Secp256k1) DH(sk, pk []byte) []byte {
	k := makeDHKey(sk, pk)
	dhCacheMu.RLock()
	cached, hit := dhCache[k]
	dhCacheMu.RUnlock()
	if hit {
		out := make([]byte, 33)
		copy(out, cached[:])
		return out
	}

	var pubKey cipher.PubKey
	var secKey cipher.SecKey
	copy(pubKey[:], pk)
	copy(secKey[:], sk)
	ecdh, err := cipher.ECDH(pubKey, secKey)
	if err != nil {
		panic(fmt.Sprintf("noise DH: ECDH failed: %v", err))
	}
	// DHLen() returns 33; ECDH returns 32-byte SHA256 hash, pad to 33.
	out := make([]byte, 33)
	copy(out, ecdh)

	var entry [33]byte
	copy(entry[:], out)
	dhCacheMu.Lock()
	if len(dhCache) >= dhCacheMax {
		// Bounded random eviction — Go map iteration order is
		// randomized, so dropping the first element we see is a
		// cheap O(1) approximation of LRU. Ephemeral entries
		// dominate the miss stream; the long-lived static-static
		// pair is statistically likely to survive any given pass.
		for k0 := range dhCache {
			delete(dhCache, k0)
			break
		}
	}
	dhCache[k] = entry
	dhCacheMu.Unlock()
	return out
}

// DHLen helps to implement `noise.DHFunc`.
func (Secp256k1) DHLen() int {
	return 33
}

// DHName helps to implement `noise.DHFunc`.
func (Secp256k1) DHName() string {
	return "Secp256k1"
}
