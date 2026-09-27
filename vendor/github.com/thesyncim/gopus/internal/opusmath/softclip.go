package opusmath

// PCMSoftClip applies libopus opus_pcm_soft_clip() (src/opus.c) in place to n
// interleaved frames of the given channel count. It expects samples in roughly
// [-1, 1] and smoothly attenuates excursions past +/-1 (hard-limiting only
// beyond +/-2) instead of clipping, carrying the per-channel adaptation
// coefficient across calls in declipMem (one entry per channel, the C float
// declip_mem[]).
//
// The whole routine runs in float32 to match the reference: the quadratic
// soft-knee (v + a*v*v), the peak search, and the coefficient update
// a = (maxval-1)/(maxval*maxval) plus the 2.4e-7 nudge are all single precision,
// as is the boundary-continuity ramp. If every sample is already within [-1, 1]
// and all declipMem entries are zero, it returns without modifying x, exactly as
// libopus short-circuits.
func PCMSoftClip(x []float32, n, channels int, declipMem []float32) {
	if channels < 1 || n < 1 || len(x) == 0 || len(declipMem) < channels {
		return
	}

	total := min(n*channels, len(x))

	allWithinNeg1Pos1 := true
	for i := range total {
		v := x[i]
		if v > 2 {
			x[i] = 2
			allWithinNeg1Pos1 = false
		} else if v < -2 {
			x[i] = -2
			allWithinNeg1Pos1 = false
		} else if v > 1 || v < -1 {
			allWithinNeg1Pos1 = false
		}
	}
	if allWithinNeg1Pos1 {
		for c := range channels {
			if declipMem[c] != 0 {
				goto applySoftClip
			}
		}
		return
	}

applySoftClip:
	for c := range channels {
		a := declipMem[c]

		for i := range n {
			idx := i*channels + c
			if idx >= len(x) {
				break
			}
			v := x[idx]
			if v*a >= 0 {
				break
			}
			x[idx] = v + a*v*v
		}

		curr := 0
		if c >= len(x) {
			declipMem[c] = a
			continue
		}
		x0 := x[c]

		for {
			var i int
			if allWithinNeg1Pos1 {
				i = n
			} else {
				for i = curr; i < n; i++ {
					idx := i*channels + c
					if idx >= len(x) {
						i = n
						break
					}
					v := x[idx]
					if v > 1 || v < -1 {
						break
					}
				}
			}

			if i == n {
				a = 0
				break
			}

			peakPos := i
			start := i
			end := i
			idx := i*channels + c
			if idx >= len(x) {
				a = 0
				break
			}
			vref := x[idx]
			maxval := float32Abs(vref)

			for start > 0 {
				idxPrev := (start-1)*channels + c
				if idxPrev >= len(x) {
					break
				}
				if vref*x[idxPrev] < 0 {
					break
				}
				start--
			}
			for end < n {
				idxEnd := end*channels + c
				if idxEnd >= len(x) {
					break
				}
				if vref*x[idxEnd] < 0 {
					break
				}
				val := float32Abs(x[idxEnd])
				if val > maxval {
					maxval = val
					peakPos = end
				}
				end++
			}

			special := start == 0 && vref*x[c] >= 0

			if maxval > 0 {
				a = (maxval - 1) / (maxval * maxval)
				a += a * 2.4e-7
				if vref > 0 {
					a = -a
				}
			} else {
				a = 0
			}

			for i = start; i < end; i++ {
				idx2 := i*channels + c
				if idx2 >= len(x) {
					break
				}
				v := x[idx2]
				x[idx2] = v + a*v*v
			}

			if special && peakPos >= 2 {
				offset := x0 - x[c]
				delta := offset / float32(peakPos)
				for i = curr; i < peakPos; i++ {
					offset -= delta
					idx2 := i*channels + c
					if idx2 >= len(x) {
						break
					}
					v := x[idx2] + offset
					if v > 1 {
						v = 1
					} else if v < -1 {
						v = -1
					}
					x[idx2] = v
				}
			}

			curr = end
			if curr == n {
				break
			}
		}

		declipMem[c] = a
	}
}

// float32Abs is the single-precision absolute value used by the soft-clip peak
// search, matching libopus' fabsf(). It is kept local so the comparisons stay in
// float32 rather than promoting through the generic util.Abs.
func float32Abs(v float32) float32 {
	if v < 0 {
		return -v
	}
	return v
}
