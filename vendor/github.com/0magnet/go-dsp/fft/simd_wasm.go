//go:build goexperiment.simd && !tinygo

package fft

import (
	"simd/archsimd"
	"sync"
	"unsafe"
)

// simdEnabled reports whether the radix-2 butterflies use SIMD128. It is a
// variable so the tests can compare against the scalar path. There is no
// feature check: a module using SIMD128 either validates or does not load.
// Only js/wasm has this path; on amd64 (AVX) it measured no faster.
var simdEnabled = true

var (
	rotLock    sync.RWMutex
	rotFactors = map[int][]complex128{}
)

// getRotFactors returns factors multiplied by i, so that a complex product
// b*w is real(b)*w + imag(b)*(i*w): two lane-wise multiplies and an add,
// with the same roundings as the scalar complex multiply.
func getRotFactors(n int, factors []complex128) []complex128 {
	rotLock.RLock()
	rot := rotFactors[n]
	rotLock.RUnlock()
	if rot != nil {
		return rot
	}
	rot = make([]complex128, len(factors))
	for k, w := range factors {
		rot[k] = complex(-imag(w), real(w))
	}
	rotLock.Lock()
	rotFactors[n] = rot
	rotLock.Unlock()
	return rot
}

// vec views a complex128 as its (real, imag) pair, which is how Go lays
// a complex128 out in memory.
func vec(c *complex128) *[2]float64 {
	return (*[2]float64)(unsafe.Pointer(c)) //nolint:gosec // complex128 is [2]float64 in memory
}

// splat returns (re, re) and (im, im) for *c. archsimd has no lane shuffle
// for wasm, but f64x2.splat is one instruction.
func splat(c *complex128) (re, im archsimd.Float64x2) {
	return archsimd.BroadcastFloat64x2(real(*c)), archsimd.BroadcastFloat64x2(imag(*c))
}

// butterflySIMD computes the radix-2 butterflies of every block in
// [start, end) for one stage, holding each complex128 in a Float64x2.
func butterflySIMD(t, r, factors, rot []complex128, start, end, stage, blocks int) {
	s2 := stage / 2
	for nb := start; nb < end; nb += stage {
		ra, rb := r[nb:nb+s2], r[nb+s2:nb+stage]
		ta, tb := t[nb:nb+s2], t[nb+s2:nb+stage]
		for j := range ra {
			k := blocks * j
			a := archsimd.LoadFloat64x2Array(vec(&ra[j]))
			br, bi := splat(&rb[j])
			w := br.Mul(archsimd.LoadFloat64x2Array(vec(&factors[k]))).
				Add(bi.Mul(archsimd.LoadFloat64x2Array(vec(&rot[k]))))
			a.Add(w).StoreArray(vec(&ta[j]))
			a.Sub(w).StoreArray(vec(&tb[j]))
		}
	}
}
