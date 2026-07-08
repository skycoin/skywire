package dred

// EncoderBuffer mirrors the libopus DRED encoder-side 16 kHz d-frame staging
// state from dred_compute_latents(), without pulling in resampling or RDOVAE
// inference yet.
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

// Append16k stages 16 kHz mono PCM into the libopus-shaped DRED d-frame buffer.
// The callback, when non-nil, is invoked once per emitted 320-sample d-frame
// with a slice that aliases internal storage and is only valid until the
// callback returns.
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
