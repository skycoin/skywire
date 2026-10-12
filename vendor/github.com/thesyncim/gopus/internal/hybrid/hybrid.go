package hybrid

import (
	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/plc"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

func (d *Decoder) finishSuccessfulDecode(frameSize, channels int) {
	d.plcState.Reset()
	d.plcState.SetLastFrameParams(plc.ModeHybrid, frameSize, channels)
}

func (d *Decoder) requireStereoDecoder() error {
	if d.channels != 2 {
		return ErrDecodeFailed
	}
	return nil
}

func decodedInt16FromFloat32(samples []float32, err error) ([]int16, error) {
	if err != nil {
		return nil, err
	}
	return float32ToInt16(samples), nil
}

func (d *Decoder) decodeWithRangeDecoder(
	rd *rangecoding.Decoder,
	frameSize int,
	packetStereo bool,
	afterSilk func(*rangecoding.Decoder) (int, error),
) ([]float32, error) {
	return d.decodeFrameWithHookFloat32(rd, frameSize, packetStereo, afterSilk, nil)
}

func (d *Decoder) decodeAndFinishPacket(
	data []byte,
	frameSize int,
	packetStereo bool,
	lastFrameChannels int,
) ([]float32, error) {
	if len(data) == 0 {
		return d.decodePLCToFloat32(frameSize, packetStereo)
	}
	if !ValidHybridFrameSize(d.frameSize48FromAPI(frameSize)) {
		return nil, ErrInvalidFrameSize
	}

	var rd rangecoding.Decoder
	rd.Init(data)

	samples, err := d.decodeWithRangeDecoder(&rd, frameSize, packetStereo, nil)
	if err != nil {
		return nil, err
	}

	d.finishSuccessfulDecode(frameSize, lastFrameChannels)
	return samples, nil
}

func (d *Decoder) decodeAndFinishWithRangeDecoder(
	rd *rangecoding.Decoder,
	frameSize int,
	packetStereo bool,
	lastFrameChannels int,
	afterSilk func(*rangecoding.Decoder) (int, error),
) ([]float32, error) {
	samples, err := d.decodeWithRangeDecoder(rd, frameSize, packetStereo, afterSilk)
	if err != nil {
		return nil, err
	}

	d.finishSuccessfulDecode(frameSize, lastFrameChannels)
	return samples, nil
}

// Decode decodes a Hybrid frame with the packet stereo flag clear. An empty
// data slice requests PLC. frameSize is the per-channel sample count at the
// configured API rate; at the default 48 kHz rate, encoded Hybrid frames use
// 480 samples for 10 ms or 960 for 20 ms. The result is float32 PCM at that
// rate.
func (d *Decoder) Decode(data []byte, frameSize int) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, false, 1)
}

// DecodeWithPacketStereo decodes a Hybrid frame using packetStereo to select
// the packet's channel layout. frameSize is measured per channel at the
// configured API rate. An empty data slice requests PLC.
func (d *Decoder) DecodeWithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, packetStereo, int(d.channels))
}

// SetRawMonoFrameHook forwards the SILK lowband raw mono/mid-channel hook used
// by decoder-side neural PLC/DRED paths.
func (d *Decoder) SetRawMonoFrameHook(hook silk.RawMonoFrameHook) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetRawMonoFrameHook(hook)
}

// SetRawMonoLossFrameHook forwards the SILK loss-history hook used by
// decoder-side neural PLC/DRED paths.
func (d *Decoder) SetRawMonoLossFrameHook(hook silk.RawMonoFrameHook) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetRawMonoLossFrameHook(hook)
}

// SetDeepPLCLossMonoHook forwards the SILK lowband loss hook used by
// decoder-side neural PLC/DRED paths.
func (d *Decoder) SetDeepPLCLossMonoHook(hook silk.DeepPLCLossMonoHook) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.SetDeepPLCLossMonoHook(hook)
}

// ArmFixedPLCLowbandCapture forwards the integer lowband capture used by the
// fixed-point multistream Hybrid PLC path. buf receives the resampled SILK
// int16 samples produced by the next PLC decode.
func (d *Decoder) ArmFixedPLCLowbandCapture(buf []int16) {
	if d == nil || d.silkDecoder == nil {
		return
	}
	d.silkDecoder.ArmPLCLowbandCapture(buf)
}

// FixedPLCLowbandCaptured reports the interleaved int16 sample count from the
// most recent SILK PLC decode captured by ArmFixedPLCLowbandCapture.
func (d *Decoder) FixedPLCLowbandCaptured() int {
	if d == nil || d.silkDecoder == nil {
		return 0
	}
	return d.silkDecoder.PLCLowbandCaptured()
}

// DecodeStereo decodes a stereo Hybrid frame and returns interleaved float32
// PCM at the configured API rate. An empty data slice requests PLC. The
// decoder must be configured for two channels. frameSize is the per-channel
// sample count; at 48 kHz, encoded Hybrid frames use 480 or 960 samples.
func (d *Decoder) DecodeStereo(data []byte, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return d.decodeAndFinishPacket(data, frameSize, true, 2)
}

// DecodeToInt16 decodes a Hybrid frame and converts the float32 output to
// interleaved int16 PCM at the configured API rate. frameSize is the per-channel
// sample count; an empty data slice requests PLC.
func (d *Decoder) DecodeToInt16(data []byte, frameSize int) ([]int16, error) {
	return decodedInt16FromFloat32(d.decodeAndFinishPacket(data, frameSize, false, 1))
}

// DecodeStereoToInt16 decodes a stereo Hybrid frame and converts it to
// interleaved int16 PCM at the configured API rate. The decoder must be
// configured for two channels.
func (d *Decoder) DecodeStereoToInt16(data []byte, frameSize int) ([]int16, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return decodedInt16FromFloat32(d.decodeAndFinishPacket(data, frameSize, true, 2))
}

// DecodeToFloat32 decodes a Hybrid frame and returns float32 PCM at the
// configured API rate. frameSize is the per-channel sample count; an empty
// data slice requests PLC.
func (d *Decoder) DecodeToFloat32(data []byte, frameSize int) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, false, 1)
}

// DecodeToFloat32WithPacketStereo decodes using packetStereo and returns
// float32 PCM at the configured API rate. frameSize is the per-channel sample
// count; an empty data slice requests PLC.
func (d *Decoder) DecodeToFloat32WithPacketStereo(data []byte, frameSize int, packetStereo bool) ([]float32, error) {
	return d.decodeAndFinishPacket(data, frameSize, packetStereo, int(d.channels))
}

// DecodeStereoToFloat32 decodes a stereo Hybrid frame and returns interleaved
// float32 PCM at the configured API rate. The decoder must be configured for
// two channels.
func (d *Decoder) DecodeStereoToFloat32(data []byte, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}

	return d.decodeAndFinishPacket(data, frameSize, true, 2)
}

// DecodeWithDecoder decodes a Hybrid frame from a pre-initialized range decoder
// and returns float32 PCM at the configured API rate. frameSize is the
// per-channel sample count. The caller owns rd and controls its packet lifetime.
func (d *Decoder) DecodeWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	return d.decodeWithRangeDecoder(rd, frameSize, false, nil)
}

// DecodeWithDecoderHook decodes from a pre-initialized range decoder.
// afterSilk runs after SILK decodes its symbols and returns the logical packet
// length after any Opus-layer redundancy parsing. frameSize is the per-channel
// sample count at the configured API rate.
func (d *Decoder) DecodeWithDecoderHook(rd *rangecoding.Decoder, frameSize int, packetStereo bool, afterSilk func(*rangecoding.Decoder) (int, error)) ([]float32, error) {
	return d.decodeAndFinishWithRangeDecoder(rd, frameSize, packetStereo, int(d.channels), afterSilk)
}

// DecodeStereoWithDecoder decodes stereo from a pre-initialized range decoder
// and returns interleaved float32 PCM at the configured API rate. The decoder
// must be configured for two channels.
func (d *Decoder) DecodeStereoWithDecoder(rd *rangecoding.Decoder, frameSize int) ([]float32, error) {
	if err := d.requireStereoDecoder(); err != nil {
		return nil, err
	}
	return d.decodeWithRangeDecoder(rd, frameSize, true, nil)
}

func float32ToInt16(samples []float32) []int16 {
	output := make([]int16, len(samples))
	for i, s := range samples {
		output[i] = opusmath.Float32ToInt16(s)
	}
	return output
}

func (d *Decoder) decodePLCToFloat32(frameSize int, stereo bool) ([]float32, error) {
	if frameSize < 0 {
		return nil, ErrInvalidFrameSize
	}
	out := make([]float32, frameSize*int(d.channels))
	if err := d.DecodePLCToFloat32WithPacketStereoInto(frameSize, stereo, out); err != nil {
		return nil, err
	}
	return out, nil
}

// DecodePLCToFloat32WithPacketStereoInto conceals a Hybrid frame into
// caller-owned PCM while advancing the same SILK and CELT PLC state.
func (d *Decoder) DecodePLCToFloat32WithPacketStereoInto(frameSize int, stereo bool, output []float32) error {
	frameSizeAPI := frameSize
	frameSize48 := d.frameSize48FromAPI(frameSizeAPI)
	if !ValidHybridFrameSize(frameSize48) && frameSize48 != 120 && frameSize48 != 240 {
		return ErrInvalidFrameSize
	}
	channels := int(d.channels)
	totalSamples := frameSizeAPI * channels
	if len(output) < totalSamples {
		return ErrDecodeFailed
	}
	output = output[:totalSamples]

	// Advance the PLC loss-fade cadence. libopus has no fade-exhausted
	// shortcut: silk_PLC and celt_decode_lost run unconditionally on every lost
	// hybrid frame. The SILK lowband fades through silk_PLC and the CELT highband
	// energy floors at the background estimate, so the concealed frame decays
	// without ever being short-circuited to silence. Critically, celt_decode_lost
	// always advances the CELT range-coder state (st->rng, the noise LCG) on each
	// lost frame, and that state is what an in-band FEC (decode_fec=1) recovery
	// reports as the final range. Returning early here would freeze st->rng and
	// desync the range coder on the next FEC step, so the CELT PLC must run on
	// every lost frame regardless of how decayed the energy is.
	_ = d.plcState.RecordLoss()

	// SILK PLC cannot produce less than 10ms; use 10ms and trim if needed.
	plcSilkFrameSize := frameSizeAPI
	apiSampleRate := int(d.apiSampleRate)
	minSilkFrameSize := apiSampleRate / 100
	if minSilkFrameSize <= 0 {
		minSilkFrameSize = 480
	}
	if plcSilkFrameSize < minSilkFrameSize {
		plcSilkFrameSize = minSilkFrameSize
	}

	// Generate SILK PLC through the SILK decoder's native nil-packet path.
	// This keeps concealment cadence/state aligned with SILK-mode PLC.
	silkUpsampled := d.ensureSilkUpsampled(plcSilkFrameSize * channels)
	clear(silkUpsampled)
	d.silkDecoder.NotifyBandwidthChange(silk.BandwidthWideband)
	if stereo {
		if channels == 1 {
			n, err := d.silkDecoder.DecodePLCStereoToMonoInto(silk.BandwidthWideband, plcSilkFrameSize, silkUpsampled)
			if err != nil {
				return err
			}
			silkUpsampled = silkUpsampled[:n]
		} else {
			n, err := d.silkDecoder.DecodePLCStereoInto(silk.BandwidthWideband, plcSilkFrameSize, silkUpsampled)
			if err != nil {
				return err
			}
			silkUpsampled = silkUpsampled[:n]
		}
	} else {
		if channels == 2 {
			n, err := d.silkDecoder.DecodeMonoToStereoPLCInto(silk.BandwidthWideband, plcSilkFrameSize, false, silkUpsampled)
			if err != nil {
				return err
			}
			silkUpsampled = silkUpsampled[:n]
		} else {
			n, err := d.silkDecoder.DecodePLCInto(silk.BandwidthWideband, plcSilkFrameSize, silkUpsampled)
			if err != nil {
				return err
			}
			silkUpsampled = silkUpsampled[:n]
		}
	}
	if len(silkUpsampled) > totalSamples {
		silkUpsampled = silkUpsampled[:totalSamples]
	}

	// The SILK decoder/resampler path already provides API-rate alignment.
	clear(output[copy(output, silkUpsampled):])

	// Conceal the CELT highband (bands 17-21) and accumulate it onto the SILK
	// lowband, as opus_decode_frame's celt_decode_with_ec(NULL, celt_accum=1)
	// does for a lost Hybrid frame.
	celtFrameSize := frameSize48
	if apiSampleRate == 96000 {
		celtFrameSize = frameSizeAPI
	}
	if err := d.celtDecoder.DecodeHybridFECPLC(celtFrameSize, output); err != nil {
		return err
	}

	return nil
}
