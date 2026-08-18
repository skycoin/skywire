package routing

import "testing"

func TestBwProbePacketRoundTrip(t *testing.T) {
	p := MakeTransportBwProbePacket(42, 3, 8)
	if p.Type() != TransportBwProbePacket {
		t.Fatalf("type = %v", p.Type())
	}
	if len(p) != TransportBwProbeSize {
		t.Errorf("probe packet size = %d, want %d", len(p), TransportBwProbeSize)
	}
	id, seq, total, ok := p.BwProbeFields()
	if !ok || id != 42 || seq != 3 || total != 8 {
		t.Errorf("probe fields = (%d,%d,%d,%v), want (42,3,8,true)", id, seq, total, ok)
	}

	a := MakeTransportBwAckPacket(42, 9_999_999)
	if a.Type() != TransportBwAckPacket {
		t.Fatalf("ack type = %v", a.Type())
	}
	aid, bps, aok := a.BwAckFields()
	if !aok || aid != 42 || bps != 9_999_999 {
		t.Errorf("ack fields = (%d,%d,%v), want (42,9999999,true)", aid, bps, aok)
	}
}
