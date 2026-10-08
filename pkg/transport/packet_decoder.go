package transport

import "github.com/skycoin/skywire/pkg/routing"

// packetDecoder splits a byte stream into routing packets as the bytes
// arrive, for reads driven by data instead of a goroutine in io.ReadFull.
type packetDecoder struct {
	hdr  [routing.PacketHeaderSize]byte
	nhdr int
	pkt  routing.Packet // the packet being filled, nil between packets
	n    int
}

// feed passes each packet p completes to emit. A packet is one allocation
// that emit may keep.
func (d *packetDecoder) feed(p []byte, emit func(routing.Packet)) {
	for {
		if d.pkt == nil {
			c := copy(d.hdr[d.nhdr:], p)
			d.nhdr += c
			p = p[c:]
			if d.nhdr < len(d.hdr) {
				return
			}
			d.pkt = make(routing.Packet, routing.PacketHeaderSize+int(routing.Packet(d.hdr[:]).Size()))
			copy(d.pkt, d.hdr[:])
			d.n, d.nhdr = routing.PacketHeaderSize, 0
		}
		c := copy(d.pkt[d.n:], p)
		d.n += c
		p = p[c:]
		if d.n < len(d.pkt) {
			return
		}
		pkt := d.pkt
		d.pkt = nil
		emit(pkt)
	}
}
