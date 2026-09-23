// Package router delivery_crc.go: the in-mux DELIVERY check (CapDeliveryCRC).
//
// Framing. When both edges negotiate CapDeliveryCRC, a sequenced DATA frame's
// payload on the wire is
//
//	app_payload ‖ crc32c(seq_be32 ‖ app_payload)   (4 bytes, big-endian)
//
// The stamp is applied in wrapPayload BEFORE the per-frame AEAD seal, so the
// trailer rides inside the ciphertext and a SACK retransmit or an FEC
// reconstruction of the same sequence reproduces byte-identical frames. The
// receiver verifies and strips the trailer in deliverData at DELIVERY time —
// after reordering, on the in-order path — not on receipt.
//
// Why here and not at the wire: per-frame AEAD already authenticates ONE frame
// as it arrives. What nothing checked before is that the bytes handed to the
// application after reassembly are the bytes the sender wrote, in that order —
// a reorder/flush defect (#4005) stayed invisible until an application hash
// failed. Binding the CRC to the sequence number means a frame delivered at the
// wrong position fails the check, not just a frame whose bytes were mangled.
package router

import (
	"encoding/binary"
	"hash/crc32"

	"github.com/skycoin/skywire/pkg/routing"
)

// deliveryCRCTable is the Castagnoli (CRC32C) table — hardware-accelerated on
// amd64/arm64, so the per-frame cost is negligible next to the AEAD seal.
var deliveryCRCTable = crc32.MakeTable(crc32.Castagnoli)

// deliveryCRC computes the CRC32C over the sequence number (big-endian) followed
// by the payload.
func deliveryCRC(seq uint32, payload []byte) uint32 {
	var seqBuf [routing.SeqSize]byte
	binary.BigEndian.PutUint32(seqBuf[:], seq)
	return crc32.Update(crc32.Update(0, deliveryCRCTable, seqBuf[:]), deliveryCRCTable, payload)
}

// stampDeliveryCRC returns payload with its CRC32C trailer appended. It always
// allocates rather than appending in place: the caller's buffer is the app's
// write buffer, which it may reuse the moment Write returns.
func stampDeliveryCRC(seq uint32, payload []byte) []byte {
	out := make([]byte, len(payload)+routing.DeliveryCRCSize)
	copy(out, payload)
	binary.BigEndian.PutUint32(out[len(payload):], deliveryCRC(seq, payload))
	return out
}

// checkDeliveryCRC strips the trailer from a stamped frame and reports whether
// it matches. A frame too short to hold a trailer is a mismatch: under the
// negotiated capability every data frame carries one.
func checkDeliveryCRC(seq uint32, frame []byte) ([]byte, bool) {
	if len(frame) < routing.DeliveryCRCSize {
		return nil, false
	}
	payload := frame[:len(frame)-routing.DeliveryCRCSize]
	want := binary.BigEndian.Uint32(frame[len(frame)-routing.DeliveryCRCSize:])
	return payload, deliveryCRC(seq, payload) == want
}
