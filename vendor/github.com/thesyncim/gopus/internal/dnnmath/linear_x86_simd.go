//go:build amd64 && goexperiment.simd && !nosimd && !purego

package dnnmath

import (
	"math"
	"simd/archsimd"
	"unsafe"

	"github.com/thesyncim/gopus/internal/dnnblob"
)

// The kernels below mirror the dnn/vec_avx.h helpers that libopus compiles
// into dnn/x86/nnet_avx2.c (compute_linear_avx2, compute_conv2d_avx2) with
// -mavx -mfma -mavx2. Callers select them only when X86VectorKernels is set.
//
// Weights load straight from the little-endian blob bytes, and every
// instruction between a kernel's first 256-bit operation and its
// ClearAVXUpperBits is VEX-encoded: stack scratch, zeroed arrays and scalar
// float code there would compile to legacy SSE and pay a state transition.

// loadBlobFloat32x8 loads the eight float32 values starting at element i of a
// little-endian float32 blob payload.
func loadBlobFloat32x8(raw []byte, i int) archsimd.Float32x8 {
	return archsimd.LoadUint8x32(raw[4*i:]).AsUint32x8().AsFloat32x8()
}

// loadBlobFloat32x4 loads the four float32 values starting at element i.
func loadBlobFloat32x4(raw []byte, i int) archsimd.Float32x4 {
	return archsimd.LoadUint8x16(raw[4*i:]).AsUint32x4().AsFloat32x4()
}

// SGEMVX86 mirrors dnn/vec_avx.h:sgemv: complete 16-, 8- and 4-row blocks
// accumulate through ascending-column FMA chains. Scalar row tails retain
// separate multiply/add rounding, matching the selected native C oracle.
func SGEMVX86(out []float32, weights dnnblob.Float32View, rows, cols, colStride int, x []float32) {
	raw := weights.Bytes()
	row := 0
	for ; row+16 <= rows; row += 16 {
		var acc0, acc1 archsimd.Float32x8
		for col := range cols {
			base := col*colStride + row
			v := archsimd.BroadcastFloat32x8(x[col])
			acc0 = loadBlobFloat32x8(raw, base).MulAdd(v, acc0)
			acc1 = loadBlobFloat32x8(raw, base+8).MulAdd(v, acc1)
		}
		acc0.Store(out[row:])
		acc1.Store(out[row+8:])
	}
	for ; row+8 <= rows; row += 8 {
		var acc archsimd.Float32x8
		for col := range cols {
			acc = loadBlobFloat32x8(raw, col*colStride+row).MulAdd(archsimd.BroadcastFloat32x8(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	// The 4-row blocks and the scalar rows use 128-bit and scalar registers.
	archsimd.ClearAVXUpperBits()
	for ; row+4 <= rows; row += 4 {
		var acc archsimd.Float32x4
		for col := range cols {
			acc = loadBlobFloat32x4(raw, col*colStride+row).MulAdd(archsimd.BroadcastFloat32x4(x[col]), acc)
		}
		acc.Store(out[row:])
	}
	for ; row < rows; row++ {
		var sum float32
		for col := range cols {
			// The explicit conversion preserves the scalar product rounding
			// observed in the selected compute_linear_avx2 remainder.
			sum += float32(weights.At(col*colStride+row) * x[col])
		}
		out[row] = sum
	}
}

// SparseSGEMV8x4X86 mirrors dnn/vec_avx.h:sparse_sgemv8x4: every 8-row block
// chains four FMAs per column block, one per input of the block.
func SparseSGEMV8x4X86(out []float32, weights dnnblob.Float32View, idx dnnblob.Int32View, rows int, x []float32) {
	raw := weights.Bytes()
	wOffset := 0
	idxPos := 0
	for row := 0; row < rows; row += 8 {
		var acc archsimd.Float32x8
		colBlocks := int(idx.At(idxPos))
		idxPos++
		for range colBlocks {
			pos := int(idx.At(idxPos))
			idxPos++
			for tap := range 4 {
				acc = loadBlobFloat32x8(raw, wOffset+8*tap).MulAdd(archsimd.BroadcastFloat32x8(x[pos+tap]), acc)
			}
			wOffset += 32
		}
		acc.Store(out[row:])
	}
	archsimd.ClearAVXUpperBits()
}

// CGEMV8x4X86 mirrors dnn/vec_avx.h:cgemv8x4. The input is quantized to
// unsigned bytes by vector_ps_to_epi8 and each 8x4 block goes through the
// AVX2 opus_mm256_dpbusds_epi32 emulation, whose VPMADDUBSW saturates every
// pair of byte products to int16. q is caller-owned scratch of at least cols
// bytes; weights use libopus's 8x4 block order.
func CGEMV8x4X86(out []float32, weights dnnblob.Int8View, scale dnnblob.Float32View, rows, cols int, x []float32, q []uint8) {
	quantizeInputX86(q, x, cols)
	w, sc := weights.Bytes(), scale.Bytes()
	wOffset := 0
	for row := 0; row < rows; row += 8 {
		var acc archsimd.Int32x8
		for col := 0; col < cols; col += 4 {
			acc = dpbusdsX86(acc, q, col, w, wOffset)
			wOffset += 32
		}
		storeScaledX86(out, row, acc, sc)
	}
	archsimd.ClearAVXUpperBits()
}

// SparseCGEMV8x4X86 mirrors dnn/vec_avx.h:sparse_cgemv8x4 with the same
// quantization and saturating block products as CGEMV8x4X86.
func SparseCGEMV8x4X86(out []float32, weights dnnblob.Int8View, idx dnnblob.Int32View, scale dnnblob.Float32View, rows, cols int, x []float32, q []uint8) {
	quantizeInputX86(q, x, cols)
	w, sc := weights.Bytes(), scale.Bytes()
	wOffset := 0
	idxPos := 0
	for row := 0; row < rows; row += 8 {
		var acc archsimd.Int32x8
		colBlocks := int(idx.At(idxPos))
		idxPos++
		for range colBlocks {
			col := int(idx.At(idxPos))
			idxPos++
			acc = dpbusdsX86(acc, q, col, w, wOffset)
			wOffset += 32
		}
		storeScaledX86(out, row, acc, sc)
	}
	archsimd.ClearAVXUpperBits()
}

// quantizeInputX86 mirrors dnn/vec_avx.h:vector_ps_to_epi8: a fused
// 127*x+127, CVTPS2DQ nearest-even conversion, then PACKUSDW and PACKUSWB.
func quantizeInputX86(q []uint8, x []float32, n int) {
	c127 := archsimd.BroadcastFloat32x8(127)
	var lanes [8]int32
	i := 0
	for ; i+8 <= n; i += 8 {
		archsimd.LoadFloat32x8(x[i:]).MulAdd(c127, c127).Round().ConvertToInt32().StoreArray(&lanes)
		// Indexing reads the lanes in place; ranging over the array value
		// would copy it through legacy SSE moves.
		for k := range lanes {
			q[i+k] = packUS8(lanes[k])
		}
	}
	for ; i < n; i++ {
		v := archsimd.BroadcastFloat32x8(x[i]).MulAdd(c127, c127).Round().ConvertToInt32()
		q[i] = packUS8(v.GetLo().GetElem(0))
	}
}

// packUS8 applies PACKUSDW's unsigned 16-bit saturation followed by
// PACKUSWB, which reads those words as signed: values from 32768 upward
// saturate to zero.
func packUS8(v int32) uint8 {
	if v < 0 || v >= 32768 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(v)
}

// dpbusdsX86 mirrors the AVX2 opus_mm256_dpbusds_epi32 in dnn/vec_avx.h:
// the four input bytes at q[col:] are broadcast to every 32-bit lane,
// multiplied by the 32 weight bytes at w[wOffset:] with VPMADDUBSW, widened
// by VPMADDWD against ones, and added to acc.
func dpbusdsX86(acc archsimd.Int32x8, q []uint8, col int, w []byte, wOffset int) archsimd.Int32x8 {
	packed := uint32(q[col]) | uint32(q[col+1])<<8 | uint32(q[col+2])<<16 | uint32(q[col+3])<<24
	xj := archsimd.BroadcastUint32x8(packed).AsUint8x32()
	weights := archsimd.LoadUint8x32(w[wOffset:]).AsInt8x32()
	pairs := xj.DotProductPairsSaturated(weights)
	return acc.Add(pairs.DotProductPairs(archsimd.BroadcastInt16x16(1)))
}

// storeScaledX86 converts acc to float and stores it times the eight scale
// values of the little-endian float32 payload sc starting at row.
func storeScaledX86(out []float32, row int, acc archsimd.Int32x8, sc []byte) {
	acc.ConvertToFloat32().Mul(loadBlobFloat32x8(sc, row)).Store(out[row:])
}

// Conv2D3x3X86 mirrors dnn/nnet_arch.h:conv2d_3x3_float as GCC compiles it
// into compute_conv2d_avx2. Each output tap rounds the second product, fuses
// the first and then the remaining seven products in source order, and adds
// the result to the running output. in holds three time steps of
// inChannels rows, each height+2 wide; weights are [outChannels][inChannels][3][3].
func Conv2D3x3X86(out []float32, weights dnnblob.Float32View, inChannels, outChannels int, in []float32, height, hstride int) {
	inStride := height + 2
	raw := weights.Bytes()
	for i := range outChannels {
		clear(out[i*hstride : i*hstride+height])
	}
	var w [9]archsimd.Float32x8
	for i := range outChannels {
		o := out[i*hstride : i*hstride+height]
		for m := range inChannels {
			wBase := (i*inChannels + m) * 9
			// Conv2D broadcasts each four-byte float directly from the retained
			// payload. x86 permits unaligned scalar reads, and this slice bounds
			// the read to one float.
			for k := range w {
				w[k] = archsimd.BroadcastFloat32x8(*(*float32)(unsafe.Pointer((*[4]byte)(raw[4*(wBase+k):]))))
			}
			r0 := in[m*inStride:]
			r1 := in[(inChannels+m)*inStride:]
			r2 := in[(2*inChannels+m)*inStride:]
			j := 0
			for ; j+8 <= height; j += 8 {
				acc := w[1].Mul(archsimd.LoadFloat32x8(r0[j+1:]))
				acc = w[0].MulAdd(archsimd.LoadFloat32x8(r0[j:]), acc)
				acc = w[2].MulAdd(archsimd.LoadFloat32x8(r0[j+2:]), acc)
				acc = w[3].MulAdd(archsimd.LoadFloat32x8(r1[j:]), acc)
				acc = w[4].MulAdd(archsimd.LoadFloat32x8(r1[j+1:]), acc)
				acc = w[5].MulAdd(archsimd.LoadFloat32x8(r1[j+2:]), acc)
				acc = w[6].MulAdd(archsimd.LoadFloat32x8(r2[j:]), acc)
				acc = w[7].MulAdd(archsimd.LoadFloat32x8(r2[j+1:]), acc)
				acc = w[8].MulAdd(archsimd.LoadFloat32x8(r2[j+2:]), acc)
				acc.Add(archsimd.LoadFloat32x8(o[j:])).Store(o[j:])
			}
			for ; j < height; j++ {
				acc := w[1].Mul(archsimd.BroadcastFloat32x8(r0[j+1]))
				acc = w[0].MulAdd(archsimd.BroadcastFloat32x8(r0[j]), acc)
				acc = w[2].MulAdd(archsimd.BroadcastFloat32x8(r0[j+2]), acc)
				acc = w[3].MulAdd(archsimd.BroadcastFloat32x8(r1[j]), acc)
				acc = w[4].MulAdd(archsimd.BroadcastFloat32x8(r1[j+1]), acc)
				acc = w[5].MulAdd(archsimd.BroadcastFloat32x8(r1[j+2]), acc)
				acc = w[6].MulAdd(archsimd.BroadcastFloat32x8(r2[j]), acc)
				acc = w[7].MulAdd(archsimd.BroadcastFloat32x8(r2[j+1]), acc)
				acc = w[8].MulAdd(archsimd.BroadcastFloat32x8(r2[j+2]), acc)
				// Add and store lane 0 through vector and integer registers,
				// since a scalar float add or store would be legacy SSE here.
				sum := acc.GetLo().Add(archsimd.BroadcastFloat32x4(o[j]))
				o[j] = math.Float32frombits(uint32(sum.AsInt32x4().GetElem(0)))
			}
		}
	}
	archsimd.ClearAVXUpperBits()
}
