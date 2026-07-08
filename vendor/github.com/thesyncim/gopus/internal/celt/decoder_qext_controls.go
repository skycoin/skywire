//go:build gopus_qext

package celt

// SetQEXTPayload configures a one-shot packet-extension payload for the next
// CELT decode call. It is used by the outer Opus decoder to forward optional
// packet extensions without allocating.
func (d *Decoder) SetQEXTPayload(payload []byte) {
	d.ensureQEXTState().pendingPayload = payload
}
