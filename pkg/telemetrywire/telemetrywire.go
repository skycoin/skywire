// Package telemetrywire pkg/telemetrywire/telemetrywire.go c3-vis-core
//
// telemetrywire is the SHARED, compact, sharded binary codec for the
// visor→TPD CXO telemetry feed. It is imported by BOTH the publishing
// side (pkg/visor/stats) and the consuming side
// (pkg/deployment/tpd/cxoaggregator) so the byte layout cannot drift
// between them — the two halves share one encoder/decoder rather than
// re-declaring a wire struct on each side.
//
// # Why sharded binary
//
// The telemetry feed formerly published ONE JSON leaf per live
// transport at transports/<uuid>/current. A busy hub (~851 transports)
// produced ~851 leaves → ~1700 CXO objects in one Root, which could not
// finish filling over TPD's short-lived announce conn (only the top
// slice landed → busy-hub telemetry undercount). Object count was the
// fill bottleneck.
//
// This codec replaces those N per-transport leaves with 16 FIXED
// sharded binary leaves: a transport is assigned to shard uuid[0]>>4
// (0..15), and every transport in a shard is packed into one binary
// blob published at transports/telemetry/<sh> (<sh> = lowercase 2-hex).
// A busy hub's telemetry subtree is then 16 leaves regardless of
// transport count, so the whole-Root fill completes.
//
// # Wire layout (little-endian)
//
//	off size field
//	0   1    version  = 0x02
//	1   1    shard    = 0x00..0x0f
//	2   2    count    uint16
//	4   ...  entries[count], 53 bytes each:
//	       +0   16  transport uuid (raw 16 bytes)
//	       +16  8   sent_bytes      uint64
//	       +24  8   recv_bytes      uint64
//	       +32  4   throughput_bps  float32 (peak goodput EWMA)
//	       +36  4   latency_min_ms  float32
//	       +40  4   latency_max_ms  float32
//	       +44  4   latency_avg_ms  float32
//	       +48  4   sampled_at      uint32 (unix SECONDS)
//	       +52  1   type            uint8 enum
//
// The version byte gates the format: a future breaking change bumps it
// (0x03, …) and both sides branch on the value. DecodeShard rejects any
// other version, so an old reader fails a new blob cleanly rather than
// misparsing it. This is why the deploy is TPD-FIRST: TPD (which reads
// shards AND the legacy current leaves as a fallback) must ship before
// visors switch to shard-only publishing, or an old TPD silently loses
// a new visor's telemetry during the rollout window.
package telemetrywire

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"

	"github.com/google/uuid"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

// Version is the current wire-format version byte. Bump on any breaking
// change to the header or entry layout; readers reject other values.
const Version = 0x02

// ShardCount is the fixed number of shards a transport set is spread
// across — the low nibble of the uuid's first byte (uuid[0]>>4).
const ShardCount = 16

const (
	headerLen = 4  // version(1) + shard(1) + count(2)
	entryLen  = 53 // see the package doc for the field layout
)

// Transport-type enum codes. 0..7 match the original spec; 8 (swsr / WS)
// is the deterministic extension for the WS transport that the visor
// fleet also runs — tptypes.Known() carries it, so both sides map it
// rather than collapsing it to unknown. Unknown/empty types encode as 0,
// which TPD treats as "skip uptime" exactly like the legacy empty-type
// path (RecordTransportHeartbeat is not called for a zero-code entry).
const (
	TypeUnknown uint8 = 0
	TypeSTCPR   uint8 = 1
	TypeSUDPH   uint8 = 2
	TypeSTCP    uint8 = 3
	TypeDMSG    uint8 = 4
	TypeSQUICR  uint8 = 5 // tptypes.QUIC, wire name "squicr"
	TypeWEBRTC  uint8 = 6
	TypeSWTR    uint8 = 7 // tptypes.WT, wire name "swtr"
	TypeSWSR    uint8 = 8 // tptypes.WS, wire name "swsr"
)

// Entry is one transport's telemetry row — the decoded form of a 53-byte
// wire record. Both sides operate on Entry; only this package knows the
// byte layout.
type Entry struct {
	ID            uuid.UUID
	SentBytes     uint64
	RecvBytes     uint64
	ThroughputBps float32
	LatMin        float32
	LatMax        float32
	LatAvg        float32
	SampledAtUnix uint32
	Type          uint8
}

// Errors returned by DecodeShard. All indicate a malformed or
// wrong-version blob; the caller drops it rather than misparsing.
var (
	ErrShort          = errors.New("telemetrywire: blob shorter than header")
	ErrVersion        = errors.New("telemetrywire: unsupported version byte")
	ErrShardRange     = errors.New("telemetrywire: shard byte out of range")
	ErrLengthMismatch = errors.New("telemetrywire: blob length does not match count")
)

// ShardOf returns the shard a transport ID belongs to: the high nibble
// of the uuid's first byte (uuid[0]>>4), 0..15.
func ShardOf(id uuid.UUID) uint8 {
	return id[0] >> 4
}

// LeafPath is the stable CXO leaf path for a shard's telemetry blob:
// "transports/telemetry/<sh>" with <sh> the lowercase 2-hex shard.
func LeafPath(shard uint8) string {
	return fmt.Sprintf("transports/telemetry/%02x", shard)
}

// TypeToCode maps a transport-type wire string to its enum code,
// normalizing legacy aliases (e.g. "quic"/"squic" → squicr, "wt" →
// swtr, "ws" → swsr) first. An empty or unrecognized type yields
// TypeUnknown (0) — TPD treats that as "skip uptime".
func TypeToCode(s string) uint8 {
	switch tptypes.NormalizeType(tptypes.Type(s)) {
	case tptypes.STCPR:
		return TypeSTCPR
	case tptypes.SUDPH:
		return TypeSUDPH
	case tptypes.STCP:
		return TypeSTCP
	case tptypes.DMSG:
		return TypeDMSG
	case tptypes.QUIC:
		return TypeSQUICR
	case tptypes.WEBRTC:
		return TypeWEBRTC
	case tptypes.WT:
		return TypeSWTR
	case tptypes.WS:
		return TypeSWSR
	default:
		return TypeUnknown
	}
}

// CodeToType maps an enum code back to its canonical transport-type wire
// string. TypeUnknown (0) and any unrecognized code yield "" — the same
// value the legacy empty-type path used to signal "skip uptime".
func CodeToType(c uint8) string {
	switch c {
	case TypeSTCPR:
		return string(tptypes.STCPR)
	case TypeSUDPH:
		return string(tptypes.SUDPH)
	case TypeSTCP:
		return string(tptypes.STCP)
	case TypeDMSG:
		return string(tptypes.DMSG)
	case TypeSQUICR:
		return string(tptypes.QUIC)
	case TypeWEBRTC:
		return string(tptypes.WEBRTC)
	case TypeSWTR:
		return string(tptypes.WT)
	case TypeSWSR:
		return string(tptypes.WS)
	default:
		return ""
	}
}

// EncodeShard packs entries into one shard blob. The caller is
// responsible for passing only entries that belong to shard (ShardOf ==
// shard); the shard byte is written to the header verbatim. Entry order
// is preserved, so a caller that wants a byte-stable blob for change
// detection should sort entries by ID first.
func EncodeShard(shard uint8, entries []Entry) []byte {
	// count is a uint16 field; a shard holds ~1/16 of a hub's transports, so
	// this ceiling is astronomically above any real fleet, but clamp anyway
	// so the header can never disagree with the body.
	if len(entries) > 0xFFFF {
		entries = entries[:0xFFFF]
	}
	buf := make([]byte, headerLen+entryLen*len(entries))
	buf[0] = Version
	buf[1] = shard
	binary.LittleEndian.PutUint16(buf[2:4], uint16(len(entries))) //nolint:gosec // clamped to 0xFFFF above
	off := headerLen
	for _, e := range entries {
		copy(buf[off:off+16], e.ID[:])
		binary.LittleEndian.PutUint64(buf[off+16:off+24], e.SentBytes)
		binary.LittleEndian.PutUint64(buf[off+24:off+32], e.RecvBytes)
		binary.LittleEndian.PutUint32(buf[off+32:off+36], math.Float32bits(e.ThroughputBps))
		binary.LittleEndian.PutUint32(buf[off+36:off+40], math.Float32bits(e.LatMin))
		binary.LittleEndian.PutUint32(buf[off+40:off+44], math.Float32bits(e.LatMax))
		binary.LittleEndian.PutUint32(buf[off+44:off+48], math.Float32bits(e.LatAvg))
		binary.LittleEndian.PutUint32(buf[off+48:off+52], e.SampledAtUnix)
		buf[off+52] = e.Type
		off += entryLen
	}
	return buf
}

// DecodeShard parses a shard blob produced by EncodeShard. It validates
// the version byte, the shard range, and that the total length matches
// the declared count exactly — a truncated or over-long blob is rejected
// rather than partially parsed.
func DecodeShard(b []byte) (shard uint8, entries []Entry, err error) {
	if len(b) < headerLen {
		return 0, nil, ErrShort
	}
	if b[0] != Version {
		return 0, nil, fmt.Errorf("%w: got 0x%02x want 0x%02x", ErrVersion, b[0], Version)
	}
	shard = b[1]
	if shard >= ShardCount {
		return 0, nil, fmt.Errorf("%w: %d", ErrShardRange, shard)
	}
	count := int(binary.LittleEndian.Uint16(b[2:4]))
	if len(b) != headerLen+entryLen*count {
		return 0, nil, fmt.Errorf("%w: len=%d count=%d", ErrLengthMismatch, len(b), count)
	}
	if count == 0 {
		return shard, nil, nil
	}
	entries = make([]Entry, count)
	off := headerLen
	for i := 0; i < count; i++ {
		var e Entry
		copy(e.ID[:], b[off:off+16])
		e.SentBytes = binary.LittleEndian.Uint64(b[off+16 : off+24])
		e.RecvBytes = binary.LittleEndian.Uint64(b[off+24 : off+32])
		e.ThroughputBps = math.Float32frombits(binary.LittleEndian.Uint32(b[off+32 : off+36]))
		e.LatMin = math.Float32frombits(binary.LittleEndian.Uint32(b[off+36 : off+40]))
		e.LatMax = math.Float32frombits(binary.LittleEndian.Uint32(b[off+40 : off+44]))
		e.LatAvg = math.Float32frombits(binary.LittleEndian.Uint32(b[off+44 : off+48]))
		e.SampledAtUnix = binary.LittleEndian.Uint32(b[off+48 : off+52])
		e.Type = b[off+52] //nolint:gosec // length validated above: len(b) == headerLen+entryLen*count
		entries[i] = e
		off += entryLen
	}
	return shard, entries, nil
}
