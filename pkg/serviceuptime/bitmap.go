// Package serviceuptime — pkg/serviceuptime/bitmap.go: 288-bit-per-day
// uptime bitmap helpers, bit-identical to pkg/visor/stats/bitmap.go
// and pkg/transport-discovery/store/redis_uptime.go's timeline format.
//
// Each UTC day is encoded as 288 bits (one per 5-minute slot) in 36
// raw bytes, MSB-first. Bit set = the recording process was alive
// during at least one keepalive within the slot. Duplicated from the
// visor's stats package to keep this package leaf-importable from
// services without pulling in pkg/visor/...; consolidate if a third
// caller needs the same helpers.
package serviceuptime

import (
	"time"
)

// SlotsPerDay is the number of 5-minute slots in a UTC day.
const SlotsPerDay = 24 * 60 / 5 // 288

// BitmapSize is the byte length of a one-day bitmap.
const BitmapSize = SlotsPerDay / 8 // 36

// SlotForTime returns the 0-based 5-minute slot index for t (in UTC).
func SlotForTime(t time.Time) int {
	utc := t.UTC()
	return (utc.Hour()*60 + utc.Minute()) / 5
}

// SetSlot sets the bit for the given slot index in bm. bm must be at
// least BitmapSize bytes; out-of-range slots are ignored so the
// helper is safe to call with computed indices at day boundaries.
func SetSlot(bm []byte, slot int) {
	if slot < 0 || slot >= SlotsPerDay {
		return
	}
	bm[slot/8] |= 1 << uint(7-slot%8) // MSB-first to match Redis SETBIT
}

// GetSlot reports whether the bit for slot is set in bm.
func GetSlot(bm []byte, slot int) bool {
	if slot < 0 || slot >= SlotsPerDay || len(bm) < BitmapSize {
		return false
	}
	return bm[slot/8]&(1<<uint(7-slot%8)) != 0
}

// CountSlots returns how many bits are set across the bitmap.
func CountSlots(bm []byte) int {
	if len(bm) < BitmapSize {
		return 0
	}
	n := 0
	for i := 0; i < BitmapSize; i++ {
		b := bm[i]
		for b != 0 {
			n += int(b & 1)
			b >>= 1
		}
	}
	return n
}
