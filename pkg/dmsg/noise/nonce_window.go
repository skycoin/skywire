// Package noise pkg/noise/nonce_window.go
package noise

import "fmt"

// NonceWindowSize is the number of recent nonces tracked for replay prevention.
// Must be a multiple of 64 for the bitmap implementation.
const NonceWindowSize = 1024

// NonceWindow tracks nonces using a sliding window to prevent replay attacks
// without unbounded memory growth. It stores the highest nonce seen and a
// bitmap of the last NonceWindowSize nonces. Nonces older than the window
// are assumed to be replays.
//
// This replaces the unbounded NonceMap which grew forever on long-lived
// sessions (e.g., setup-node handling thousands of streams).
type NonceWindow struct {
	maxNonce uint64
	bitmap   [NonceWindowSize / 64]uint64
}

// NewNonceWindow creates a new NonceWindow.
func NewNonceWindow() *NonceWindow {
	return &NonceWindow{}
}

// Check returns nil if the nonce is valid (not a replay, not too old).
// If valid, the nonce is recorded in the window.
func (nw *NonceWindow) Check(nonce uint64) error {
	if nonce == 0 {
		return fmt.Errorf("nonce cannot be zero")
	}

	if nw.maxNonce == 0 {
		// First nonce seen.
		nw.maxNonce = nonce
		nw.setBit(nonce)
		return nil
	}

	if nonce > nw.maxNonce {
		// New highest nonce — advance the window.
		diff := nonce - nw.maxNonce
		if diff >= NonceWindowSize {
			// Nonce jumped far ahead — clear entire bitmap.
			nw.bitmap = [NonceWindowSize / 64]uint64{}
		} else {
			// Clear the bits that are now outside the window.
			for i := nw.maxNonce + 1; i <= nonce; i++ {
				nw.clearBit(i)
			}
		}
		nw.maxNonce = nonce
		nw.setBit(nonce)
		return nil
	}

	// Nonce is <= maxNonce. Check if it's within the window.
	if nw.maxNonce-nonce >= NonceWindowSize {
		return fmt.Errorf("nonce (%d) is too old (window starts at %d)", nonce, nw.maxNonce-NonceWindowSize+1)
	}

	// Check for replay.
	if nw.hasBit(nonce) {
		return fmt.Errorf("received decryption nonce (%d) is repeated", nonce)
	}

	nw.setBit(nonce)
	return nil
}

func (nw *NonceWindow) setBit(nonce uint64) {
	idx := nonce % NonceWindowSize
	nw.bitmap[idx/64] |= 1 << (idx % 64)
}

func (nw *NonceWindow) clearBit(nonce uint64) {
	idx := nonce % NonceWindowSize
	nw.bitmap[idx/64] &^= 1 << (idx % 64)
}

func (nw *NonceWindow) hasBit(nonce uint64) bool {
	idx := nonce % NonceWindowSize
	return nw.bitmap[idx/64]&(1<<(idx%64)) != 0
}
