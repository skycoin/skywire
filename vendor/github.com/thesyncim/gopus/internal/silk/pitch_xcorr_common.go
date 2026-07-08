package silk

func innerProductF32Acc(a, b []float32, length int) float32 {
	if length <= 0 {
		return 0
	}
	_ = a[length-1] // BCE hint
	_ = b[length-1] // BCE hint
	var result float32
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
		sum[0] += noFMA32(tmp, y0)
		sum[1] += noFMA32(tmp, y1)
		sum[2] += noFMA32(tmp, y2)
		sum[3] += noFMA32(tmp, y3)

		tmp = x[xIdx]
		xIdx++
		y0 = y[yIdx]
		yIdx++
		sum[0] += noFMA32(tmp, y1)
		sum[1] += noFMA32(tmp, y2)
		sum[2] += noFMA32(tmp, y3)
		sum[3] += noFMA32(tmp, y0)

		tmp = x[xIdx]
		xIdx++
		y1 = y[yIdx]
		yIdx++
		sum[0] += noFMA32(tmp, y2)
		sum[1] += noFMA32(tmp, y3)
		sum[2] += noFMA32(tmp, y0)
		sum[3] += noFMA32(tmp, y1)

		tmp = x[xIdx]
		xIdx++
		y2 = y[yIdx]
		yIdx++
		sum[0] += noFMA32(tmp, y3)
		sum[1] += noFMA32(tmp, y0)
		sum[2] += noFMA32(tmp, y1)
		sum[3] += noFMA32(tmp, y2)
		j += 4
	}

	if j < length {
		tmp := x[xIdx]
		xIdx++
		y3 = y[yIdx]
		yIdx++
		sum[0] += noFMA32(tmp, y0)
		sum[1] += noFMA32(tmp, y1)
		sum[2] += noFMA32(tmp, y2)
		sum[3] += noFMA32(tmp, y3)
		j++
	}
	if j < length {
		tmp := x[xIdx]
		xIdx++
		y0 = y[yIdx]
		yIdx++
		sum[0] += noFMA32(tmp, y1)
		sum[1] += noFMA32(tmp, y2)
		sum[2] += noFMA32(tmp, y3)
		sum[3] += noFMA32(tmp, y0)
		j++
	}
	if j < length {
		tmp := x[xIdx]
		y1 = y[yIdx]
		sum[0] += noFMA32(tmp, y2)
		sum[1] += noFMA32(tmp, y3)
		sum[2] += noFMA32(tmp, y0)
		sum[3] += noFMA32(tmp, y1)
	}
}
