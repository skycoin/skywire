package silk

func innerProductF32Acc(a, b []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	_ = a[length-1] // BCE hint
	_ = b[length-1] // BCE hint
	var result float32
	// celt_pitch_xcorr_c's non-unrolled path calls celt_inner_prod_c
	// (celt/pitch.c:291-296; celt/pitch.h:159-166). GCC v3 emits a rounded
	// product followed by an add here; the four-lag xcorr kernel uses FMAs.
	for i := range length {
		result += noFMA32(a[i], b[i])
	}
	return result
}

func xcorrKernelFloat(x, y []float32, sum *[4]float32, length int) {
	if length < 3 {
		return
	}
	// BCE hints: x needs length elements, y needs length+3 elements.
	_ = x[length-1]
	_ = y[length+2]
	xIdx := 0
	yIdx := 0
	y0 := y[yIdx]
	yIdx++
	y1 := y[yIdx]
	yIdx++
	y2 := y[yIdx]
	yIdx++
	y3 := float32(0)

	j := 0
	for j+3 < length {
		tmp := x[xIdx]
		xIdx++
		y3 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y0)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y1)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y2)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y3)

		tmp = x[xIdx]
		xIdx++
		y0 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y1)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y2)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y3)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y0)

		tmp = x[xIdx]
		xIdx++
		y1 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y2)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y3)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y0)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y1)

		tmp = x[xIdx]
		xIdx++
		y2 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y3)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y0)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y1)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y2)
		j += 4
	}

	if j < length {
		tmp := x[xIdx]
		xIdx++
		y3 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y0)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y1)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y2)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y3)
		j++
	}
	if j < length {
		tmp := x[xIdx]
		xIdx++
		y0 = y[yIdx]
		yIdx++
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y1)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y2)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y3)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y0)
		j++
	}
	if j < length {
		tmp := x[xIdx]
		y1 = y[yIdx]
		sum[0] = pitchXcorrMAC32(sum[0], tmp, y2)
		sum[1] = pitchXcorrMAC32(sum[1], tmp, y3)
		sum[2] = pitchXcorrMAC32(sum[2], tmp, y0)
		sum[3] = pitchXcorrMAC32(sum[3], tmp, y1)
	}
}
