package dred

// EncoderBuffer stages 16 kHz mono samples into the libopus DRED encoder
// d-frame window used by dred_compute_latents(). LatentGenerator composes this
// buffer with LPCNet analysis and RDOVAE inference.
type EncoderBuffer struct {
	inputBuffer     [2 * DFrameSize]float32
	inputBufferFill int32
	dredOffset      int32
	latentOffset    int32
}

// Reset restores the libopus DRED encoder buffer state after reset.
func (b *EncoderBuffer) Reset() {
	if b == nil {
		return
	}
	*b = EncoderBuffer{}
	b.inputBufferFill = SilkEncoderDelay
}

// InputBufferFill reports how many 16 kHz samples are currently staged.
func (b *EncoderBuffer) InputBufferFill() int32 {
	if b == nil {
		return 0
	}
	return b.inputBufferFill
}

// DREDOffset reports the current libopus-shaped DRED offset in 2.5 ms units.
func (b *EncoderBuffer) DREDOffset() int32 {
	if b == nil {
		return 0
	}
	return b.dredOffset
}

// LatentOffset reports the current libopus-shaped latent offset.
func (b *EncoderBuffer) LatentOffset() int32 {
	if b == nil {
		return 0
	}
	return b.latentOffset
}

// Append16k stages 16 kHz mono float32 PCM into the DRED d-frame buffer. For
// each emitted 320-sample frame, the callback receives a slice that aliases
// internal storage and is valid only until the callback returns. The return
// value is the number of d-frames emitted, whether or not callback is nil.
func (b *EncoderBuffer) Append16k(pcm []float32, extraDelay int32, emit func(frame []float32)) int {
	if b == nil {
		return 0
	}

	currOffset16k := 40 + extraDelay - b.inputBufferFill
	b.dredOffset = floorDiv32(currOffset16k+20, 40)
	b.latentOffset = 0

	emitted := 0
	for remaining := len(pcm); remaining > 0; {
		processSize16k := min(remaining, DFrameSize)
		fill := int(b.inputBufferFill)
		copy(b.inputBuffer[fill:], pcm[:processSize16k])
		b.inputBufferFill += int32(processSize16k)
		pcm = pcm[processSize16k:]
		remaining -= processSize16k

		if b.inputBufferFill >= DFrameSize {
			currOffset16k += DFrameSize
			if emit != nil {
				emit(b.inputBuffer[:DFrameSize])
			}
			emitted++
			b.inputBufferFill -= DFrameSize
			fill = int(b.inputBufferFill)
			copy(b.inputBuffer[:fill], b.inputBuffer[DFrameSize:DFrameSize+fill])
			if b.dredOffset < 6 {
				b.dredOffset += 8
			} else {
				b.latentOffset++
			}
		}
	}

	return emitted
}
