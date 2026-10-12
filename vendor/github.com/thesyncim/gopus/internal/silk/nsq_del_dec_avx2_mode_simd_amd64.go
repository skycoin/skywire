//go:build amd64 && goexperiment.simd && !nosimd && !purego

package silk

import "simd/archsimd"

// silkNSQDelDecUsesAVX2 mirrors the x86 libopus dispatch of silk_NSQ_del_dec
// to silk_NSQ_del_dec_avx2, which RTCD selects on AVX2+FMA hosts.
var silkNSQDelDecUsesAVX2 = archsimd.X86.AVX2() && archsimd.X86.FMA()
