// Package stats provides the visor's local telemetry store.
//
// pkg/visor/stats/bitmap.go: 288-bit-per-day uptime bitmap helpers.
//
// Each UTC day is encoded as 288 bits (one per 5-minute slot) in 36
// raw bytes. Bit set = the tier or service was online during at least
// one sampler tick within the slot. The encoding is bit-identical to
// the format pkg/transport-discovery/store/redis_uptime.go uses for
// integrated uptime tracking, so renderers and dashboards can consume
// either source unchanged.
package stats

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
// least BitmapSize bytes; the slot index must be in [0, SlotsPerDay).
// Out-of-range slots are ignored to make the helper safe to call with
// computed indices at day boundaries.
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

// Render converts a 36-byte bitmap to its 288-character ASCII form,
// '.' for set bits and ' ' for unset, matching TPD's v3 timeline shape.
// A zero-length or nil input renders as 288 spaces.
func Render(bm []byte) string {
	out := make([]byte, SlotsPerDay)
	for i := range out {
		if GetSlot(bm, i) {
			out[i] = '.'
		} else {
			out[i] = ' '
		}
	}
	return string(out)
}

// And returns the bitwise AND of two equal-length bitmaps. Returns nil
// if the inputs are not both BitmapSize bytes. Used to derive composite
// views like "process AND dmsg AND skynet" for callers that want a
// strict online definition.
func And(a, b []byte) []byte {
	if len(a) != BitmapSize || len(b) != BitmapSize {
		return nil
	}
	out := make([]byte, BitmapSize)
	for i := range out {
		out[i] = a[i] & b[i]
	}
	return out
}
