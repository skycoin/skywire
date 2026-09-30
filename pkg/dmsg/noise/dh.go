// Package noise pkg/dmsg/noise/dh.go c1-net-dmsg
package noise

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"sync"
	"sync/atomic"

	"github.com/flynn/noise"
	"github.com/skycoin/skycoin/src/cipher"
	secp256k1 "github.com/skycoin/skycoin/src/cipher/secp256k1-go"
)

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
//
// keypairPoolSize and keypairGenerators are build-tagged — see
// dh_pool_native.go and dh_pool_js.go. "In parallel" is only true off the
// browser: js/wasm is GOMAXPROCS=1, so extra generators there take turns with
// the boot path rather than adding throughput.
var keypairPool = func() chan noise.DHKey {
	ch := make(chan noise.DHKey, keypairPoolSize)
	for i := 0; i < keypairGenerators; i++ {
		go func() {
			for {
				ch <- generateDHKey()
			}
		}()
	}
	return ch
}()

// generateDHKey wraps secp256k1.GenerateKeyPair, retrying on the rare panic it
// raises from its internal self-test ("IMPOSSIBLE4: pubkey failed" and friends
// in skycoin's pure-Go EC arithmetic) for certain random secret keys. The panic
// is non-deterministic and key-specific — observed on Windows — and would
// otherwise crash the whole process from this background goroutine. A fresh key
// on the next iteration succeeds, so recover and retry instead of dying.
func generateDHKey() noise.DHKey {
	for {
		if key, ok := tryGenerateDHKey(); ok {
			return key
		}
	}
}

func tryGenerateDHKey() (key noise.DHKey, ok bool) {
	defer func() {
		if r := recover(); r != nil {
			ok = false
		}
	}()
	pub, sec := secp256k1.GenerateKeyPair()
	return noise.DHKey{Private: sec, Public: pub}, true
}

// Secp256k1 implements `noise.DHFunc`. The zero value computes every DH.
// Built with the handshake's own static secret key and the peer's static
// public key (see New), it caches the one DH that repeats between the same
// two peers: static-static.
type Secp256k1 struct {
	staticSK   []byte
	peerStatic []byte
}

// GenerateKeypair helps to implement `noise.DHFunc`.
func (Secp256k1) GenerateKeypair(_ io.Reader) (noise.DHKey, error) {
	return <-keypairPool, nil
}

// dhCache memoizes the static-static DH (the KK pattern's ss) by (sk, pk),
// so repeated stream handshakes between the same two peers don't redo it.
// The other DHs of a handshake involve a fresh ephemeral key and never
// repeat; they skip the cache. When they were cached too (at ~500 DH/s on
// the address resolver) they cycled the whole cache every few seconds and
// evicted the ss entries before a peer came back. Forward secrecy comes from
// the ephemeral DHs, which are never cached.
const dhCacheMax = 4096 // ~ (65 + 33) * 4096 ≈ 400 KB worst case

// dhCacheKey packs pk (33 bytes, compressed secp256k1) || sk
// (32 bytes) into a fixed-size array so the map key has no
// allocation overhead and direct == comparison works.
type dhCacheKey [65]byte

var (
	dhCacheMu sync.RWMutex
	dhCache   = make(map[dhCacheKey][33]byte, dhCacheMax)

	dhCacheHits      atomic.Uint64
	dhCacheMisses    atomic.Uint64
	dhCacheEvictions atomic.Uint64
)

func makeDHKey(sk, pk []byte) dhCacheKey {
	var k dhCacheKey
	copy(k[:33], pk)
	copy(k[33:], sk)
	return k
}

// DH helps to implement `noise.DHFunc`.
func (d Secp256k1) DH(sk, pk []byte) ([]byte, error) {
	static := len(d.staticSK) > 0 && bytes.Equal(sk, d.staticSK) && bytes.Equal(pk, d.peerStatic)
	if !static {
		return ecdh(sk, pk)
	}
	k := makeDHKey(sk, pk)
	dhCacheMu.RLock()
	cached, hit := dhCache[k]
	dhCacheMu.RUnlock()
	if hit {
		dhCacheHits.Add(1)
		out := make([]byte, 33)
		copy(out, cached[:])
		return out, nil
	}
	dhCacheMisses.Add(1)
	out, err := ecdh(sk, pk)
	if err != nil {
		return nil, err
	}

	var entry [33]byte
	copy(entry[:], out)
	dhCacheMu.Lock()
	if len(dhCache) >= dhCacheMax {
		// Bounded random eviction — Go map iteration order is
		// randomized, so dropping the first element we see is a
		// cheap O(1) approximation of LRU.
		for k0 := range dhCache {
			delete(dhCache, k0)
			break
		}
		dhCacheEvictions.Add(1)
	}
	dhCache[k] = entry
	dhCacheMu.Unlock()
	return out, nil
}

// ecdh is cipher.ECDH without its extra public key check:
// secp256k1.ECDH validates both keys itself (and must: an off-curve peer
// point would otherwise be multiplied), so the one in cipher.ECDH repeated
// a point decompression, ~12% of each DH. The output is the same,
// SHA256 of the shared point, padded to DHLen.
func ecdh(sk, pk []byte) ([]byte, error) {
	if len(sk) != 32 || len(pk) != 33 {
		return nil, fmt.Errorf("noise DH: bad key length (sk %d, pk %d)", len(sk), len(pk))
	}
	shared := secp256k1.ECDH(pk, sk)
	if shared == nil {
		// flynn/noise's DHFunc returns an error — surface it as a
		// handshake failure instead of panicking on a malformed peer key.
		return nil, errors.New("noise DH: ECDH failed: invalid key")
	}
	h := cipher.SumSHA256(shared)
	out := make([]byte, 33)
	copy(out, h[:])
	return out, nil
}

// CacheStats is a snapshot of the noise DH cache counters. Hits/Misses
// are counted on every call to (Secp256k1).DH; Evictions is incremented
// each time a single entry is dropped to make room (one per eviction,
// not per overflowed insertion).
type CacheStats struct {
	Hits      uint64
	Misses    uint64
	Evictions uint64
	Size      int
	Capacity  int
}

// GetCacheStats returns a snapshot of the noise DH cache counters.
// Safe to call concurrently with DH.
func GetCacheStats() CacheStats {
	dhCacheMu.RLock()
	size := len(dhCache)
	dhCacheMu.RUnlock()
	return CacheStats{
		Hits:      dhCacheHits.Load(),
		Misses:    dhCacheMisses.Load(),
		Evictions: dhCacheEvictions.Load(),
		Size:      size,
		Capacity:  dhCacheMax,
	}
}

// DHLen helps to implement `noise.DHFunc`.
func (Secp256k1) DHLen() int {
	return 33
}

// DHName helps to implement `noise.DHFunc`.
func (Secp256k1) DHName() string {
	return "Secp256k1"
}
