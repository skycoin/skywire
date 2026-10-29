// Package api pkg/deployment/tpd/api/entries_json.go c4-net-discovery
package api

import (
	"encoding/hex"
	"encoding/json"
	"math"
	"strconv"
	"unicode/utf8"

	"github.com/skycoin/skywire/pkg/transport"
)

// marshalEntries returns the same bytes as json.Marshal(entries) without
// the per-field MarshalText copies, which made /transports/edge the
// heaviest TPD allocator. Anything it does not handle goes to json.Marshal.
func marshalEntries(entries []*transport.Entry) ([]byte, error) {
	if entries == nil {
		return []byte("null"), nil
	}
	n := 2
	for _, e := range entries {
		if e != nil {
			// The fixed text is 216 bytes and the three numbers add about 80.
			n += 300 + len(e.Type) + len(e.Label)
		}
	}
	b := make([]byte, 0, n)
	b = append(b, '[')
	for i, e := range entries {
		if i > 0 {
			b = append(b, ',')
		}
		var ok bool
		if b, ok = appendEntryJSON(b, e); !ok {
			return json.Marshal(entries)
		}
	}
	return append(b, ']'), nil
}

func appendEntryJSON(b []byte, e *transport.Entry) ([]byte, bool) {
	if e == nil {
		return append(b, "null"...), true
	}
	b = append(b, `{"t_id":"`...)
	b = appendUUID(b, e.ID)
	b = append(b, `","edges":["`...)
	b = hex.AppendEncode(b, e.Edges[0][:])
	b = append(b, `","`...)
	b = hex.AppendEncode(b, e.Edges[1][:])
	b = append(b, `"],"type":`...)
	var ok bool
	if b, ok = appendPlainString(b, string(e.Type)); !ok {
		return b, false
	}
	b = append(b, `,"label":`...)
	if b, ok = appendPlainString(b, string(e.Label)); !ok {
		return b, false
	}
	if e.Latency != 0 {
		b = append(b, `,"latency_ms":`...)
		if b, ok = appendFloat(b, e.Latency); !ok {
			return b, false
		}
	}
	if e.Bandwidth != 0 {
		b = append(b, `,"bandwidth":`...)
		b = strconv.AppendUint(b, e.Bandwidth, 10)
	}
	if e.ThroughputBps != 0 {
		b = append(b, `,"throughput_bps":`...)
		if b, ok = appendFloat(b, e.ThroughputBps); !ok {
			return b, false
		}
	}
	return append(b, '}'), true
}

func appendUUID(b []byte, u [16]byte) []byte {
	b = hex.AppendEncode(b, u[0:4])
	b = append(b, '-')
	b = hex.AppendEncode(b, u[4:6])
	b = append(b, '-')
	b = hex.AppendEncode(b, u[6:8])
	b = append(b, '-')
	b = hex.AppendEncode(b, u[8:10])
	b = append(b, '-')
	return hex.AppendEncode(b, u[10:16])
}

// appendPlainString quotes s when it needs no escaping under encoding/json's
// rules (which also escape <, > and &), and reports false otherwise.
func appendPlainString(b []byte, s string) ([]byte, bool) {
	for i := 0; i < len(s); i++ {
		c := s[i]
		if c < 0x20 || c >= utf8.RuneSelf || c == '"' || c == '\\' || c == '<' || c == '>' || c == '&' {
			return b, false
		}
	}
	b = append(b, '"')
	b = append(b, s...)
	return append(b, '"'), true
}

// appendFloat formats f as encoding/json does, and reports false for NaN
// and infinities, which json.Marshal rejects.
func appendFloat(b []byte, f float64) ([]byte, bool) {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return b, false
	}
	format := byte('f')
	if abs := math.Abs(f); abs != 0 && (abs < 1e-6 || abs >= 1e21) {
		format = 'e'
	}
	b = strconv.AppendFloat(b, f, format, -1, 64)
	if format == 'e' {
		// Shorten e-09 to e-9, as encoding/json does.
		if n := len(b); n >= 4 && b[n-4] == 'e' && b[n-3] == '-' && b[n-2] == '0' {
			b[n-2] = b[n-1]
			b = b[:n-1]
		}
	}
	return b, true
}
