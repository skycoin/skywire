// Package cxoutils pkg/cxo/cxoutils/frame.go c2-net-cxo
//
// Versioned+gzipped framing for batched CXO leaves. Feeds that used to
// publish ONE tiny JSON leaf per item (per transport, per (server,
// client) pair, per service) collapse to a FEW batched leaves whose
// body is a JSON array of the shard's members. Two things wrap that
// array on the wire:
//
//   - a single leading VERSION byte, so old/new readers branch on the
//     format explicitly (an old reader rejects a bumped version cleanly
//     rather than misparsing it — the same gate telemetrywire's binary
//     codec uses); and
//   - gzip, because CXO stores + propagates object bytes verbatim (no
//     built-in compression), so a batched JSON array travels
//     uncompressed unless the publisher compresses it.
//
// The frame is deliberately format-agnostic: it carries opaque payload
// bytes, so a feed can put JSON (the common case for awkward,
// variable-length, signed records like disc.Entry / servicedisc.Service)
// or any other encoding behind the same version gate. Fixed-layout
// records that ARE amenable to compact binary should use a dedicated
// binary codec instead (see pkg/telemetrywire).
package cxoutils

// FrameGzip wraps payload as [version byte][gzip(payload)]. Callers pick
// a per-feed version const; readers compare it in UnframeGzip and branch.
func FrameGzip(version byte, payload []byte) []byte {
	gz := Gzip(payload)
	out := make([]byte, 0, 1+len(gz))
	out = append(out, version)
	out = append(out, gz...)
	return out
}

// UnframeGzip splits a FrameGzip blob into its version byte and the
// decompressed payload. ok=false only for an empty blob; the caller is
// responsible for checking the returned version against what it
// understands (an unknown version means "skip this leaf, fall back").
// Gunzip auto-detects the gzip magic, so a non-gzipped payload (or a
// future uncompressed variant) round-trips unchanged.
func UnframeGzip(body []byte) (version byte, payload []byte, ok bool) {
	if len(body) < 1 {
		return 0, nil, false
	}
	return body[0], Gunzip(body[1:]), true
}
