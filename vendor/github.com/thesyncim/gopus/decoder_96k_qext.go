//go:build gopus_qext

package gopus

// decoderHD96kFields holds the 96 kHz API-rate state for the Decoder.
//
// C ref: opus_decoder.c opus_decoder_init() ENABLE_QEXT gate (Fs == 96000),
// which runs the native 96 kHz CELT mode (mode96000_1920_240) plus the >20 kHz
// extension-band decode chain. gopus routes Fs=96000 to the native HD96k CELT
// decode driver (celt EnableHD96kMode + DecodeFrame at frameSize=1920); the
// QEXT extension payload reserved in the Opus packet padding is parsed by the
// existing top-level extension machinery and forwarded to the CELT layer.
type decoderHD96kFields struct {
	apiIs96kHz bool
}

func (d *Decoder) is96kHz() bool { return d.apiIs96kHz }

// decode96kFloat32 decodes an Opus packet (or PLC frame) natively at 96 kHz.
// The top-level sample rate is 96000 so 20 ms CELT frames decode to 1920
// samples/channel; the CELT decoder runs the native HD96k mode.
func (d *Decoder) decode96kFloat32(data []byte, pcm []float32) (int, error) {
	return d.decodeFloat32(data, pcm, true)
}

// decodeInt1696k decodes at 96 kHz into int16, routing through decode96kFloat32.
func (d *Decoder) decodeInt1696k(data []byte, pcm []int16) (int, error) {
	channels := int(d.channels)
	needed := len(pcm)
	scratch := make([]float32, needed)
	n, err := d.decode96kFloat32(data, scratch)
	if err != nil {
		return 0, err
	}
	softClipAndFloat32ToInt16(pcm[:n*channels], scratch[:n*channels], n, channels, d.softClipMem[:])
	return n, nil
}

// decodeInt2496k decodes at 96 kHz into int32 (24-bit), routing through decode96kFloat32.
func (d *Decoder) decodeInt2496k(data []byte, pcm []int32) (int, error) {
	channels := int(d.channels)
	needed := len(pcm)
	scratch := make([]float32, needed)
	n, err := d.decode96kFloat32(data, scratch)
	if err != nil {
		return 0, err
	}
	float32ToInt24Slice(pcm[:n*channels], scratch[:n*channels], n, channels)
	return n, nil
}

// init96kDecoder initialises a new Decoder for native 96 kHz API output.
// Called from NewDecoder under gopus_qext when cfg.SampleRate == 96000.
func init96kDecoder(d *Decoder) {
	d.apiIs96kHz = true
	d.sampleRate = 96000
	// 20 ms at 96 kHz default frame size for PLC sizing.
	d.lastFrameSize = 96000 / 50
	// Switch the CELT decoder into the native HD96k mode (overlap 240,
	// 3840-sample MDCT, HD preemphasis, Fs=96000).
	d.celtDecoder.EnableHD96kMode()
}
