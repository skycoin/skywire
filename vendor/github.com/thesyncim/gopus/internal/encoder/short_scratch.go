package encoder

// ReserveShortEncodeScratch sizes the packet and CELT fallback scratch for a
// caller's short-input frame before mode selection can switch a child encoder
// from SILK to CELT. The first call performs any growth; later calls with the
// same frame and packet bounds reuse the backing storage.
func (e *Encoder) ReserveShortEncodeScratch(frameSize, maxDataBytes int) {
	if frameSize <= 0 || maxDataBytes <= 0 {
		return
	}
	e.ensureFramePayload(maxDataBytes)
	e.ensureCELTEncoder()
	e.celtEncoder.ReserveEncodeScratch(frameSize)
	e.reserveFixedShortScratch(frameSize, maxDataBytes)
}
