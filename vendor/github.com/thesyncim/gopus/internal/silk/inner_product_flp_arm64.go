//go:build arm64 && !nosimd && !purego

package silk

func innerProductFLPArm64(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	_ = a[length-1]
	_ = b[length-1]
	a = a[:length:length]
	b = b[:length:length]
	var result silkCReal
	i := 0
	for ; i+3 < length; i += 4 {
		p0 := silkCReal(a[i]) * silkCReal(b[i])
		p1 := silkCReal(a[i+1]) * silkCReal(b[i+1])
		p2 := silkCReal(a[i+2]) * silkCReal(b[i+2])
		p3 := silkCReal(a[i+3]) * silkCReal(b[i+3])
		result += ((p0 + p1) + p2) + p3
	}
	for ; i < length; i++ {
		result += silkCReal(a[i]) * silkCReal(b[i])
	}
	return result
}

func innerProductFLPImpl(a, b []float32, length int) silkCReal {
	if length <= 0 {
		return 0
	}
	_ = a[length-1]
	_ = b[length-1]
	return innerProductFLPArm64(a, b, length)
}
