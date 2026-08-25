package telemetrywire

import (
	"testing"

	"github.com/google/uuid"

	tptypes "github.com/skycoin/skywire/pkg/transport/types"
)

func sampleEntry(seed byte) Entry {
	var id uuid.UUID
	for i := range id {
		id[i] = seed + byte(i)
	}
	return Entry{
		ID:            id,
		SentBytes:     0x1122334455667788,
		RecvBytes:     0x8877665544332211,
		ThroughputBps: 123456.75,
		LatMin:        1.5,
		LatMax:        250.25,
		LatAvg:        42.125,
		SampledAtUnix: 0x5F5E1000,
		Type:          TypeSTCPR,
	}
}

func TestShardOf(t *testing.T) {
	var id uuid.UUID
	id[0] = 0xA7 // high nibble 0xA = 10
	if got := ShardOf(id); got != 0x0A {
		t.Fatalf("ShardOf = %d, want 10", got)
	}
	id[0] = 0x0F
	if got := ShardOf(id); got != 0 {
		t.Fatalf("ShardOf = %d, want 0", got)
	}
	for sh := uint8(0); sh < ShardCount; sh++ {
		id[0] = sh << 4
		if got := ShardOf(id); got != sh {
			t.Fatalf("ShardOf(byte0=%#x) = %d, want %d", id[0], got, sh)
		}
	}
}

func TestLeafPath(t *testing.T) {
	cases := map[uint8]string{
		0:  "transports/telemetry/00",
		1:  "transports/telemetry/01",
		10: "transports/telemetry/0a",
		15: "transports/telemetry/0f",
	}
	for sh, want := range cases {
		if got := LeafPath(sh); got != want {
			t.Errorf("LeafPath(%d) = %q, want %q", sh, got, want)
		}
	}
}

func TestRoundTrip(t *testing.T) {
	for _, count := range []int{0, 1, 2, 53, 800} {
		entries := make([]Entry, count)
		for i := 0; i < count; i++ {
			entries[i] = sampleEntry(byte(i))
		}
		blob := EncodeShard(7, entries)
		shard, got, err := DecodeShard(blob)
		if err != nil {
			t.Fatalf("count=%d DecodeShard: %v", count, err)
		}
		if shard != 7 {
			t.Fatalf("count=%d shard = %d, want 7", count, shard)
		}
		if len(got) != count {
			t.Fatalf("count=%d decoded %d entries", count, len(got))
		}
		for i := range got {
			if got[i] != entries[i] {
				t.Fatalf("count=%d entry %d mismatch:\n got=%+v\nwant=%+v", count, i, got[i], entries[i])
			}
		}
	}
}

func TestThroughputRoundTrips(t *testing.T) {
	e := sampleEntry(3)
	e.ThroughputBps = 987654.5
	_, got, err := DecodeShard(EncodeShard(0, []Entry{e}))
	if err != nil {
		t.Fatal(err)
	}
	if got[0].ThroughputBps != 987654.5 {
		t.Fatalf("throughput = %v, want 987654.5", got[0].ThroughputBps)
	}
}

func TestDecodeRejectsCorruptInput(t *testing.T) {
	valid := EncodeShard(3, []Entry{sampleEntry(1), sampleEntry(2)})

	// Too short for even a header.
	if _, _, err := DecodeShard([]byte{0x02, 0x00}); err == nil {
		t.Error("expected ErrShort for 2-byte blob")
	}
	// Wrong version byte.
	bad := append([]byte(nil), valid...)
	bad[0] = 0x03
	if _, _, err := DecodeShard(bad); err == nil {
		t.Error("expected ErrVersion for wrong version")
	}
	// Shard out of range.
	bad = append([]byte(nil), valid...)
	bad[1] = 0x10
	if _, _, err := DecodeShard(bad); err == nil {
		t.Error("expected ErrShardRange for shard 16")
	}
	// Truncated body (drop the last entry's worth of bytes minus one).
	if _, _, err := DecodeShard(valid[:len(valid)-1]); err == nil {
		t.Error("expected ErrLengthMismatch for truncated blob")
	}
	// Over-long body (extra trailing byte).
	if _, _, err := DecodeShard(append(append([]byte(nil), valid...), 0x00)); err == nil {
		t.Error("expected ErrLengthMismatch for over-long blob")
	}
	// Count claims more entries than bytes present.
	shortCount := append([]byte(nil), valid...)
	shortCount[2] = 0xFF
	shortCount[3] = 0xFF
	if _, _, err := DecodeShard(shortCount); err == nil {
		t.Error("expected ErrLengthMismatch when count exceeds body")
	}
}

func TestTypeEnumMapping(t *testing.T) {
	cases := []struct {
		s    string
		code uint8
	}{
		{"", TypeUnknown},
		{"nonsense", TypeUnknown},
		{string(tptypes.STCPR), TypeSTCPR},
		{string(tptypes.SUDPH), TypeSUDPH},
		{string(tptypes.STCP), TypeSTCP},
		{string(tptypes.DMSG), TypeDMSG},
		{string(tptypes.QUIC), TypeSQUICR},
		{string(tptypes.WEBRTC), TypeWEBRTC},
		{string(tptypes.WT), TypeSWTR},
		{string(tptypes.WS), TypeSWSR},
	}
	for _, c := range cases {
		if got := TypeToCode(c.s); got != c.code {
			t.Errorf("TypeToCode(%q) = %d, want %d", c.s, got, c.code)
		}
	}

	// Legacy aliases normalize to the canonical code.
	for _, alias := range []string{"quic", "squic"} {
		if got := TypeToCode(alias); got != TypeSQUICR {
			t.Errorf("TypeToCode(%q) = %d, want squicr code %d", alias, got, TypeSQUICR)
		}
	}
	if got := TypeToCode("wt"); got != TypeSWTR {
		t.Errorf("TypeToCode(wt) = %d, want %d", got, TypeSWTR)
	}
	if got := TypeToCode("ws"); got != TypeSWSR {
		t.Errorf("TypeToCode(ws) = %d, want %d", got, TypeSWSR)
	}

	// CodeToType is the inverse over the known codes; 0 and unknown → "".
	roundtrip := []uint8{TypeSTCPR, TypeSUDPH, TypeSTCP, TypeDMSG, TypeSQUICR, TypeWEBRTC, TypeSWTR, TypeSWSR}
	for _, code := range roundtrip {
		if TypeToCode(CodeToType(code)) != code {
			t.Errorf("code %d did not round-trip through CodeToType→TypeToCode", code)
		}
	}
	if CodeToType(TypeUnknown) != "" {
		t.Error("CodeToType(0) should be empty string")
	}
	if CodeToType(200) != "" {
		t.Error("CodeToType(unknown) should be empty string")
	}
}

// TestEncodedSizeIsShardStable proves the blob length is header + 53*count,
// the fixed record size the wire spec pins.
func TestEncodedSizeIsShardStable(t *testing.T) {
	for _, count := range []int{0, 1, 5, 100} {
		entries := make([]Entry, count)
		blob := EncodeShard(0, entries)
		if want := headerLen + entryLen*count; len(blob) != want {
			t.Errorf("count=%d blob len = %d, want %d", count, len(blob), want)
		}
	}
}
