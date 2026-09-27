package gopus

import "github.com/thesyncim/gopus/internal/opusmath"

// PCMSoftClip applies soft clipping to interleaved float32 PCM samples in-place.
//
// pcm holds N*channels interleaved samples. channels is the number of audio channels.
// softclipMem is per-channel state of length channels; pass a zero-initialized slice
// for the first call and reuse it across successive calls on the same stream.
//
// This mirrors opus_pcm_soft_clip() / opus_pcm_soft_clip_impl() from src/opus.c and
// celt/celt.c in libopus 1.6.1.
func PCMSoftClip(pcm []float32, channels int, softclipMem []float32) {
	if channels < 1 || len(pcm) == 0 || len(softclipMem) < channels {
		return
	}
	n := len(pcm) / channels
	if n < 1 {
		return
	}
	opusmath.PCMSoftClip(pcm, n, channels, softclipMem)
}

// opusPCMSoftClip applies the libopus soft clipping algorithm in-place.
// It expects interleaved samples in the range of roughly [-1, 1].
// This mirrors opus_pcm_soft_clip_impl() in libopus for float builds.
func opusPCMSoftClip(x []float32, n, channels int, declipMem []float32) {
	opusmath.PCMSoftClip(x, n, channels, declipMem)
}

func softClipAndFloat32ToInt16(dst []int16, src []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := min(min(n*channels, len(src)), len(dst))
	if total <= 0 {
		return
	}

	if len(declipMem) >= channels {
		for c := range channels {
			if declipMem[c] != 0 {
				goto fallback
			}
		}
		if convertFloat32ToInt16Unit(dst, src, total) {
			return
		}
		_ = src[total-1]
		_ = dst[total-1]
		for i := 0; i < total; i++ {
			v := src[i]
			if v > 1 || v < -1 {
				goto fallback
			}
			dst[i] = float32ToInt16(v)
		}
		return
	}

fallback:
	opusPCMSoftClip(src[:total], n, channels, declipMem)
	convertFloat32ToInt16NoSoftClipUnit(dst, src, total)
}

func softClipAndFloat32ToInt16Scalar(dst []int16, src []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := min(min(n*channels, len(src)), len(dst))
	if total <= 0 {
		return
	}
	opusPCMSoftClip(src[:total], n, channels, declipMem)
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}

func float32ToInt16NoSoftClip(dst []int16, src []float32, n, channels int) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := min(min(n*channels, len(src)), len(dst))
	if total <= 0 {
		return
	}
	convertFloat32ToInt16NoSoftClipUnit(dst, src, total)
}

func float32ToInt16NoSoftClipScalar(dst []int16, src []float32, n, channels int) {
	if channels < 1 || n < 1 || len(src) == 0 || len(dst) == 0 {
		return
	}
	total := min(min(n*channels, len(src)), len(dst))
	for i := 0; i < total; i++ {
		dst[i] = float32ToInt16(src[i])
	}
}
