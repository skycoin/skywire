package celt

import (
	"runtime"

	"github.com/thesyncim/gopus/internal/opusmath"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

const (
	spreadNone       = 0
	spreadLight      = 1
	spreadNormal     = 2
	spreadAggressive = 3
)

// Exported spread constants for callers outside the celt package.
const (
	SpreadNone       = spreadNone
	SpreadLight      = spreadLight
	SpreadNormal     = spreadNormal
	SpreadAggressive = spreadAggressive
)

var orderyTable = []int{
	1, 0,
	3, 0, 2, 1,
	7, 0, 4, 3, 6, 1, 5, 2,
	15, 0, 8, 7, 12, 3, 11, 4, 14, 1, 9, 6, 13, 2, 10, 5,
}

var bitInterleaveTable = []int{
	0, 1, 1, 1,
	2, 3, 3, 3,
	2, 3, 3, 3,
	2, 3, 3, 3,
}

var bitDeinterleaveTable = []int{
	0x00, 0x03, 0x0C, 0x0F,
	0x30, 0x33, 0x3C, 0x3F,
	0xC0, 0xC3, 0xCC, 0xCF,
	0xF0, 0xF3, 0xFC, 0xFF,
}

type bandCtx struct {
	rd              *rangecoding.Decoder
	re              *rangecoding.Encoder
	encode          bool
	extEnc          *rangecoding.Encoder
	extDec          *rangecoding.Decoder
	extBudget       int
	extTotalBits    int
	extraBands      bool
	bandEdges       []int
	bandLogN        []int
	cacheIndex      []int16
	cacheBits       []uint8
	bandCaps        []int32
	bandE           []celtEner
	nbBands         int
	channels        int
	spread          int
	tfChange        int
	remainingBits   int
	intensity       int
	band            int
	seed            uint32
	seedActive      bool
	resynth         bool
	disableInv      bool
	avoidSplitNoise bool
	// thetaRound controls theta quantization biasing for stereo RDO:
	//   0: normal rounding (default)
	//  -1: bias toward rounding down (toward 0 or 16384)
	//   1: bias toward rounding up (toward 8192/equal split)
	// This is used by theta RDO optimization in quant_all_bands.
	thetaRound int
	// tapset controls the window taper selection for prefilter/postfilter comb filter.
	// Values: 0 (narrow), 1 (medium), 2 (wide).
	// This is set during spreading_decision and used for comb filter taper gains.
	// While not directly used in PVQ quantization, it's tracked here for
	// encoder state consistency and future prefilter integration.
	// Reference: libopus celt/celt.c comb_filter() gains table
	tapset int
	// scratch holds pre-allocated buffers for the decode hot path.
	// This eliminates per-call allocations in algUnquant, Hadamard transforms, etc.
	scratch *bandDecodeScratch
	// encScratch holds pre-allocated buffers for the encode hot path.
	// This eliminates per-call allocations in algQuant, PVQ search, etc.
	encScratch *bandEncodeScratch
}

type splitCtx struct {
	inv       int
	imid      int
	iside     int
	delta     int
	itheta    int
	ithetaQ30 int // Extended precision theta in Q30 format (when QEXT enabled)
	qalloc    int
}

func orderyForStride(stride int) []int {
	switch stride {
	case 2:
		return orderyTable[0:2]
	case 4:
		return orderyTable[2:6]
	case 8:
		return orderyTable[6:14]
	case 16:
		return orderyTable[14:30]
	default:
		return nil
	}
}

func deinterleaveHadamardStride2Into(dst, src []celtNorm, n0 int) {
	DeinterleaveStereoIntoF32(src, dst[n0:n0<<1], dst[:n0])
}

func interleaveHadamardStride2Into(dst, src []celtNorm, n0 int) {
	InterleaveStereoIntoF32(src[n0:n0<<1], src[:n0], dst)
}

func deinterleaveHadamardStride4Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0<<1]
	row2 := dst[n0<<1 : n0*3]
	row3 := dst[n0*3 : n0<<2]
	for j, base := 0, 0; j < n0; j, base = j+1, base+4 {
		row3[j] = src[base]
		row0[j] = src[base+1]
		row2[j] = src[base+2]
		row1[j] = src[base+3]
	}
}

func interleaveHadamardStride4Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0<<1]
	row2 := src[n0<<1 : n0*3]
	row3 := src[n0*3 : n0<<2]
	for j, base := 0, 0; j < n0; j, base = j+1, base+4 {
		dst[base] = row3[j]
		dst[base+1] = row0[j]
		dst[base+2] = row2[j]
		dst[base+3] = row1[j]
	}
}

func deinterleaveHadamardStride8Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0<<1]
	row2 := dst[n0<<1 : n0*3]
	row3 := dst[n0*3 : n0<<2]
	row4 := dst[n0<<2 : n0*5]
	row5 := dst[n0*5 : n0*6]
	row6 := dst[n0*6 : n0*7]
	row7 := dst[n0*7 : n0<<3]
	for j, base := 0, 0; j < n0; j, base = j+1, base+8 {
		row7[j] = src[base]
		row0[j] = src[base+1]
		row4[j] = src[base+2]
		row3[j] = src[base+3]
		row6[j] = src[base+4]
		row1[j] = src[base+5]
		row5[j] = src[base+6]
		row2[j] = src[base+7]
	}
}

func interleaveHadamardStride8Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0<<1]
	row2 := src[n0<<1 : n0*3]
	row3 := src[n0*3 : n0<<2]
	row4 := src[n0<<2 : n0*5]
	row5 := src[n0*5 : n0*6]
	row6 := src[n0*6 : n0*7]
	row7 := src[n0*7 : n0<<3]
	for j, base := 0, 0; j < n0; j, base = j+1, base+8 {
		dst[base] = row7[j]
		dst[base+1] = row0[j]
		dst[base+2] = row4[j]
		dst[base+3] = row3[j]
		dst[base+4] = row6[j]
		dst[base+5] = row1[j]
		dst[base+6] = row5[j]
		dst[base+7] = row2[j]
	}
}

func deinterleaveHadamardStride16Into(dst, src []celtNorm, n0 int) {
	row0 := dst[:n0]
	row1 := dst[n0 : n0<<1]
	row2 := dst[n0<<1 : n0*3]
	row3 := dst[n0*3 : n0<<2]
	row4 := dst[n0<<2 : n0*5]
	row5 := dst[n0*5 : n0*6]
	row6 := dst[n0*6 : n0*7]
	row7 := dst[n0*7 : n0<<3]
	row8 := dst[n0<<3 : n0*9]
	row9 := dst[n0*9 : n0*10]
	row10 := dst[n0*10 : n0*11]
	row11 := dst[n0*11 : n0*12]
	row12 := dst[n0*12 : n0*13]
	row13 := dst[n0*13 : n0*14]
	row14 := dst[n0*14 : n0*15]
	row15 := dst[n0*15 : n0<<4]
	for j, base := 0, 0; j < n0; j, base = j+1, base+16 {
		row15[j] = src[base]
		row0[j] = src[base+1]
		row8[j] = src[base+2]
		row7[j] = src[base+3]
		row12[j] = src[base+4]
		row3[j] = src[base+5]
		row11[j] = src[base+6]
		row4[j] = src[base+7]
		row14[j] = src[base+8]
		row1[j] = src[base+9]
		row9[j] = src[base+10]
		row6[j] = src[base+11]
		row13[j] = src[base+12]
		row2[j] = src[base+13]
		row10[j] = src[base+14]
		row5[j] = src[base+15]
	}
}

func interleaveHadamardStride16Into(dst, src []celtNorm, n0 int) {
	row0 := src[:n0]
	row1 := src[n0 : n0<<1]
	row2 := src[n0<<1 : n0*3]
	row3 := src[n0*3 : n0<<2]
	row4 := src[n0<<2 : n0*5]
	row5 := src[n0*5 : n0*6]
	row6 := src[n0*6 : n0*7]
	row7 := src[n0*7 : n0<<3]
	row8 := src[n0<<3 : n0*9]
	row9 := src[n0*9 : n0*10]
	row10 := src[n0*10 : n0*11]
	row11 := src[n0*11 : n0*12]
	row12 := src[n0*12 : n0*13]
	row13 := src[n0*13 : n0*14]
	row14 := src[n0*14 : n0*15]
	row15 := src[n0*15 : n0<<4]
	for j, base := 0, 0; j < n0; j, base = j+1, base+16 {
		dst[base] = row15[j]
		dst[base+1] = row0[j]
		dst[base+2] = row8[j]
		dst[base+3] = row7[j]
		dst[base+4] = row12[j]
		dst[base+5] = row3[j]
		dst[base+6] = row11[j]
		dst[base+7] = row4[j]
		dst[base+8] = row14[j]
		dst[base+9] = row1[j]
		dst[base+10] = row9[j]
		dst[base+11] = row6[j]
		dst[base+12] = row13[j]
		dst[base+13] = row2[j]
		dst[base+14] = row10[j]
		dst[base+15] = row5[j]
	}
}

func deinterleaveHadamard(x []celtNorm, n0, stride int, hadamard bool) {
	deinterleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func deinterleaveHadamardInto(dst, src []celtNorm, n0, stride int, hadamard bool) {
	n := n0 * stride
	dst = dst[:n]
	src = src[:n]
	if hadamard {
		switch stride {
		case 2:
			deinterleaveHadamardStride2Into(dst, src, n0)
		case 4:
			deinterleaveHadamardStride4Into(dst, src, n0)
		case 8:
			deinterleaveHadamardStride8Into(dst, src, n0)
		case 16:
			deinterleaveHadamardStride16Into(dst, src, n0)
		default:
			ordery := orderyForStride(stride)
			for i := range stride {
				row := ordery[i] * n0
				for j := range n0 {
					dst[row+j] = src[j*stride+i]
				}
			}
		}
		return
	}
	switch stride {
	case 2:
		DeinterleaveStereoIntoF32(src, dst[:n0], dst[n0:n])
	case 3:
		n1 := n0
		n2 := n0 << 1
		for j := range n0 {
			base := j * 3
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
		}
	case 4:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		for j := range n0 {
			base := j << 2
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
		}
	case 8:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		for j := range n0 {
			base := j << 3
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
		}
	case 12:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		for j := range n0 {
			base := j * 12
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
			dst[n8+j] = src[base+8]
			dst[n9+j] = src[base+9]
			dst[n10+j] = src[base+10]
			dst[n11+j] = src[base+11]
		}
	case 16:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		n12 := n0 * 12
		n13 := n12 + n0
		n14 := n12 + n2
		n15 := n12 + n3
		for j := range n0 {
			base := j << 4
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
			dst[n6+j] = src[base+6]
			dst[n7+j] = src[base+7]
			dst[n8+j] = src[base+8]
			dst[n9+j] = src[base+9]
			dst[n10+j] = src[base+10]
			dst[n11+j] = src[base+11]
			dst[n12+j] = src[base+12]
			dst[n13+j] = src[base+13]
			dst[n14+j] = src[base+14]
			dst[n15+j] = src[base+15]
		}
	case 6:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		for j := range n0 {
			base := j * 6
			dst[j] = src[base]
			dst[n1+j] = src[base+1]
			dst[n2+j] = src[base+2]
			dst[n3+j] = src[base+3]
			dst[n4+j] = src[base+4]
			dst[n5+j] = src[base+5]
		}
	default:
		for i := range stride {
			row := i * n0
			for j := range n0 {
				dst[row+j] = src[j*stride+i]
			}
		}
	}
}

func deinterleaveHadamardScratchBuf(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	deinterleaveHadamardInto(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func deinterleaveHadamardIntoNorm(dst, src []celtNorm, n0, stride int, hadamard bool) {
	n := n0 * stride
	dst = dst[:n]
	src = src[:n]
	if hadamard {
		ordery := orderyForStride(stride)
		if len(ordery) >= stride {
			for i := range stride {
				row := ordery[i] * n0
				for j := range n0 {
					dst[row+j] = src[j*stride+i]
				}
			}
			return
		}
	}
	for i := range stride {
		row := i * n0
		for j := range n0 {
			dst[row+j] = src[j*stride+i]
		}
	}
}

func deinterleaveHadamardScratchBufNorm(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	deinterleaveHadamardIntoNorm(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func interleaveHadamard(x []celtNorm, n0, stride int, hadamard bool) {
	interleaveHadamardScratchBuf(x, n0, stride, hadamard, nil, nil)
}

func interleaveHadamardInto(dst, src []celtNorm, n0, stride int, hadamard bool) {
	n := n0 * stride
	dst = dst[:n]
	src = src[:n]
	if hadamard {
		switch stride {
		case 2:
			interleaveHadamardStride2Into(dst, src, n0)
		case 4:
			interleaveHadamardStride4Into(dst, src, n0)
		case 8:
			interleaveHadamardStride8Into(dst, src, n0)
		case 16:
			interleaveHadamardStride16Into(dst, src, n0)
		default:
			ordery := orderyForStride(stride)
			for i := range stride {
				row := ordery[i] * n0
				for j := range n0 {
					dst[j*stride+i] = src[row+j]
				}
			}
		}
		return
	}
	switch stride {
	case 2:
		InterleaveStereoIntoF32(src[:n0], src[n0:n], dst)
	case 3:
		n1 := n0
		n2 := n0 << 1
		for j := range n0 {
			base := j * 3
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
		}
	case 4:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		for j := range n0 {
			base := j << 2
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
		}
	case 8:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		for j := range n0 {
			base := j << 3
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
		}
	case 12:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		for j := range n0 {
			base := j * 12
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
			dst[base+8] = src[n8+j]
			dst[base+9] = src[n9+j]
			dst[base+10] = src[n10+j]
			dst[base+11] = src[n11+j]
		}
	case 16:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		n6 := n4 + n2
		n7 := n4 + n3
		n8 := n0 << 3
		n9 := n8 + n0
		n10 := n8 + n2
		n11 := n8 + n3
		n12 := n0 * 12
		n13 := n12 + n0
		n14 := n12 + n2
		n15 := n12 + n3
		for j := range n0 {
			base := j << 4
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
			dst[base+6] = src[n6+j]
			dst[base+7] = src[n7+j]
			dst[base+8] = src[n8+j]
			dst[base+9] = src[n9+j]
			dst[base+10] = src[n10+j]
			dst[base+11] = src[n11+j]
			dst[base+12] = src[n12+j]
			dst[base+13] = src[n13+j]
			dst[base+14] = src[n14+j]
			dst[base+15] = src[n15+j]
		}
	case 6:
		n1 := n0
		n2 := n0 << 1
		n3 := n2 + n0
		n4 := n0 << 2
		n5 := n4 + n0
		for j := range n0 {
			base := j * 6
			dst[base] = src[j]
			dst[base+1] = src[n1+j]
			dst[base+2] = src[n2+j]
			dst[base+3] = src[n3+j]
			dst[base+4] = src[n4+j]
			dst[base+5] = src[n5+j]
		}
	default:
		for i := range stride {
			row := i * n0
			for j := range n0 {
				dst[j*stride+i] = src[row+j]
			}
		}
	}
}

func interleaveHadamardScratchBuf(x []celtNorm, n0, stride int, hadamard bool, decScratch *bandDecodeScratch, encScratch *bandEncodeScratch) {
	n := n0 * stride
	var tmp []celtNorm
	if decScratch != nil {
		tmp = decScratch.ensureHadamardTmpNorm(n)
	} else if encScratch != nil {
		tmp = encScratch.ensureHadamardTmpNorm(n)
	} else {
		tmp = make([]celtNorm, n)
	}
	interleaveHadamardInto(tmp, x, n0, stride, hadamard)
	copy(x, tmp)
}

func haar1(x []celtNorm, n0, stride int) {
	n0 >>= 1
	if n0 <= 0 || stride <= 0 {
		return
	}

	const invSqrt2 = float32(0.7071067811865476)

	step := stride * 2
	// BCE hint: maximum index accessed is (stride-1) + stride + (n0-1)*step
	maxIdx := stride - 1 + stride + (n0-1)*step
	if maxIdx >= len(x) {
		return
	}
	_ = x[maxIdx]
	switch stride {
	case 1:
		haar1Stride1NEON(x[:2*n0:2*n0], n0)
		return
	case 2:
		haar1Stride2NEON(x[:4*n0:4*n0], n0)
		return
	case 4:
		haar1Stride4NEON(x[:8*n0:8*n0], n0)
		return
	}
	for i := range stride {
		idx0 := i
		idx1 := i + stride
		for j := 0; j < n0; j++ {
			haar1PairNorm(x, idx0, idx1, invSqrt2)
			idx0 += step
			idx1 += step
		}
	}
}

func haar1PairNorm(x []celtNorm, idx0, idx1 int, invSqrt2 float32) {
	tmp1 := noFMA32Mul(invSqrt2, float32(x[idx0]))
	tmp2 := noFMA32Mul(invSqrt2, float32(x[idx1]))
	x[idx0] = celtNorm(noFMA32Add(tmp1, tmp2))
	x[idx1] = celtNorm(noFMA32Sub(tmp1, tmp2))
}

func expRotation1(x []celtNorm, length, stride int, c, s opusVal16) {
	expRotation1Norm(x, length, stride, c, s)
}

func expRotation1Norm(x []celtNorm, length, stride int, c, s opusVal16) {
	if length <= 0 {
		return
	}
	x = x[:length:length]
	if stride <= 0 || stride >= length {
		return
	}
	// With stride >= 4 four consecutive indices belong to four independent
	// rotation chains, so the fused arm64 build runs both passes 4-wide
	// (bit-identical per element); the scalar loops stay the purego oracle
	// and the stride<4 path.
	if expRotationUsesNeon && stride >= 4 {
		expRotation1StrideNeon(x, length, stride, c, s)
		return
	}
	expRotation1NormScalar(x, length, stride, c, s)
}

func expRotation1NormScalar(x []celtNorm, length, stride int, c, s opusVal16) {
	// xs[i] aliases x[i+stride] and has exactly length-stride elements — the
	// trip count of the forward pass — so every access below is bounds-check
	// free. The rotation itself is a serial cascade (each pair reads the
	// previous pair's write), so only loop overhead is reducible here.
	xs := x[stride:]
	c32 := float32(c)
	s32 := float32(s)
	ms32 := -s32

	for i := range xs {
		x1 := float32(x[i])
		x2 := float32(xs[i])
		xs[i] = celtNorm(expRotationMac32(c32, x2, s32, x1))
		x[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
	}
	if n2 := length - 2*stride; n2 > 0 {
		xb, xsb := x[:n2], xs[:n2]
		for i := len(xsb) - 1; i >= 0; i-- {
			x1 := float32(xb[i])
			x2 := float32(xsb[i])
			xsb[i] = celtNorm(expRotationMac32(c32, x2, s32, x1))
			xb[i] = celtNorm(expRotationMac32(c32, x1, ms32, x2))
		}
	}
}

func expRotationMac32(a, b, c, d float32) float32 {
	return fma32(a, b, noFMA32Mul(c, d))
}

func expRotation(x []celtNorm, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	c, s, ok := expRotationCoefficients(length, k, spread)
	if !ok {
		spreadFactor := expRotationSpreadFactors[spread-1]
		gain := float32(length) / float32(length+spreadFactor*k)
		theta := 0.5 * gain * gain
		c = opusVal16(opusmath.CELTCosNormF32(theta))
		s = opusVal16(opusmath.CELTCosNormF32(float32(1) - theta))
	}

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	length = celtUdiv(length, stride)
	for i := range stride {
		off := i * length
		if dir < 0 {
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, c)
			}
			expRotation1(x[off:], length, 1, c, s)
		} else {
			expRotation1(x[off:], length, 1, c, -s)
			if stride2 != 0 {
				expRotation1(x[off:], length, stride2, s, -c)
			}
		}
	}
}

func expRotationNorm(x []celtNorm, length, dir, stride, k, spread int) {
	if 2*k >= length || spread == spreadNone {
		return
	}
	c, s, ok := expRotationCoefficients(length, k, spread)
	if !ok {
		spreadFactor := expRotationSpreadFactors[spread-1]
		gain := float32(length) / float32(length+spreadFactor*k)
		theta := 0.5 * gain * gain
		c = opusVal16(opusmath.CELTCosNormF32(theta))
		s = opusVal16(opusmath.CELTCosNormF32(float32(1) - theta))
	}

	stride2 := 0
	if length >= 8*stride {
		stride2 = 1
		for (stride2*stride2+stride2)*stride+(stride>>2) < length {
			stride2++
		}
	}
	length = celtUdiv(length, stride)
	for i := range stride {
		off := i * length
		if dir < 0 {
			if stride2 != 0 {
				expRotation1Norm(x[off:], length, stride2, s, c)
			}
			expRotation1Norm(x[off:], length, 1, c, s)
		} else {
			expRotation1Norm(x[off:], length, 1, c, -s)
			if stride2 != 0 {
				expRotation1Norm(x[off:], length, stride2, s, -c)
			}
		}
	}
}

func extractCollapseMask(pulses []int32, n, b int) int {
	if b <= 1 {
		return 1
	}
	if n <= 0 {
		return 1
	}
	pulses = pulses[:n:n]
	_ = pulses[n-1] // BCE
	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		return 0
	}
	mask := 0
	base := 0
	for i := range b {
		tmp := int32(0)
		end := base + n0
		j := base
		for ; j+3 < end; j += 4 {
			tmp |= pulses[j] | pulses[j+1] | pulses[j+2] | pulses[j+3]
		}
		for ; j < end; j++ {
			tmp |= pulses[j]
		}
		if tmp != 0 {
			mask |= 1 << i
		}
		base = end
	}
	return mask
}

// normalizeResidualInto normalizes the pulse vector into a pre-allocated output buffer.
func normalizeResidualInto(out []celtNorm, pulses []int, gain opusVal16, yy opusVal16) {
	n := len(pulses)
	if len(out) < n {
		return
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		// Fall back to computing energy from pulses.
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		return
	}
	scale := celtRSqrt(energy) * float32(gain)
	i := 0
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
}

func normalizeResidualInto32(out []celtNorm, pulses []int32, gain opusVal16, yy opusVal16) {
	n := len(pulses)
	if len(out) < n {
		return
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		return
	}
	scale := celtRSqrt(energy) * float32(gain)
	i := 0
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
}

// normalizeResidualIntoAndCollapse normalizes the pulse vector into out and
// computes the collapse mask in the same pass.
func normalizeResidualIntoAndCollapse(out []celtNorm, pulses []int, gain opusVal16, yy opusVal16, b int) int {
	n := len(pulses)
	if len(out) < n {
		return 0
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		// Fall back to computing energy from pulses.
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	return normalizeResidualKnownEnergyIntoAndCollapse(out, pulses, gain, opusVal16(energy), b)
}

func normalizeResidualIntoAndCollapse32(out []celtNorm, pulses []int32, gain opusVal16, yy opusVal16, b int) int {
	n := len(pulses)
	if len(out) < n {
		return 0
	}
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy := float32(yy)
	if energy <= 0 {
		i := 0
		for ; i+3 < n; i += 4 {
			v0 := float32(pulses[i])
			v1 := float32(pulses[i+1])
			v2 := float32(pulses[i+2])
			v3 := float32(pulses[i+3])
			energy += v0*v0 + v1*v1 + v2*v2 + v3*v3
		}
		for ; i < n; i++ {
			v := float32(pulses[i])
			energy += v * v
		}
	}
	if energy <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	return normalizeResidualKnownEnergyIntoAndCollapse32(out, pulses, gain, opusVal16(energy), b)
}

func normalizeResidualKnownEnergyIntoAndCollapse(out []celtNorm, pulses []int, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := 0
		end := base + n0
		for i := base; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	// Handle any remaining tail elements when n is not divisible by b.
	for i := base; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

func normalizeResidualKnownEnergyIntoAndCollapseNorm(out []celtNorm, pulses []int, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := 0
		end := base + n0
		for i := base; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	for i := base; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

func normalizeResidualKnownEnergyIntoAndCollapse32(out []celtNorm, pulses []int32, gain opusVal16, energy opusVal16, b int) int {
	n := len(pulses)
	out = out[:n:n]
	pulses = pulses[:n:n]
	energy32 := float32(energy)
	if energy32 <= 0 {
		for i := range n {
			v := float32(pulses[i])
			energy32 += v * v
		}
	}
	if energy32 <= 0 {
		clear(out[:n])
		if b <= 1 {
			return 1
		}
		return 0
	}
	scale := celtRSqrt(energy32) * float32(gain)

	if b <= 1 {
		i := 0
		for ; i+3 < n; i += 4 {
			out[i] = celtNorm(float32(pulses[i]) * scale)
			out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
			out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
			out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
		}
		for ; i < n; i++ {
			out[i] = celtNorm(float32(pulses[i]) * scale)
		}
		return 1
	}

	n0 := celtUdiv(n, b)
	if n0 <= 0 {
		clear(out[:n])
		return 0
	}

	mask := 0
	base := 0
	for blk := range b {
		tmp := int32(0)
		end := base + n0
		i := base
		for ; i+3 < end; i += 4 {
			v0 := pulses[i]
			v1 := pulses[i+1]
			v2 := pulses[i+2]
			v3 := pulses[i+3]
			out[i] = celtNorm(float32(v0) * scale)
			out[i+1] = celtNorm(float32(v1) * scale)
			out[i+2] = celtNorm(float32(v2) * scale)
			out[i+3] = celtNorm(float32(v3) * scale)
			tmp |= v0 | v1 | v2 | v3
		}
		for ; i < end; i++ {
			v := pulses[i]
			out[i] = celtNorm(float32(v) * scale)
			tmp |= v
		}
		if tmp != 0 {
			mask |= 1 << blk
		}
		base = end
	}
	i := base
	for ; i+3 < n; i += 4 {
		out[i] = celtNorm(float32(pulses[i]) * scale)
		out[i+1] = celtNorm(float32(pulses[i+1]) * scale)
		out[i+2] = celtNorm(float32(pulses[i+2]) * scale)
		out[i+3] = celtNorm(float32(pulses[i+3]) * scale)
	}
	for ; i < n; i++ {
		out[i] = celtNorm(float32(pulses[i]) * scale)
	}
	return mask
}

func celtRSqrt(x float32) float32 {
	return float32(1) / opusmath.SqrtF32(x)
}

func celtMul32(a, b opusVal16) opusVal16 {
	return opusVal16(float32(a) * float32(b))
}

func renormalizeVector(x []celtNorm, gain opusVal16) {
	if len(x) == 0 {
		return
	}
	n := len(x)
	x = x[:n:n]
	var energy float32
	if celtUseFusedFloatMath {
		energy = celtInnerProdNeonStyleNorm(x, x)
	} else if celtUseSSEFloatMath {
		energy = celtInnerProdSSEStyleNorm(x, x)
	} else {
		for i := range x {
			v := float32(x[i])
			energy = celtFloatMulAdd(v, v, energy)
		}
	}
	energy = float32(1e-15) + energy
	renormalizeVectorWithEnergy(x, gain, opusVal16(energy))
}

func renormalizeVectorWithEnergy(x []celtNorm, gain, energy opusVal16) {
	if len(x) == 0 || energy <= 0 {
		return
	}
	n := len(x)
	x = x[:n:n]
	_ = x[n-1]
	scale := noFMA32Mul(celtRSqrt(float32(energy)), float32(gain))
	i := 0
	for ; i+3 < n; i += 4 {
		x[i] = celtNorm(noFMA32Mul(float32(x[i]), scale))
		x[i+1] = celtNorm(noFMA32Mul(float32(x[i+1]), scale))
		x[i+2] = celtNorm(noFMA32Mul(float32(x[i+2]), scale))
		x[i+3] = celtNorm(noFMA32Mul(float32(x[i+3]), scale))
	}
	for ; i < n; i++ {
		x[i] = celtNorm(noFMA32Mul(float32(x[i]), scale))
	}
}

func scaleLowbandOutForFoldingNorm(dst []celtNorm, src []celtNorm, n int) {
	if n <= 0 {
		return
	}
	dst = dst[:n:n]
	src = src[:n:n]
	scale := opusmath.SqrtF32(float32(n))
	for i := range n {
		dst[i] = celtNorm(scale * float32(src[i]))
	}
}

// seededZeroPulseResynth fuses zero-pulse fill/fold generation with the
// exact same energy accumulation order used by renormalizeVector.
func seededZeroPulseResynth(x []celtNorm, lowband []celtNorm, seed *uint32, gain opusVal16) bool {
	if seed == nil {
		return false
	}
	n := len(x)
	if n == 0 {
		return true
	}
	x = x[:n:n]
	_ = x[n-1]

	seedVal := *seed
	// Parallelize the LCG (s = s*A + C). uint32-modular arithmetic is exactly
	// associative, so the four taps of an unrolled step can each be expressed
	// from the step-entry seed via precomputed A^k (bit-identical to the serial
	// chain), leaving only A^4 on the dependency chain (~4x shorter) and letting
	// the per-element float conversions pipeline. Powers are runtime vars because
	// A*A overflows a typed uint32 const.
	lcgA := uint32(1664525)
	lcgC := uint32(1013904223)
	a2 := lcgA * lcgA
	a3 := a2 * lcgA
	a4 := a3 * lcgA
	c2 := lcgA*lcgC + lcgC
	c3 := lcgA*c2 + lcgC
	c4 := lcgA*c3 + lcgC
	if lowband == nil {
		i := 0
		for ; i+3 < n; i += 4 {
			s1 := lcgA*seedVal + lcgC
			s2 := a2*seedVal + c2
			s3 := a3*seedVal + c3
			s4 := a4*seedVal + c4
			x[i] = celtNorm(float32(int32(s1) >> 20))
			x[i+1] = celtNorm(float32(int32(s2) >> 20))
			x[i+2] = celtNorm(float32(int32(s3) >> 20))
			x[i+3] = celtNorm(float32(int32(s4) >> 20))
			seedVal = s4
		}
		for ; i < n; i++ {
			seedVal = seedVal*lcgA + lcgC
			x[i] = celtNorm(float32(int32(seedVal) >> 20))
		}
		*seed = seedVal
		renormalizeVector(x, gain)
		return true
	}

	if len(lowband) < n {
		return false
	}
	_ = lowband[n-1]

	const foldNoise = 1.0 / 256.0
	i := 0
	for ; i+3 < n; i += 4 {
		s1 := lcgA*seedVal + lcgC
		s2 := a2*seedVal + c2
		s3 := a3*seedVal + c3
		s4 := a4*seedVal + c4
		x[i] = celtNorm(float32(lowband[i]) + float32(foldNoise)*float32(int32(((s1>>15)&1)<<1)-1))
		x[i+1] = celtNorm(float32(lowband[i+1]) + float32(foldNoise)*float32(int32(((s2>>15)&1)<<1)-1))
		x[i+2] = celtNorm(float32(lowband[i+2]) + float32(foldNoise)*float32(int32(((s3>>15)&1)<<1)-1))
		x[i+3] = celtNorm(float32(lowband[i+3]) + float32(foldNoise)*float32(int32(((s4>>15)&1)<<1)-1))
		seedVal = s4
	}
	for ; i < n; i++ {
		seedVal = seedVal*lcgA + lcgC
		x[i] = celtNorm(float32(lowband[i]) + float32(foldNoise)*float32(int32(((seedVal>>15)&1)<<1)-1))
	}
	*seed = seedVal
	renormalizeVector(x, gain)
	return true
}

func stereoMerge(x, y []celtNorm, mid opusVal16) {
	n := len(x)
	if n == 0 || len(y) < n {
		return
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]
	mid32 := float32(mid)
	xp := float32(0)
	side := float32(0)
	if celtUseFusedFloatMath {
		xp = celtInnerProdNeonStyle(y, x)
		side = celtInnerProdNeonStyle(y, y)
	} else if celtUseSSEFloatMath {
		xp = celtInnerProdSSEStyle(y, x)
		side = celtInnerProdSSEStyle(y, y)
	} else {
		for i := range n {
			xv := float32(x[i])
			yv := float32(y[i])
			xp = celtFloatMulAdd(yv, xv, xp)
			side = celtFloatMulAdd(yv, yv, side)
		}
	}
	xp *= mid32
	mid2 := mid32 * mid32
	el := mid2 + side - float32(2)*xp
	er := mid2 + side + float32(2)*xp
	if el < float32(6e-4) || er < float32(6e-4) {
		copy(y, x[:n])
		return
	}
	lgain := celtRSqrt(el)
	rgain := celtRSqrt(er)
	// libopus rounds l before ADD32/SUB32; the kernel keeps every op a bare
	// FMUL/FADD/FSUB (no mid*x +/- r contraction) so it stays bit-exact on the
	// fused arm64 build too.
	stereoMergeRescaleNEON(x, y, mid32, lgain, rgain)
}

func specialHybridFoldingWithEdges(norm, norm2 []celtNorm, edges []int, start, M int, dualStereo bool) {
	if start+2 >= len(edges) {
		return
	}
	n1 := M * (edges[start+1] - edges[start])
	n2 := M * (edges[start+2] - edges[start+1])
	if n2 <= n1 {
		return
	}
	src := 2*n1 - n2
	dst := n1
	count := n2 - n1
	if src < 0 || src+count > len(norm) || dst+count > len(norm) {
		return
	}
	copy(norm[dst:dst+count], norm[src:src+count])
	if dualStereo && norm2 != nil {
		if src < 0 || src+count > len(norm2) || dst+count > len(norm2) {
			return
		}
		copy(norm2[dst:dst+count], norm2[src:src+count])
	}
}

func algUnquantNoExtInto(shape []celtNorm, rd *rangecoding.Decoder, n, k, spread, b int, gain opusVal16, scratch *bandDecodeScratch) int {
	if len(shape) < n {
		return 0
	}
	shape = shape[:n:n]
	if k <= 0 || n <= 0 {
		clear(shape)
		return 0
	}
	if rd == nil {
		clear(shape)
		return 0
	}

	vSize := PVQ_V(n, k)
	if vSize == 0 {
		clear(shape)
		return 0
	}
	var idx uint32
	if vSize <= 1<<rangecoding.EC_UINT_BITS {
		idx = rd.DecodeUniformSmall(vSize)
	} else {
		idx = rd.DecodeUniform(vSize)
	}

	var pulses []int32
	if scratch != nil {
		pulses = scratch.ensurePVQPulses(n)
	} else {
		pulses = make([]int32, n)
	}
	yy := opusVal16(decodePulsesInto32(idx, n, k, pulses, scratch))
	// Normalize and rotate directly into the caller's shape buffer. shape is a
	// distinct float32 destination from the int32 pulses scratch, and
	// normalizeResidual/expRotation are point-wise (out[i] from pulses[i], then
	// in-place rotation), so writing into shape is bit-identical to the prior
	// scratch+copy and drops one frame-size clear and memmove per band.
	cm := normalizeResidualKnownEnergyIntoAndCollapse32(shape, pulses, opusVal16(gain), yy, b)
	expRotationNorm(shape, n, -1, b, k, spread)
	return cm
}

// algUnquantInto decodes PVQ into a pre-allocated shape buffer using scratch buffers.
func algUnquantInto(shape []celtNorm, rd *rangecoding.Decoder, band, n, k, spread, b int, gain opusVal16, extDec *rangecoding.Decoder, extraBits int, scratch *bandDecodeScratch) int {
	if extraBits < 2 || extDec == nil {
		return algUnquantNoExtInto(shape, rd, n, k, spread, b, gain, scratch)
	}

	if len(shape) < n {
		return 0
	}
	shape = shape[:n:n]
	if k <= 0 || n <= 0 {
		clear(shape)
		return 0
	}
	if rd == nil {
		clear(shape)
		return 0
	}

	vSize := PVQ_V(n, k)
	if vSize == 0 {
		clear(shape)
		return 0
	}
	var idx uint32
	if vSize <= 1<<rangecoding.EC_UINT_BITS {
		idx = rd.DecodeUniformSmall(vSize)
	} else {
		idx = rd.DecodeUniform(vSize)
	}

	var pulses []int32
	if scratch != nil {
		pulses = scratch.ensurePVQPulses(n)
	} else {
		pulses = make([]int32, n)
	}
	decodePulsesInto32(idx, n, k, pulses, scratch)
	var yy opusVal16
	up := (1 << extraBits) - 1
	up32 := int32(up)
	if n == 2 {
		refine := int32(extDec.DecodeUniform(uint32(up))) - int32((up-1)/2)
		pulses[0] *= up32
		pulses[1] *= up32
		if pulses[1] == 0 {
			if pulses[0] > 0 {
				pulses[1] = -refine
			} else {
				pulses[1] = refine
			}
			if refine*pulses[0] > 0 {
				pulses[0] -= refine
			} else {
				pulses[0] += refine
			}
		} else if pulses[1] > 0 {
			pulses[0] += refine
			if pulses[0] > 0 {
				pulses[1] -= refine
			} else {
				pulses[1] += refine
			}
		} else {
			pulses[0] -= refine
			if pulses[0] > 0 {
				pulses[1] -= refine
			} else {
				pulses[1] += refine
			}
		}
		yy0 := float32(pulses[0]) * float32(pulses[0])
		yy1 := float32(pulses[1]) * float32(pulses[1])
		yy = opusVal16(yy0 + yy1)
	} else {
		refine := make([]int32, n)
		useEntropy := (extDec.StorageBits() - extDec.Tell()) > (n-1)*(extraBits+3)+1
		for i := 0; i < n-1; i++ {
			refine[i] = int32(ecDecRefine(extDec, up, extraBits, useEntropy))
		}
		sign := 0
		if pulses[n-1] == 0 {
			sign = int(extDec.DecodeRawBit())
		} else if pulses[n-1] < 0 {
			sign = 1
		}
		for i := 0; i < n-1; i++ {
			pulses[i] = pulses[i]*up32 + refine[i]
		}
		last := up32 * int32(k)
		for i := 0; i < n-1; i++ {
			v := pulses[i]
			if v < 0 {
				v = -v
			}
			last -= v
		}
		if sign != 0 {
			last = -last
		}
		pulses[n-1] = last
		sumSq := opusVal16(0)
		for i := range n {
			sumSq = opusVal16(float32(sumSq) + float32(pulses[i])*float32(pulses[i]))
		}
		yy = sumSq
	}
	// Normalize and rotate directly into shape (see algUnquantNoExtInto).
	cm := normalizeResidualKnownEnergyIntoAndCollapse32(shape, pulses, opusVal16(gain), yy, b)
	expRotationNorm(shape, n, -1, b, k, spread)
	return cm
}

func algQuantScratch(re *rangecoding.Encoder, band int, x []celtNorm, n, k, spread, b int, gain opusVal16, resynth bool, extEnc *rangecoding.Encoder, extraBits int, scratch *bandEncodeScratch) int {
	if k <= 0 || n <= 0 {
		return 0
	}
	if re == nil {
		return 0
	}

	// Quantize the vector to pulses.
	var pulses []int32
	var upPulses []int32
	var collapsePulses []int32
	var refine []int32
	var yy opusVal16 // Energy computed during PVQ search
	encodedIndex := uint32(0)
	var xNorm []celtNorm
	var yy32 opusVal16
	normPath := false
	// xNormAliased is set when the no-extra-bits path works the normalized vector
	// directly in the caller's x buffer (no clone): the search, resynth and inverse
	// rotation all write x in place, so the trailing copy(x, xNorm) is a self-copy
	// and is skipped. End state of x is byte-identical to the clone+copy form.
	xNormAliased := false

	// Scratch buffer pointers
	var iyBuf *[]int32
	var signxBuf *[]byte
	var yBuf, absXBuf *[]float32
	var xNormBuf *[]celtNorm
	var uBuf *[]uint32
	if scratch != nil {
		iyBuf = &scratch.pvqIy
		signxBuf = &scratch.pvqSignx
		yBuf = &scratch.pvqY
		absXBuf = &scratch.pvqAbsX
		xNormBuf = &scratch.pvqX
		uBuf = &scratch.cwrsU
	}

	if extraBits >= 2 && extEnc != nil {
		if xNormBuf != nil {
			xNorm = ensureNormSliceNoClear(xNormBuf, n)
		} else {
			xNorm = make([]celtNorm, n)
		}
		copy(xNorm, x[:n])
		expRotationNorm(xNorm, n, 1, b, k, spread)
		normPath = true
		if n == 2 {
			var refineVal int32
			up := (1 << extraBits) - 1
			pulses, upPulses, refineVal, yy32 = opPVQSearchN2Norm(xNorm, k, up)
			yy = yy32
			collapsePulses = upPulses
			index := encodePulsesFast32(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
			encodedIndex = index
			extEnc.EncodeUniform(uint32(refineVal+int32((up-1)/2)), uint32(up))
		} else {
			up := (1 << extraBits) - 1
			pulses, upPulses, refine, yy32 = opPVQSearchExtraNorm(xNorm, k, up)
			yy = yy32
			collapsePulses = upPulses
			index := encodePulsesFast32(pulses, n, k, uBuf)
			vSize := PVQ_V(n, k)
			if vSize == 0 {
				return 0
			}
			re.EncodeUniform(index, vSize)
			encodedIndex = index
			useEntropy := (extEnc.StorageBits() - extEnc.Tell()) > (n-1)*(extraBits+3)+1
			for i := 0; i < n-1; i++ {
				ecEncRefine(extEnc, refine[i], up, extraBits, useEntropy)
			}
			if pulses[n-1] == 0 {
				sign := 0
				if upPulses[n-1] < 0 {
					sign = 1
				}
				extEnc.EncodeRawBits(uint32(sign), 1)
			}
		}
	} else {
		// Work in place on the caller's x: forward rotation, PVQ search (with its
		// abs writeback), resynth and inverse rotation all target x directly,
		// removing the per-band clone and the trailing copy-back.
		xNorm = x[:n:n]
		xNormAliased = true
		expRotationNorm(xNorm, n, 1, b, k, spread)
		pulses, yy32 = opPVQSearchScratchNormWithInputMutation(xNorm, k, iyBuf, signxBuf, yBuf, absXBuf, true)
		yy = yy32
		collapsePulses = pulses
		normPath = true
		index := encodePulsesFast32(pulses, n, k, uBuf)
		vSize := PVQ_V(n, k)
		if vSize == 0 {
			return 0
		}
		re.EncodeUniform(index, vSize)
		encodedIndex = index
	}

	cm := 0
	if resynth {
		if len(upPulses) > 0 {
			if len(collapsePulses) > 0 {
				cm = extractCollapseMask(collapsePulses, n, b)
			}
			if normPath {
				cm = normalizeResidualKnownEnergyIntoAndCollapse32(xNorm, upPulses, opusVal16(gain), yy32, b)
				expRotationNorm(xNorm, n, -1, b, k, spread)
				copy(x[:n], xNorm)
				_ = encodedIndex
				return cm
			}
			normalizeResidualInto32(x, upPulses, gain, yy)
		} else {
			// In the common path, collapse-mask extraction and residual
			// normalization can share the same scan over the pulse vector.
			if normPath {
				cm = normalizeResidualKnownEnergyIntoAndCollapse32(xNorm, pulses, opusVal16(gain), yy32, b)
				expRotationNorm(xNorm, n, -1, b, k, spread)
				if !xNormAliased {
					copy(x[:n], xNorm)
				}
				_ = encodedIndex
				return cm
			}
			cm = normalizeResidualIntoAndCollapse32(x, pulses, gain, yy, b)
		}
		expRotation(x, n, -1, b, k, spread)
	} else if len(collapsePulses) > 0 {
		cm = extractCollapseMask(collapsePulses, n, b)
	}
	if normPath && !xNormAliased {
		copy(x[:n], xNorm)
	}
	_ = encodedIndex

	return cm
}

var computeQnExp2Table = [...]int{16384, 17866, 19483, 21247, 23170, 25267, 27554, 30048}

func computeQn(n, b, offset, pulseCap int, stereo bool) int {
	n2 := 2*n - 1
	if stereo && n == 2 {
		n2--
	}
	qb := celtSudiv(b+n2*offset, n2)
	qb = min(b-pulseCap-(4<<bitRes), qb)
	qb = min(8<<bitRes, qb)
	if qb < (1 << (bitRes - 1)) {
		return 1
	}
	qn := computeQnExp2Table[qb&0x7] >> (14 - (qb >> bitRes))
	qn = min(((qn+1)>>1)<<1, 256)
	return qn
}

// stereoItheta computes the standard 14-bit theta value for stereo encoding.
// Returns itheta in range [0, 16384].
func stereoItheta(x, y []celtNorm, stereo bool) int {
	return stereoIthetaQ30(x, y, stereo) >> 16
}

// stereoIthetaQ30 computes the extended precision Q30 theta value for stereo encoding.
// This matches libopus stereo_itheta() which returns itheta in Q30 format.
// The value represents atan2(side, mid) * 2/pi, scaled to [0, 1<<30].
// Standard itheta (14-bit) can be obtained by shifting right by 16.
func stereoIthetaQ30(x, y []celtNorm, stereo bool) int {
	// stereoIthetaQ30Norm only reads x/y (sum of squares), so no scratch copy is needed.
	return stereoIthetaQ30Norm(x, y, stereo)
}

func stereoIthetaQ30Norm(x, y []celtNorm, stereo bool) int {
	if len(x) == 0 || len(y) == 0 {
		return 0
	}
	n := min(len(y), len(x))
	if n <= 0 {
		return 0
	}
	x = x[:n:n]
	y = y[:n:n]
	_ = x[n-1]
	_ = y[n-1]

	var emid, eside float32
	if stereo {
		for i := 0; i < n; i++ {
			xv := float32(x[i])
			yv := float32(y[i])
			m := xv + yv
			s := xv - yv
			emid = celtFloatMulAdd(m, m, emid)
			eside = celtFloatMulAdd(s, s, eside)
		}
	} else {
		if celtUseSSEFloatMath {
			emid = celtInnerProdSSEStyleNorm(x[:n], x[:n])
			eside = celtInnerProdSSEStyleNorm(y[:n], y[:n])
		} else if celtUseFusedFloatMath {
			emid = celtInnerProdNeonStyleNorm(x[:n], x[:n])
			eside = celtInnerProdNeonStyleNorm(y[:n], y[:n])
		} else {
			for i := 0; i < n; i++ {
				xv := float32(x[i])
				yv := float32(y[i])
				emid = celtFloatMulAdd(xv, xv, emid)
				eside = celtFloatMulAdd(yv, yv, eside)
			}
		}
	}

	if emid <= 0 && eside <= 0 {
		return 0
	}

	// Compute mid and side magnitudes
	mid := opusmath.SqrtF32(emid)
	side := opusmath.SqrtF32(eside)
	theta := float32(0.5) + (float32(65536)*float32(16384))*celtAtan2pNormF32(side, mid)
	return floor32ToInt(theta)
}

// celtAtan2pNormF32 matches libopus float-path arithmetic more closely.
func celtAtan2pNormF32(y, x float32) float32 {
	if x*x+y*y < 1e-18 {
		return 0
	}
	if y < x {
		return celtAtanNormF32(y / x)
	}
	return 1 - celtAtanNormF32(x/y)
}

const celtUseFusedFloatMath = runtime.GOARCH == "arm64"
const celtUseSSEFloatMath = libopusFloatInnerProdUsesSSEOrder

func celtFloatMulAdd(a, b, c float32) float32 {
	if celtUseFusedFloatMath {
		// libopus arm/pitch_neon_intr.c:celt_inner_prod_neon forces
		// vfmaq_f32 for NEON lanes; this is the scalar lane equivalent.
		// celtUseFusedFloatMath is true only on arm64, where fma32 contracts
		// to one FMADDS — the same single rounding as mdctFMA32's math.FMA
		// without its FCVT round-trips (this is a runtime-data path, so the
		// constant-folding caveat that keeps mdctFMA32 on math.FMA does not
		// apply).
		return fma32(a, b, c)
	}
	return a*b + c
}

func celtInnerProdSSEStyle(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleAsm(x, y)
}

func celtInnerProdSSEStyleGo(x, y []celtNorm) float32 {
	var acc [4]float32
	i := 0
	for ; i < len(x)-3; i += 4 {
		for lane := range 4 {
			product := float32(x[i+lane]) * float32(y[i+lane])
			acc[lane] += product
		}
	}
	sum0 := acc[0] + acc[2]
	sum1 := acc[1] + acc[3]
	sum := sum0 + sum1
	for ; i < len(x); i++ {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}

func celtInnerProdSSEStyleNorm(x, y []celtNorm) float32 {
	return celtInnerProdSSEStyleAsm(x, y)
}

// celtInnerProdNeonStyle reproduces libopus arm/pitch_neon_intr.c
// celt_inner_prod_neon: a 4-lane vfmaq_f32 accumulator over 8-element groups,
// a 4-element tail, the (acc0+acc2)+(acc1+acc3) reduction, and a scalar tail.
// celtInnerProd8FMA32 implements this in NEON asm on arm64 and a bit-identical
// math.FMA fallback under the purego tag.
func celtInnerProdNeonStyle(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	return celtInnerProd8FMA32(x[:n:n], y[:n:n], n)
}

func celtInnerProdNeonStyleNorm(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	return celtInnerProd8FMA32(x[:n:n], y[:n:n], n)
}

func celtInnerProdLibopusOrder(x, y []celtNorm) float32 {
	n := min(len(y), len(x))
	x = x[:n:n]
	y = y[:n:n]
	if celtUseFusedFloatMath {
		return celtInnerProdNeonStyle(x, y)
	}
	if celtUseSSEFloatMath {
		return celtInnerProdSSEStyle(x, y)
	}
	var sum float32
	for i := range x {
		sum = celtFloatMulAdd(float32(x[i]), float32(y[i]), sum)
	}
	return sum
}

func celtAtanNormF32(x float32) float32 {
	const (
		a1  float32 = 0.636619772367581
		a3  float32 = -0.3333165943622589
		a5  float32 = 0.19962704181671143
		a7  float32 = -0.13976582884788513
		a9  float32 = 0.09794234484434128
		a11 float32 = -0.057773590087890625
		a13 float32 = 0.023040136322379112
		a15 float32 = -0.0043554059229791164
	)
	xSq := x * x
	p := celtFloatMulAdd(xSq, a15, a13)
	p = celtFloatMulAdd(xSq, p, a11)
	p = celtFloatMulAdd(xSq, p, a9)
	p = celtFloatMulAdd(xSq, p, a7)
	p = celtFloatMulAdd(xSq, p, a5)
	p = celtFloatMulAdd(xSq, p, a3)
	return a1 * celtFloatMulAdd(x*xSq, p, x)
}

// celtCosNorm2F32 computes cos(pi/2 * x) for x in [0, 1].
// This is used for extended precision mid/side computation from Q30 theta.
// Matches libopus celt_cos_norm2().
func celtCosNorm2F32(x float32) float32 {
	xf := x
	xf = xf - 4*float32(floor32ToInt(0.25*float32(xf+1)))
	outputSign := float32(1)
	if xf > 1 {
		outputSign = -1
		xf -= 2
	}
	const (
		cosA0 float32 = 9.999999403953552246093750000000e-01
		cosA2 float32 = -1.233698248863220214843750000000000
		cosA4 float32 = 2.536507546901702880859375000000e-01
		cosA6 float32 = -2.08106283098459243774414062500e-02
		cosA8 float32 = 8.581906440667808055877685546875e-04
	)
	xSq := xf * xf
	p := celtFloatMulAdd(xSq, cosA8, cosA6)
	p = celtFloatMulAdd(xSq, p, cosA4)
	p = celtFloatMulAdd(xSq, p, cosA2)
	p = celtFloatMulAdd(xSq, p, cosA0)
	return outputSign * p
}

func thetaUsesQEXT(ctx *bandCtx) bool {
	return ctx != nil && (ctx.extEnc != nil || ctx.extDec != nil)
}

func thetaSplitGains(sctx *splitCtx, useQ30 bool) (mid, side float32) {
	if sctx == nil {
		return 0, 0
	}
	if useQ30 {
		theta := float32(sctx.ithetaQ30) * (1.0 / float32(1<<30))
		return celtCosNorm2F32(theta), celtCosNorm2F32(1.0 - theta)
	}
	return float32(sctx.imid) / 32768.0, float32(sctx.iside) / 32768.0
}

func stereoSplit(x, y []celtNorm) {
	if len(x) == 0 || len(y) == 0 {
		return
	}
	n := min(len(y), len(x))
	const invSqrt2 float32 = 0.70710678
	for i := 0; i < n; i++ {
		l := noFMA32Mul(invSqrt2, float32(x[i]))
		r := noFMA32Mul(invSqrt2, float32(y[i]))
		x[i] = celtNorm(noFMA32Add(l, r))
		y[i] = celtNorm(noFMA32Sub(r, l))
	}
}

func intensityStereoWeighted(x, y []celtNorm, leftEnergy, rightEnergy celtEner) {
	if len(x) == 0 || len(y) == 0 {
		return
	}
	n := min(len(y), len(x))
	if leftEnergy < 0 {
		leftEnergy = 0
	}
	if rightEnergy < 0 {
		rightEnergy = 0
	}
	left := float32(leftEnergy)
	right := float32(rightEnergy)
	const celtFloatEpsilon float32 = 1e-15
	norm := celtFloatEpsilon + opusmath.SqrtF32(celtFloatEpsilon+left*left+right*right)
	a1 := left / norm
	a2 := right / norm
	for i := 0; i < n; i++ {
		x[i] = celtNorm(noFMA32Add(noFMA32Mul(a1, float32(x[i])), noFMA32Mul(a2, float32(y[i]))))
	}
}

// computeChannelWeights computes channel weights for stereo RDO distortion calculation.
// This mirrors libopus bands.c compute_channel_weights().
// The weights account for inter-aural masking effects.
func computeChannelWeights(ex, ey celtEner) (w0, w1 float32) {
	ex32 := float32(ex)
	ey32 := float32(ey)
	minE := ex32
	if ey32 < minE {
		minE = ey32
	}
	// Adjustment to make the weights a bit more conservative.
	ex32 = ex32 + minE/3.0
	ey32 = ey32 + minE/3.0
	// Match libopus float path: no normalization, weights are raw adjusted energies.
	return ex32, ey32
}

func innerProductNorm(x, y []celtNorm) float32 {
	return celtInnerProdLibopusOrder(x, y)
}

func thetaRDODistortion(w0, w1 float32, xSave, xBand, ySave, yBand []celtNorm) float32 {
	return w0*innerProductNorm(xSave, xBand) + w1*innerProductNorm(ySave, yBand)
}

func (ctx *bandCtx) bandEnergy(channel int) celtEner {
	if ctx.bandE == nil || ctx.nbBands <= 0 || channel < 0 || channel >= ctx.channels {
		return 0
	}
	idx := channel*ctx.nbBands + ctx.band
	if idx < 0 || idx >= len(ctx.bandE) {
		return 0
	}
	return ctx.bandE[idx]
}

func (ctx *bandCtx) modeBandEdges() []int {
	if len(ctx.bandEdges) >= 2 {
		return ctx.bandEdges
	}
	return EBands[:]
}

func (ctx *bandCtx) modeBandCount() int {
	edges := ctx.modeBandEdges()
	if len(edges) < 2 {
		return 0
	}
	return len(edges) - 1
}

func (ctx *bandCtx) modeLogN(band int) int {
	if band >= 0 && band < len(ctx.bandLogN) {
		return ctx.bandLogN[band]
	}
	if band >= 0 && band < len(LogN) {
		return LogN[band]
	}
	return 0
}

func pulseCacheForBandTables(band, lm int, cacheIndex []int16, cacheBits []uint8, bands int) ([]uint8, bool) {
	if band < 0 || band >= bands {
		return nil, false
	}
	if lm < -1 {
		return nil, false
	}
	idx := (lm + 1) * bands
	if idx < 0 || idx+band >= len(cacheIndex) {
		return nil, false
	}
	start := int(cacheIndex[idx+band])
	if start < 0 || start >= len(cacheBits) {
		return nil, false
	}
	cache := cacheBits[start:]
	if len(cache) == 0 {
		return nil, false
	}
	maxPseudo := int(cache[0])
	if maxPseudo <= 0 || maxPseudo >= len(cache) {
		return nil, false
	}
	return cache, true
}

func (ctx *bandCtx) pulseCacheForBand(lm int) ([]uint8, bool) {
	if len(ctx.cacheIndex) != 0 && len(ctx.cacheBits) != 0 {
		return pulseCacheForBandTables(ctx.band, lm, ctx.cacheIndex, ctx.cacheBits, ctx.modeBandCount())
	}
	return pulseCacheForBand(ctx.band, lm)
}

func (ctx *bandCtx) bitsToPulses(lm, bitsQ3 int) int {
	if bitsQ3 <= 0 {
		return 0
	}
	if cache, ok := ctx.pulseCacheForBand(lm); ok {
		return bitsToPulsesCached(cache, bitsQ3)
	}
	return bitsToPulses(ctx.band, lm, bitsQ3)
}

func (ctx *bandCtx) pulsesToBits(lm, pulses int) int {
	if pulses <= 0 {
		return 0
	}
	if cache, ok := ctx.pulseCacheForBand(lm); ok {
		return pulsesToBitsCached(cache, pulses)
	}
	return pulsesToBits(ctx.band, lm, pulses)
}

// computeTheta computes and encodes/decodes the stereo theta angle.
// This is the standard version without extended precision support.
func computeTheta(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	computeThetaWithExtBudget(ctx, sctx, x, y, n, b, nil, B, B0, lm, stereo, fill)
}

func computeThetaWithExtBudget(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, extB *int, B, B0, lm int, stereo bool, fill *int) {
	if !ctx.encode && (ctx.extDec == nil || extB == nil || *extB <= 0) {
		computeThetaDecode(ctx, sctx, x, y, n, b, B, B0, lm, stereo, fill)
		return
	}
	computeThetaExt(ctx, sctx, x, y, n, b, extB, B, B0, lm, stereo, fill)
}

func computeThetaDecode(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, B, B0, lm int, stereo bool, fill *int) {
	bIn := *b
	pulseCap := ctx.modeLogN(ctx.band) + lm*(1<<bitRes)
	offset := (pulseCap >> 1) - qthetaOffset
	if stereo && n == 2 {
		offset = (pulseCap >> 1) - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	itheta := 0
	ithetaQ30 := 0
	inv := 0
	tell := 0
	measureQalloc := false
	if qn != 1 {
		tell = ctx.rd.TellFrac()
		measureQalloc = true
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			fm := int(ctx.rd.Decode(uint32(ft)))
			if fm < (x0+1)*p0 {
				itheta = fm / p0
			} else {
				itheta = x0 + 1 + (fm - (x0+1)*p0)
			}
			var fl int
			if itheta <= x0 {
				fl = p0 * itheta
				ctx.rd.Update(uint32(fl), uint32(fl+p0), uint32(ft))
			} else {
				fl = (itheta - 1 - x0) + (x0+1)*p0
				ctx.rd.Update(uint32(fl), uint32(fl+1), uint32(ft))
			}
		} else if B0 > 1 || stereo {
			ft := uint32(qn + 1)
			if ft <= 1<<8 {
				itheta = int(ctx.rd.DecodeUniformSmall(ft))
			} else {
				itheta = int(ctx.rd.DecodeUniform(ft))
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			fm := int(ctx.rd.Decode(uint32(ft)))
			if fm < ((qn >> 1) * ((qn >> 1) + 1) >> 1) {
				itheta = int((isqrt32(uint32(8*fm+1)) - 1) >> 1)
				fs := itheta + 1
				fl := itheta * (itheta + 1) >> 1
				ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
			} else {
				itheta = int((2*(qn+1) - int(isqrt32(uint32(8*(ft-fm-1)+1)))) >> 1)
				fs := qn + 1 - itheta
				fl := ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
				ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
			}
		}
		itheta = celtUdiv(itheta*16384, qn)
		ithetaQ30 = itheta << 16
	} else if stereo {
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			tell = ctx.rd.TellFrac()
			measureQalloc = true
			inv = ctx.rd.DecodeBit(2)
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
		ithetaQ30 = 0
	}

	if measureQalloc {
		qalloc := ctx.rd.TellFrac() - tell
		if qalloc != 0 {
			*b -= qalloc
			sctx.qalloc = qalloc
		}
	}

	imid := 0
	iside := 0
	delta := 0
	if itheta == 0 {
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	} else if itheta == 16384 {
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	} else {
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
	_ = bIn
}

// computeThetaExt computes and encodes/decodes the stereo theta angle with optional extended precision.
// When extended precision is available (ctx.extEnc != nil and extB != nil),
// it also encodes additional Q30 precision bits to the extension bitstream.
// Reference: libopus bands.c compute_theta() with ENABLE_QEXT path (lines 863-885)
func computeThetaExt(ctx *bandCtx, sctx *splitCtx, x, y []celtNorm, n int, b *int, extB *int, B, B0, lm int, stereo bool, fill *int) {
	bIn := *b
	pulseCap := ctx.modeLogN(ctx.band) + lm*(1<<bitRes)
	offset := (pulseCap >> 1) - qthetaOffset
	if stereo && n == 2 {
		offset = (pulseCap >> 1) - qthetaOffsetTwoPhase
	}
	qn := computeQn(n, *b, offset, pulseCap, stereo)
	if stereo && ctx.band >= ctx.intensity {
		qn = 1
	}

	tell := 0
	if ctx.encode {
		if ctx.re != nil {
			tell = ctx.re.TellFrac()
		}
	} else if ctx.rd != nil {
		tell = ctx.rd.TellFrac()
	}
	itheta := 0
	ithetaQ30 := 0
	rawItheta := 0
	inv := 0
	if ctx.encode {
		// Match libopus: derive raw theta before qn decisions so qn==1
		// can still drive phase inversion signaling.
		ithetaQ30 = stereoIthetaQ30Norm(x, y, stereo)
		itheta = ithetaQ30 >> 16
		rawItheta = itheta
	}
	if qn != 1 {
		if ctx.encode {
			// Apply theta biasing for stereo RDO.
			// When thetaRound == 0: normal rounding (default)
			// When thetaRound != 0: bias toward 0/16384 (away from equal split)
			// Reference: libopus bands.c compute_theta(), lines 787-796
			if !stereo || ctx.thetaRound == 0 {
				// Standard rounding
				itheta = (itheta*qn + 8192) >> 14
				if !stereo && ctx.avoidSplitNoise && itheta > 0 && itheta < qn {
					unquantized := celtUdiv(itheta*16384, qn)
					delta := fracMul16((n-1)<<7, bitexactLog2tanTheta(unquantized))
					if delta > *b {
						itheta = qn
					} else if delta < -*b {
						itheta = 0
					}
				}
			} else {
				// Stereo theta RDO biasing: bias toward 0 or 16384
				// (away from equal split at itheta=8192).
				// bias is positive when itheta > 8192, negative otherwise
				bias := 0
				if itheta > 8192 {
					bias = 32767 / qn
				} else {
					bias = -32767 / qn
				}
				down := min(max((itheta*qn+bias)>>14, 0), qn-1)
				if ctx.thetaRound < 0 {
					itheta = down
				} else {
					itheta = down + 1
				}
			}
		}
		if stereo && n > 2 {
			p0 := 3
			x0 := qn / 2
			ft := p0*(x0+1) + x0
			if ctx.encode {
				if ctx.re != nil {
					var fl, fs int
					if itheta <= x0 {
						fl = p0 * itheta
						fs = p0
					} else {
						fl = (itheta - 1 - x0) + (x0+1)*p0
						fs = 1
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < (x0+1)*p0 {
					itheta = fm / p0
				} else {
					itheta = x0 + 1 + (fm - (x0+1)*p0)
				}
				var fl int
				if itheta <= x0 {
					fl = p0 * itheta
					ctx.rd.Update(uint32(fl), uint32(fl+p0), uint32(ft))
				} else {
					fl = (itheta - 1 - x0) + (x0+1)*p0
					ctx.rd.Update(uint32(fl), uint32(fl+1), uint32(ft))
				}
			}
		} else if B0 > 1 || stereo {
			if ctx.encode {
				if ctx.re != nil {
					ctx.re.EncodeUniform(uint32(itheta), uint32(qn+1))
				}
			} else if ctx.rd != nil {
				ft := uint32(qn + 1)
				if ft <= 1<<8 {
					itheta = int(ctx.rd.DecodeUniformSmall(ft))
				} else {
					itheta = int(ctx.rd.DecodeUniform(ft))
				}
			}
		} else {
			ft := ((qn >> 1) + 1) * ((qn >> 1) + 1)
			if ctx.encode {
				if ctx.re != nil {
					fs := 1
					fl := 0
					if itheta <= (qn >> 1) {
						fs = itheta + 1
						fl = itheta * (itheta + 1) >> 1
					} else {
						fs = qn + 1 - itheta
						fl = ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					}
					ctx.re.Encode(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			} else if ctx.rd != nil {
				fm := int(ctx.rd.Decode(uint32(ft)))
				if fm < ((qn >> 1) * ((qn >> 1) + 1) >> 1) {
					itheta = int((isqrt32(uint32(8*fm+1)) - 1) >> 1)
					fs := itheta + 1
					fl := itheta * (itheta + 1) >> 1
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				} else {
					itheta = int((2*(qn+1) - int(isqrt32(uint32(8*(ft-fm-1)+1)))) >> 1)
					fs := qn + 1 - itheta
					fl := ft - ((qn + 1 - itheta) * (qn + 2 - itheta) >> 1)
					ctx.rd.Update(uint32(fl), uint32(fl+fs), uint32(ft))
				}
			}
		}
		// Unquantize itheta to 14-bit range [0, 16384]
		itheta = celtUdiv(itheta*16384, qn)

		// Extended precision theta coding (ENABLE_QEXT path in libopus).
		// This applies to regular band splits as well as stereo splits.
		codedExtendedTheta := false
		if extB != nil && ((ctx.encode && ctx.extEnc != nil) || (!ctx.encode && ctx.extDec != nil)) {
			extTellFrac := 0
			if ctx.encode {
				extTellFrac = ctx.extEnc.TellFrac()
			} else {
				extTellFrac = ctx.extDec.TellFrac()
			}
			extRemainingBits := max(ctx.extTotalBits-extTellFrac, 0)
			if *extB > extRemainingBits {
				*extB = extRemainingBits
			}
			if *extB >= 2*n<<bitRes && extRemainingBits-1 > 2<<bitRes {
				extTellBefore := extTellFrac
				extraBits := min(max(celtSudiv(*extB, (2*n-1)<<bitRes), 2), 12)

				encodedVal := 0
				if ctx.encode {
					residual := ithetaQ30 - (itheta << 16)
					scaleFactor := int64(qn) * int64((1<<extraBits)-1)
					encodedVal = int((int64(residual)*scaleFactor + (1 << 29)) >> 30)
					encodedVal += (1 << (extraBits - 1)) - 1
					if encodedVal < 0 {
						encodedVal = 0
					}
					if encodedVal > (1<<extraBits)-2 {
						encodedVal = (1 << extraBits) - 2
					}
					ctx.extEnc.EncodeUniform(uint32(encodedVal), uint32((1<<extraBits)-1))
					extTellFrac = ctx.extEnc.TellFrac()
				} else {
					encodedVal = int(ctx.extDec.DecodeUniform(uint32((1 << extraBits) - 1)))
					extTellFrac = ctx.extDec.TellFrac()
				}

				encodedVal -= (1 << (extraBits - 1)) - 1
				ithetaQ30 = min(max((itheta<<16)+int((int64(encodedVal)*(1<<30))/int64(qn*((1<<extraBits)-1))), 0), 1<<30)
				*extB -= extTellFrac - extTellBefore
				codedExtendedTheta = true
			}
		}

		if !codedExtendedTheta {
			ithetaQ30 = itheta << 16
		}

		if ctx.encode && stereo {
			if itheta == 0 {
				intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
			} else {
				stereoSplit(x, y)
			}
		}
	} else if stereo {
		if ctx.encode {
			// Match libopus: for intensity stereo bands, inversion is selected
			// from the raw theta and then entropy-coded as a single flag.
			if itheta > 8192 && !ctx.disableInv {
				inv = 1
			} else {
				inv = 0
			}
			if inv != 0 && y != nil {
				for i := range y {
					y[i] = -y[i]
				}
			}
			intensityStereoWeighted(x, y, ctx.bandEnergy(0), ctx.bandEnergy(1))
		}
		if *b > 2<<bitRes && ctx.remainingBits > 2<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					if inv != 0 {
						ctx.re.EncodeBit(1, 2)
					} else {
						ctx.re.EncodeBit(0, 2)
					}
				}
			} else if ctx.rd != nil {
				inv = ctx.rd.DecodeBit(2)
			}
		} else {
			inv = 0
		}
		if ctx.disableInv {
			inv = 0
		}
		itheta = 0
		ithetaQ30 = 0
	}

	qalloc := 0
	if ctx.encode {
		if ctx.re != nil {
			qalloc = ctx.re.TellFrac() - tell
		}
	} else if ctx.rd != nil {
		qalloc = ctx.rd.TellFrac() - tell
	}
	if qalloc != 0 {
		*b -= qalloc
		sctx.qalloc = qalloc
	}

	imid := 0
	iside := 0
	delta := 0
	if itheta == 0 {
		imid = 32767
		iside = 0
		*fill &= (1 << B) - 1
		delta = -16384
	} else if itheta == 16384 {
		imid = 0
		iside = 32767
		*fill &= ((1 << B) - 1) << B
		delta = 16384
	} else {
		imid = bitexactCos(itheta)
		iside = bitexactCos(16384 - itheta)
		delta = fracMul16((n-1)<<7, bitexactLog2tanTheta(itheta))
	}

	sctx.inv = inv
	sctx.imid = imid
	sctx.iside = iside
	sctx.delta = delta
	sctx.itheta = itheta
	sctx.ithetaQ30 = ithetaQ30
	_, _ = bIn, rawItheta
}

func computeQEXTPVQRefineBits(ctx *bandCtx, extBudget, n int) int {
	if ctx == nil || n <= 1 || extBudget <= 0 {
		return 0
	}
	if ctx.encode {
		if ctx.extEnc == nil {
			return 0
		}
	} else if ctx.extDec == nil {
		return 0
	}

	extraBits := (extBudget / (n - 1)) >> bitRes
	extTellFrac := 0
	if ctx.encode {
		extTellFrac = ctx.extEnc.TellFrac()
	} else {
		extTellFrac = ctx.extDec.TellFrac()
	}
	extRemainingBits := ctx.extTotalBits - extTellFrac
	if extRemainingBits <= n<<bitRes {
		return 0
	}
	if extRemainingBits < ((extraBits+1)*(n-1)+n)<<bitRes {
		extraBits = ((extRemainingBits - (n << bitRes)) / (n - 1)) >> bitRes
		extraBits = max(extraBits-1, 0)
	}
	return min(12, extraBits)
}

func quantPartitionEncodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int, extBudget int) (int, []celtNorm) {
	if n == 1 {
		return 1, x
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	cache, hasCache := ctx.pulseCacheForBand(lm)
	maxBits := 0
	if hasCache {
		if lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		var extB *int
		if extBudget != 0 && ctx.extEnc != nil {
			extB = &extBudget
		}
		computeThetaWithExtBudget(ctx, &sctx, x[:nHalf], y, nHalf, &b, extB, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, thetaUsesQEXT(ctx))
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []celtNorm
		var lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			midGain := celtMul32(gain, opusVal16(mid))
			cm, _ = quantPartitionEncodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, midGain, fill, extBudget/2)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			sideGain := celtMul32(gain, opusVal16(side))
			scm, _ := quantPartitionEncodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, sideGain, fill>>B, extBudget/2)
			cm |= scm << (B0 >> 1)
		} else {
			sideGain := celtMul32(gain, opusVal16(side))
			cm, _ = quantPartitionEncodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, sideGain, fill>>B, extBudget/2)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			midGain := celtMul32(gain, opusVal16(mid))
			scm, _ := quantPartitionEncodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, midGain, fill, extBudget/2)
			cm |= scm
		}
		return cm, x
	}

	q := 0
	currBits := 0
	remBefore := ctx.remainingBits
	qInit := 0
	if hasCache {
		if b > 0 {
			q = bitsToPulsesCached(cache, b)
			qInit = q
			currBits = pulsesToBitsCached(cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		qInit = q
		currBits = ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	_, _, _ = remBefore, qInit, currBits
	if q != 0 {
		k := getPulses(q)
		if ctx.encode {
			pvqExtraBits := 0
			if extBudget > 0 && ctx.extEnc != nil {
				pvqExtraBits = computeQEXTPVQRefineBits(ctx, extBudget, n)
			}
			cm := algQuantScratch(ctx.re, ctx.band, x, n, k, ctx.spread, B, gain, ctx.resynth, ctx.extEnc, pvqExtraBits, ctx.encScratch)
			return cm, x
		}
		// Use scratch-aware version to avoid allocations in decode hot path
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.extDec, computeQEXTPVQRefineBits(ctx, extBudget, n), ctx.scratch)
		return cm, x
	}
	if cubicBits := computeQEXTCubicBits(ctx, extBudget, n, b, lm); cubicBits > 0 {
		if ctx.encode {
			return cubicQuant(x, n, cubicBits, B, ctx.extEnc, gain, ctx.resynth, ctx.encScratch), x
		}
		return cubicUnquant(x, n, cubicBits, B, ctx.extDec, gain, ctx.scratch), x
	}
	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0, x
		}
		if lowband == nil {
			var seedPtr *uint32
			if ctx.seedActive {
				seedPtr = &ctx.seed
			}
			if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
				if ctx.seedActive {
					for i := range x {
						ctx.seed = ctx.seed*1664525 + 1013904223
						// Match libopus: arithmetic shift on signed seed before scaling.
						x[i] = celtNorm(int32(ctx.seed) >> 20)
					}
				}
				renormalizeVector(x, gain)
			}
			return cmMask, x
		}
		var seedPtr *uint32
		if ctx.seedActive {
			seedPtr = &ctx.seed
		}
		if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					tmp := 1.0 / 256.0
					if (ctx.seed & 0x8000) == 0 {
						tmp = -tmp
					}
					if i < len(lowband) {
						x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
					} else {
						x[i] = celtNorm(tmp)
					}
				}
			}
			renormalizeVector(x, gain)
		}
		return fill, x
	}
	return fill, x
}

func quantPartitionDecodeNoExt(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int) int {
	if n == 1 {
		return 1
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	var cache []uint8
	hasCache := false
	maxBits := 0
	cacheStart := -1
	if len(ctx.cacheIndex) == 0 && len(ctx.cacheBits) == 0 {
		band := ctx.band
		if band >= 0 && band < MaxBands && lm >= -1 {
			idx := (lm+1)*MaxBands + band
			if idx >= 0 && idx < len(cacheIndex50) {
				start := int(cacheIndex50[idx])
				if start >= 0 && start < len(cacheBits50) && pulseCacheLookup50.valid[start] {
					cache = cacheBits50[start:]
					hasCache = true
					cacheStart = start
					if lm != -1 {
						maxBits = int(pulseCacheLookup50.maxBits[start])
					}
				}
			}
		}
	} else {
		cache, hasCache = ctx.pulseCacheForBand(lm)
		if hasCache && lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeThetaWithExtBudget(ctx, &sctx, x[:nHalf], y, nHalf, &b, nil, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, false)
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []celtNorm
		var lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			cm = quantPartitionDecodeNoExt(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeNoExt(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B)
			cm |= scm << (B0 >> 1)
		} else {
			cm = quantPartitionDecodeNoExt(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeNoExt(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill)
			cm |= scm
		}
		return cm
	}

	q := 0
	currBits := 0
	if hasCache {
		if b > 0 {
			if cacheStart >= 0 {
				idx := b - 1
				if idx < 0 {
					idx = 0
				} else if idx >= pulseCacheLookupBits {
					idx = pulseCacheLookupBits - 1
				}
				q = int(pulseCacheLookup50.lut[cacheStart][idx])
			} else {
				q = bitsToPulsesCached(cache, b)
			}
			if cacheStart >= 0 {
				if q > 0 {
					currBits = int(cache[q]) + 1
				} else {
					currBits = 0
				}
			} else {
				currBits = pulsesToBitsCached(cache, q)
			}
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				if cacheStart >= 0 {
					if q > 0 {
						currBits = int(cache[q]) + 1
					} else {
						currBits = 0
					}
				} else {
					currBits = pulsesToBitsCached(cache, q)
				}
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		currBits = ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	if q != 0 {
		k := getPulses(q)
		cm := algUnquantNoExtInto(x, ctx.rd, n, k, ctx.spread, B, gain, ctx.scratch)
		return cm
	}
	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0
		}
		if lowband == nil {
			var seedPtr *uint32
			if ctx.seedActive {
				seedPtr = &ctx.seed
			}
			if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
				if ctx.seedActive {
					for i := range x {
						ctx.seed = ctx.seed*1664525 + 1013904223
						x[i] = celtNorm(int32(ctx.seed) >> 20)
					}
				}
				renormalizeVector(x, gain)
			}
			return cmMask
		}
		var seedPtr *uint32
		if ctx.seedActive {
			seedPtr = &ctx.seed
		}
		if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					tmp := 1.0 / 256.0
					if (ctx.seed & 0x8000) == 0 {
						tmp = -tmp
					}
					if i < len(lowband) {
						x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
					} else {
						x[i] = celtNorm(tmp)
					}
				}
			}
			renormalizeVector(x, gain)
		}
		return fill
	}
	return fill
}

func quantPartitionDecodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, gain opusVal16, fill int, extBudget int) int {
	if n == 1 {
		return 1
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	cache, hasCache := ctx.pulseCacheForBand(lm)
	maxBits := 0
	if hasCache {
		if lm != -1 {
			maxBits = pulseCacheMaxBits(cache)
		}
	}

	if lm != -1 && b > maxBits+12 && n > 2 {
		nHalf := n >> 1
		y := x[nHalf:]
		lm--
		B0 := B
		if B == 1 {
			fill = (fill & 1) | (fill << 1)
		}
		B = (B + 1) >> 1

		sctx := splitCtx{}
		computeThetaWithExtBudget(ctx, &sctx, x[:nHalf], y, nHalf, &b, &extBudget, B, B0, lm, false, &fill)
		mid, side := thetaSplitGains(&sctx, thetaUsesQEXT(ctx))
		if B0 > 1 && (sctx.itheta&0x3fff) != 0 {
			if sctx.itheta > 8192 {
				sctx.delta -= sctx.delta >> (4 - lm)
			} else {
				sctx.delta = min(0, sctx.delta+(nHalf<<bitRes>>(5-lm)))
			}
		}
		mbits := max(0, min(b, (b-sctx.delta)/2))
		sbits := b - mbits
		ctx.remainingBits -= sctx.qalloc

		var lowband1 []celtNorm
		var lowband2 []celtNorm
		if lowband != nil && len(lowband) >= nHalf {
			lowband1 = lowband[:nHalf]
		}
		if lowband != nil && len(lowband) >= n {
			lowband2 = lowband[nHalf:]
		}

		rebalance := ctx.remainingBits
		var cm int
		if mbits >= sbits {
			cm = quantPartitionDecodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill, extBudget/2)
			rebalance = mbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 0 {
				sbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B, extBudget/2)
			cm |= scm << (B0 >> 1)
		} else {
			cm = quantPartitionDecodeWithExtBudget(ctx, y, nHalf, sbits, B, lowband2, lm, celtMul32(gain, opusVal16(side)), fill>>B, extBudget/2)
			cm <<= B0 >> 1
			rebalance = sbits - (rebalance - ctx.remainingBits)
			if rebalance > 3<<bitRes && sctx.itheta != 16384 {
				mbits += rebalance - (3 << bitRes)
			}
			scm := quantPartitionDecodeWithExtBudget(ctx, x[:nHalf], nHalf, mbits, B, lowband1, lm, celtMul32(gain, opusVal16(mid)), fill, extBudget/2)
			cm |= scm
		}
		return cm
	}

	q := 0
	currBits := 0
	remBefore := ctx.remainingBits
	qInit := 0
	if hasCache {
		if b > 0 {
			q = bitsToPulsesCached(cache, b)
			qInit = q
			currBits = pulsesToBitsCached(cache, q)
			ctx.remainingBits -= currBits
			for ctx.remainingBits < 0 && q > 0 {
				ctx.remainingBits += currBits
				q--
				currBits = pulsesToBitsCached(cache, q)
				ctx.remainingBits -= currBits
			}
		}
	} else {
		q = ctx.bitsToPulses(lm, b)
		qInit = q
		currBits = ctx.pulsesToBits(lm, q)
		ctx.remainingBits -= currBits
		for ctx.remainingBits < 0 && q > 0 {
			ctx.remainingBits += currBits
			q--
			currBits = ctx.pulsesToBits(lm, q)
			ctx.remainingBits -= currBits
		}
	}
	_, _, _ = remBefore, qInit, currBits
	if q != 0 {
		k := getPulses(q)
		pvqExtraBits := 0
		if extBudget > 0 && ctx.extDec != nil {
			pvqExtraBits = computeQEXTPVQRefineBits(ctx, extBudget, n)
		}
		cm := algUnquantInto(x, ctx.rd, ctx.band, n, k, ctx.spread, B, gain, ctx.extDec, pvqExtraBits, ctx.scratch)
		return cm
	}
	if cubicBits := computeQEXTCubicBits(ctx, extBudget, n, b, lm); cubicBits > 0 {
		return cubicUnquant(x, n, cubicBits, B, ctx.extDec, gain, ctx.scratch)
	}
	if ctx.resynth {
		cmMask := (1 << B) - 1
		fill &= cmMask
		if fill == 0 {
			clear(x)
			return 0
		}
		if lowband == nil {
			var seedPtr *uint32
			if ctx.seedActive {
				seedPtr = &ctx.seed
			}
			if !seededZeroPulseResynth(x, nil, seedPtr, gain) {
				if ctx.seedActive {
					for i := range x {
						ctx.seed = ctx.seed*1664525 + 1013904223
						x[i] = celtNorm(int32(ctx.seed) >> 20)
					}
				}
				renormalizeVector(x, gain)
			}
			return cmMask
		}
		var seedPtr *uint32
		if ctx.seedActive {
			seedPtr = &ctx.seed
		}
		if !seededZeroPulseResynth(x, lowband, seedPtr, gain) {
			if ctx.seedActive {
				for i := range x {
					ctx.seed = ctx.seed*1664525 + 1013904223
					tmp := 1.0 / 256.0
					if (ctx.seed & 0x8000) == 0 {
						tmp = -tmp
					}
					if i < len(lowband) {
						x[i] = celtNorm(float32(lowband[i]) + float32(tmp))
					} else {
						x[i] = celtNorm(tmp)
					}
				}
			}
			renormalizeVector(x, gain)
		}
		return fill
	}
	return fill
}
func quantBandN1(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	stereo := y != nil
	x0 := x
	for c := 0; c < 1+boolToInt(stereo); c++ {
		sign := 0
		if ctx.remainingBits >= 1<<bitRes {
			if ctx.encode {
				if ctx.re != nil {
					if x[0] < 0 {
						sign = 1
					}
					ctx.re.EncodeRawBits(uint32(sign), 1)
				}
			} else if ctx.rd != nil {
				sign = int(ctx.rd.DecodeRawBit())
			}
			ctx.remainingBits -= 1 << bitRes
		}
		if ctx.resynth {
			val := 1.0
			if sign != 0 {
				val = -1.0
			}
			x[0] = celtNorm(val)
		}
		if stereo {
			x = y
		}
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op,
		// so we just copy the value directly without any scaling.
		lowbandOut[0] = celtNorm(x0[0])
	}
	return 1
}

func quantBandN1Decode(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	if y == nil {
		return quantBandN1DecodeMono(ctx, x, b, lowbandOut)
	}
	return quantBandN1DecodeStereo(ctx, x, y, b, lowbandOut)
}

func quantBandN1DecodeMono(ctx *bandCtx, x []celtNorm, b int, lowbandOut []celtNorm) int {
	sign := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		val := 1.0
		if sign != 0 {
			val = -1.0
		}
		x[0] = celtNorm(val)
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = celtNorm(x[0])
	}
	return 1
}

func quantBandN1DecodeStereo(ctx *bandCtx, x, y []celtNorm, b int, lowbandOut []celtNorm) int {
	sign0 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign0 = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	sign1 := 0
	if ctx.remainingBits >= 1<<bitRes {
		if ctx.rd != nil {
			sign1 = int(ctx.rd.DecodeRawBit())
		}
		ctx.remainingBits -= 1 << bitRes
	}
	if ctx.resynth {
		val0 := 1.0
		if sign0 != 0 {
			val0 = -1.0
		}
		val1 := 1.0
		if sign1 != 0 {
			val1 = -1.0
		}
		x[0] = celtNorm(val0)
		y[0] = celtNorm(val1)
	}
	if lowbandOut != nil && len(lowbandOut) > 0 {
		// In floating-point mode, libopus's SHR32(X[0],4) is a no-op.
		lowbandOut[0] = celtNorm(x[0])
	}
	return 1
}

func copyLowbandScratch(dst, src []celtNorm, n int) []celtNorm {
	if n > 0 && len(dst) >= n && len(src) >= n {
		copy(dst[:n], src[:n])
		return dst[:n:n]
	}
	copy(dst, src)
	return dst
}

func quantBandWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, extBudget int) int {
	return quantBandPreparedLowbandWithExtBudget(ctx, x, n, b, B, lowband, lm, lowbandOut, gain, lowbandScratch, fill, false, extBudget)
}

func quantBandPreparedLowbandWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, lowbandPrepared bool, extBudget int) int {
	if n == 1 {
		return quantBandN1(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	N0 := n
	N_B := celtUdiv(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if !lowbandPrepared && lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			if ctx.encode {
				haar1(x, n>>k, 1<<k)
			}
			if lowband != nil && !lowbandPrepared {
				haar1Norm(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if ctx.encode {
			haar1(x, N_B, B)
		}
		if lowband != nil && !lowbandPrepared {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		if ctx.encScratch != nil {
			x = ctx.encScratch.ensureQuantWork(n)
			deinterleaveHadamardInto(x, xOrig, N_B>>recombine, B0<<recombine, longBlocks)
		} else {
			deinterleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
		if lowband != nil && !lowbandPrepared {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}
	cm := 0
	if ctx.extraBands && b > cubicQEXTThresholdQ3(ctx, n, lm) {
		cm = cubicQuantPartition(ctx, x, n, b, B, lm, gain)
	} else {
		cm, _ = quantPartitionEncodeWithExtBudget(ctx, x, n, b, B, lowband, lm, gain, fill, extBudget)
	}

	if ctx.resynth {
		if B0 > 1 {
			if ctx.encScratch != nil {
				interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
				x = xOrig
			} else {
				interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func prepareQuantBandLowband(dst, src []celtNorm, n, B, tfChange int, scratch *bandEncodeScratch) []celtNorm {
	if src == nil || len(src) < n || len(dst) < n {
		return nil
	}
	dst = dst[:n]
	copy(dst, src[:n])

	N_B := celtUdiv(n, B)
	recombine := max(tfChange, 0)
	if recombine != 0 {
		for k := range recombine {
			haar1Norm(dst, n>>k, 1<<k)
		}
	}
	longBlocks := B == 1
	B >>= recombine
	N_B <<= recombine
	for (N_B&1) == 0 && tfChange < 0 {
		haar1Norm(dst, N_B, B)
		B <<= 1
		N_B >>= 1
		tfChange++
	}
	if B > 1 {
		deinterleaveHadamardScratchBufNorm(dst, N_B>>recombine, B<<recombine, longBlocks, nil, scratch)
	}
	return dst
}

func quantBandDecode(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int) int {
	if ctx.extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		return quantBandDecodeNoExtFast(ctx, x, n, b, B, lowband, lm, lowbandOut, gain, lowbandScratch, fill)
	}
	return quantBandDecodeWithExtBudget(ctx, x, n, b, B, lowband, lm, lowbandOut, gain, lowbandScratch, fill, ctx.extBudget)
}

func quantBandDecodeNoExtFast(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	N0 := n
	N_B := celtUdiv(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			if lowband != nil {
				haar1Norm(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if lowband != nil {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		if ctx.scratch != nil {
			x = ctx.scratch.ensureQuantWork(n)
			deinterleaveHadamardInto(x, xOrig, N_B>>recombine, B0<<recombine, longBlocks)
		} else {
			deinterleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
		if lowband != nil {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}

	cm := quantPartitionDecodeNoExt(ctx, x, n, b, B, lowband, lm, gain, fill)

	if ctx.resynth {
		if B0 > 1 {
			if ctx.scratch != nil {
				interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
				x = xOrig
			} else {
				interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func quantBandDecodeWithExtBudget(ctx *bandCtx, x []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, gain opusVal16, lowbandScratch []celtNorm, fill int, extBudget int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, nil, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
	}

	N0 := n
	N_B := celtUdiv(n, B)
	longBlocks := B == 1

	recombine := 0
	tfChange := ctx.tfChange
	if tfChange > 0 {
		recombine = tfChange
	}

	if lowbandScratch != nil && lowband != nil && (recombine != 0 || ((N_B&1) == 0 && tfChange < 0) || B > 1) {
		lowband = copyLowbandScratch(lowbandScratch, lowband, n)
	}

	if recombine != 0 {
		for k := 0; k < recombine; k++ {
			if lowband != nil {
				haar1Norm(lowband, n>>k, 1<<k)
			}
			fill = bitInterleaveTable[fill&0xF] | (bitInterleaveTable[fill>>4] << 2)
		}
	}
	B >>= recombine
	N_B <<= recombine

	timeDivide := 0
	for (N_B&1) == 0 && tfChange < 0 {
		if lowband != nil {
			haar1Norm(lowband, N_B, B)
		}
		fill |= fill << B
		B <<= 1
		N_B >>= 1
		timeDivide++
		tfChange++
	}
	B0 := B
	N_B0 := N_B
	xOrig := x

	if B0 > 1 {
		if ctx.scratch != nil {
			x = ctx.scratch.ensureQuantWork(n)
			deinterleaveHadamardInto(x, xOrig, N_B>>recombine, B0<<recombine, longBlocks)
		} else {
			deinterleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
		if lowband != nil {
			deinterleaveHadamardScratchBufNorm(lowband, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
		}
	}

	cm := 0
	if ctx.extraBands && b > cubicQEXTThresholdQ3(ctx, n, lm) {
		cm = cubicQuantPartition(ctx, x, n, b, B, lm, gain)
	} else if extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		cm = quantPartitionDecodeNoExt(ctx, x, n, b, B, lowband, lm, gain, fill)
	} else {
		cm = quantPartitionDecodeWithExtBudget(ctx, x, n, b, B, lowband, lm, gain, fill, extBudget)
	}

	if ctx.resynth {
		if B0 > 1 {
			if ctx.scratch != nil {
				interleaveHadamardInto(xOrig, x, N_B>>recombine, B0<<recombine, longBlocks)
				x = xOrig
			} else {
				interleaveHadamardScratchBuf(x, N_B>>recombine, B0<<recombine, longBlocks, ctx.scratch, ctx.encScratch)
			}
		}
		N_B = N_B0
		B = B0
		for k := 0; k < timeDivide; k++ {
			B >>= 1
			N_B <<= 1
			cm |= cm >> B
			haar1(x, N_B, B)
		}
		for k := 0; k < recombine; k++ {
			cm = bitDeinterleaveTable[cm&0xF]
			haar1(x, N0>>k, 1<<k)
		}
		B <<= recombine

		if lowbandOut != nil && len(lowbandOut) >= N0 {
			scaleLowbandOutForFoldingNorm(lowbandOut, x, N0)
		}
		cm &= (1 << B) - 1
	}
	return cm
}

func quantBandStereo(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int) int {
	return quantBandStereoWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
}

func quantBandStereoWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, extBudget int) int {
	return quantBandStereoPreparedLowbandWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, false, extBudget)
}

func quantBandStereoPreparedLowband(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, lowbandPrepared bool) int {
	return quantBandStereoPreparedLowbandWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, lowbandPrepared, ctx.extBudget)
}

func quantBandStereoPreparedLowbandWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, lowbandPrepared bool, extBudget int) int {
	if n == 1 {
		return quantBandN1(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	if ctx.encode && ctx.bandE != nil && ctx.channels == 2 {
		l := ctx.bandEnergy(0)
		r := ctx.bandEnergy(1)
		if l < 1e-10 || r < 1e-10 {
			if l > r {
				copy(y, x)
			} else {
				copy(x, y)
			}
		}
	}

	sctx := splitCtx{}
	var extB *int
	if extBudget != 0 && (ctx.extEnc != nil || ctx.extDec != nil) {
		extB = &extBudget
	}
	computeThetaWithExtBudget(ctx, &sctx, x, y, n, &b, extB, B, B, lm, true, &fill)
	mid, side := thetaSplitGains(&sctx, thetaUsesQEXT(ctx))

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 {
			if ctx.encode {
				if ctx.re != nil {
					bit := 0
					if float32(x2[0])*float32(y2[1])-float32(x2[1])*float32(y2[0]) < 0 {
						bit = 1
					}
					ctx.re.EncodeRawBits(uint32(bit), 1)
					if bit != 0 {
						sign = -1
					}
				}
			} else if ctx.rd != nil {
				if ctx.rd.DecodeRawBit() == 1 {
					sign = -1
				}
			}
		}
		cm := quantBandPreparedLowbandWithExtBudget(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill, lowbandPrepared, extBudget)
		sign32 := float32(sign)
		y2[0] = celtNorm(-sign32 * float32(x2[1]))
		y2[1] = celtNorm(sign32 * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			// Apply inv negation (same as common resynth path)
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, mbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandPreparedLowbandWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, lowbandPrepared, extBudget/2+qextExtra)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			sbits = min(sbits, ctx.remainingBits)
		}
		cm |= quantBandWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2-qextExtra)
	} else {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, sbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2+qextExtra)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			mbits = min(mbits, ctx.remainingBits)
		}
		cm |= quantBandPreparedLowbandWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, lowbandPrepared, extBudget/2-qextExtra)
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, opusVal16(mid))
		}
		if sctx.inv != 0 {
			for i := range n {
				y[i] = -y[i]
			}
		}
	}
	return cm
}

func quantBandStereoDecode(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int) int {
	if ctx.extBudget == 0 && ctx.extDec == nil && !ctx.extraBands {
		return quantBandStereoDecodeNoExtFast(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill)
	}
	return quantBandStereoDecodeWithExtBudget(ctx, x, y, n, b, B, lowband, lm, lowbandOut, lowbandScratch, fill, ctx.extBudget)
}

func quantBandStereoDecodeNoExtFast(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	sctx := splitCtx{}
	computeThetaWithExtBudget(ctx, &sctx, x, y, n, &b, nil, B, B, lm, true, &fill)
	mid, side := thetaSplitGains(&sctx, false)

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 && ctx.rd != nil {
			if ctx.rd.DecodeRawBit() == 1 {
				sign = -1
			}
		}
		cm := quantBandDecodeNoExtFast(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill)
		sign32 := float32(sign)
		y2[0] = celtNorm(-sign32 * float32(x2[1]))
		y2[1] = celtNorm(sign32 * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		cm = quantBandDecodeNoExtFast(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandDecodeNoExtFast(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B)
	} else {
		cm = quantBandDecodeNoExtFast(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		cm |= quantBandDecodeNoExtFast(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill)
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, opusVal16(mid))
		}
		if sctx.inv != 0 {
			for i := range n {
				y[i] = -y[i]
			}
		}
	}
	return cm
}

func quantBandStereoDecodeWithExtBudget(ctx *bandCtx, x, y []celtNorm, n, b, B int, lowband []celtNorm, lm int, lowbandOut []celtNorm, lowbandScratch []celtNorm, fill int, extBudget int) int {
	if n == 1 {
		return quantBandN1Decode(ctx, x, y, b, lowbandOut)
	}
	if n > 0 {
		x = x[:n:n]
		_ = x[n-1]
		if y != nil {
			y = y[:n:n]
			_ = y[n-1]
		}
	}

	origFill := fill

	sctx := splitCtx{}
	computeThetaWithExtBudget(ctx, &sctx, x, y, n, &b, &extBudget, B, B, lm, true, &fill)
	mid, side := thetaSplitGains(&sctx, thetaUsesQEXT(ctx))

	if n == 2 {
		mbits := b
		sbits := 0
		if sctx.itheta != 0 && sctx.itheta != 16384 {
			sbits = 1 << bitRes
		}
		mbits -= sbits
		c := sctx.itheta > 8192
		ctx.remainingBits -= sctx.qalloc + sbits

		x2 := x
		y2 := y
		if c {
			x2 = y
			y2 = x
		}
		sign := 1
		if sbits > 0 {
			if ctx.rd != nil {
				if ctx.rd.DecodeRawBit() == 1 {
					sign = -1
				}
			}
		}
		cm := quantBandDecodeWithExtBudget(ctx, x2, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, origFill, extBudget)
		sign32 := float32(sign)
		y2[0] = celtNorm(-sign32 * float32(x2[1]))
		y2[1] = celtNorm(sign32 * float32(x2[0]))
		if ctx.resynth {
			x[0] = celtNorm(float32(mid) * float32(x[0]))
			x[1] = celtNorm(float32(mid) * float32(x[1]))
			y[0] = celtNorm(float32(side) * float32(y[0]))
			y[1] = celtNorm(float32(side) * float32(y[1]))
			tmp := float32(x[0])
			y0 := float32(y[0])
			x[0] = celtNorm(noFMA32Sub(tmp, y0))
			y[0] = celtNorm(noFMA32Add(tmp, y0))
			tmp = float32(x[1])
			y1 := float32(y[1])
			x[1] = celtNorm(noFMA32Sub(tmp, y1))
			y[1] = celtNorm(noFMA32Add(tmp, y1))
			if sctx.inv != 0 {
				y[0] = -y[0]
				y[1] = -y[1]
			}
		}
		return cm
	}

	delta := sctx.delta
	mbits := max(0, min(b, (b-delta)/2))
	sbits := b - mbits
	ctx.remainingBits -= sctx.qalloc

	rebalance := ctx.remainingBits
	cm := 0
	if mbits >= sbits {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, mbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandDecodeWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, extBudget/2+qextExtra)
		rebalance = mbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 0 {
			sbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			sbits = min(sbits, ctx.remainingBits)
		}
		cm |= quantBandDecodeWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2-qextExtra)
	} else {
		qextExtra := 0
		if ctx.bandCaps != nil && extBudget != 0 && ctx.band >= 0 && ctx.band < len(ctx.bandCaps) {
			qextExtra = max(0, min(extBudget/2, sbits-int(ctx.bandCaps[ctx.band])/2))
		}
		cm = quantBandDecodeWithExtBudget(ctx, y, n, sbits, B, nil, lm, nil, opusVal16(side), nil, fill>>B, extBudget/2+qextExtra)
		rebalance = sbits - (rebalance - ctx.remainingBits)
		if rebalance > 3<<bitRes && sctx.itheta != 16384 {
			mbits += rebalance - (3 << bitRes)
		}
		if ctx.extraBands {
			mbits = min(mbits, ctx.remainingBits)
		}
		cm |= quantBandDecodeWithExtBudget(ctx, x, n, mbits, B, lowband, lm, lowbandOut, 1.0, lowbandScratch, fill, extBudget/2-qextExtra)
	}

	if ctx.resynth {
		if n != 2 {
			stereoMerge(x, y, opusVal16(mid))
		}
		if sctx.inv != 0 {
			for i := range n {
				y[i] = -y[i]
			}
		}
	}
	return cm
}

func quantAllBandsDecodeWithScratch(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int32, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch, extDec *rangecoding.Decoder, extraBits []int32, extTotalBits int) (left, right []celtNorm, collapse []byte) {
	return quantAllBandsDecodeWithScratchWithMode(rd, channels, frameSize, lm, start, end,
		pulses, shortBlocks, spread, dualStereo, intensity, tfRes, totalBitsQ3, balance,
		codedBands, disableInv, seed, scratch, extDec, extraBits, extTotalBits,
		nil, nil, nil, nil)
}

func clearDecodedBandEdges(buf []celtNorm, frameSize, start, end int) {
	if start < 0 {
		start = 0
	}
	if start > frameSize {
		start = frameSize
	}
	if end < start {
		end = start
	}
	if end > frameSize {
		end = frameSize
	}
	clear(buf[:start])
	clear(buf[end:frameSize])
}

func quantAllBandsDecodeWithScratchWithMode(rd *rangecoding.Decoder, channels, frameSize, lm int, start, end int,
	pulses []int32, shortBlocks int, spread int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32,
	scratch *bandDecodeScratch, extDec *rangecoding.Decoder, extraBits []int32, extTotalBits int,
	bandEdges []int, bandLogN []int, cacheIndex []int16, cacheBits []uint8) (left, right []celtNorm, collapse []byte) {
	M := 1 << lm
	B := max(shortBlocks, 1)
	edges := bandEdges
	if len(edges) < 2 {
		edges = EBands[:]
	}
	maxBands := len(edges) - 1
	if maxBands <= 0 {
		return nil, nil, nil
	}
	if start < 0 {
		start = 0
	}
	if end > maxBands {
		end = maxBands
	}
	if end <= start {
		return nil, nil, nil
	}
	N := frameSize
	normOffset := M * edges[start]
	normLen := max(M*edges[maxBands-1]-normOffset, 0)
	maxBand := M * (edges[end] - edges[end-1])
	if scratch != nil {
		// Back the band-decode-local float scratch with one contiguous arena
		// before the inline/getter sizing below reslices within each slot.
		scratch.ensureFloatScratch(channels, N, normLen, maxBand)
	}
	if scratch == nil {
		left = make([]celtNorm, N)
		if channels == 2 {
			right = make([]celtNorm, N)
		}
		collapse = make([]byte, channels*maxBands)
	} else {
		left = ensureNormSlice(&scratch.left, N)
		clearDecodedBandEdges(left, N, M*edges[start], M*edges[end])
		if channels == 2 {
			right = ensureNormSlice(&scratch.right, N)
			clearDecodedBandEdges(right, N, M*edges[start], M*edges[end])
		} else if cap(scratch.right) > 0 {
			scratch.right = scratch.right[:0]
			right = nil
		}
		collapse = ensureByteSlice(&scratch.collapse, channels*maxBands)
		for i := range collapse {
			collapse[i] = 0
		}
	}

	var norm []celtNorm
	if scratch == nil {
		norm = make([]celtNorm, channels*normLen)
	} else {
		norm = ensureNormSliceNoClear(&scratch.norm, channels*normLen)
	}
	var norm2 []celtNorm
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	var lowbandScratch []celtNorm
	if scratch == nil {
		lowbandScratch = make([]celtNorm, maxBand)
	} else {
		lowbandScratch = ensureNormSliceNoClear(&scratch.lowband, maxBand)
	}

	lowbandOffset := 0
	updateLowband := true
	extraBands := extDec != nil && extraBits != nil && start == 0 && len(edges) >= 2 && edges[0] > 0 && (end == nbQEXTBands || end == 2)
	var bandCaps [MaxBands]int32
	bandCapsSlice := []int32(nil)
	if channels == 2 && extDec != nil && !extraBands {
		initCapsInto(bandCaps[:end], end, lm, channels)
		bandCapsSlice = bandCaps[:end]
	}
	ctx := bandCtx{
		rd:              rd,
		encode:          false,
		spread:          spread,
		remainingBits:   0,
		intensity:       intensity,
		resynth:         true,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		scratch:         scratch,
		extTotalBits:    extTotalBits,
		extraBands:      extraBands,
		bandEdges:       edges,
		bandLogN:        bandLogN,
		cacheIndex:      cacheIndex,
		cacheBits:       cacheBits,
		bandCaps:        bandCapsSlice,
	}
	if seed != nil {
		ctx.seed = *seed
		ctx.seedActive = true
	}
	extBalance := 0
	extTell := 0

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.extBudget = 0
		if extDec != nil && extraBits != nil {
			ctx.extDec = extDec
			if i != start && i-1 < len(extraBits) {
				extBalance += int(extraBits[i-1]) + extTell
			}
			extTell = extDec.TellFrac()
			if i != start {
				extBalance -= extTell
			}
			if i <= codedBands-1 && i < len(extraBits) {
				extCurrBalance := celtSudiv(extBalance, min(3, codedBands-i))
				extRemaining := ctx.extTotalBits - extTell
				ctx.extBudget = max(0, min(16383, min(extRemaining, int(extraBits[i])+extCurrBalance)))
			}
		}
		last := i == end-1
		bandStart := edges[i] * M
		bandEnd := edges[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		x := left[bandStart:bandEnd]
		var y []celtNorm
		if channels == 2 {
			y = right[bandStart:bandEnd]
		}

		tell := rd.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudiv(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		if ctx.resynth && (M*edges[i]-nBand >= M*edges[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0)
		}

		ctx.tfChange = int(tfRes[i])

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*edges[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*edges[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*edges[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := min(max(M*edges[i]-normOffset, 0), len(norm))
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = celtNorm(float32(0.5) * (float32(norm[j]) + float32(norm2[j])))
				}
			}
		}

		var lowbandX []celtNorm
		var lowbandY []celtNorm
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}
		if effectiveLowband >= 0 && lowbandX != nil {
		}

		var lowbandOutX []celtNorm
		var lowbandOutY []celtNorm
		outStart := M*edges[i] - normOffset
		if !last && outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 && outStart+nBand <= len(norm2) {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if dualStereo != 0 {
			xCM = quantBandDecode(&ctx, x, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM)
			if channels == 2 {
				yCM = quantBandDecode(&ctx, y, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM)
			}
		} else {
			if channels == 2 {
				xCM = quantBandStereoDecode(&ctx, x, y, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM)
				yCM = xCM
			} else {
				xCM = quantBandDecode(&ctx, x, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM)
				yCM = xCM
			}
		}

		collapse[i*channels] = byte(xCM)
		if channels == 2 {
			collapse[i*channels+channels-1] = byte(yCM)
		}
		balance += int(pulses[i]) + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}
	if seed != nil {
		*seed = ctx.seed
	}

	return left, right, collapse
}

// quantAllBandsEncode encodes all frequency bands using PVQ quantization.
// Parameters:
//   - re: range encoder for the main bitstream
//   - channels: number of audio channels (1 or 2)
//   - frameSize: frame size in samples
//   - lm: log2 of M (time resolution multiplier)
//   - start, end: band range to encode
//   - x, y: normalized MDCT coefficients for left/right channels
//   - pulses: bit allocation per band
//   - shortBlocks: number of short blocks (1 for long block)
//   - spread: spreading parameter (0-3)
//   - tapset: window taper selection for comb filter (0-2), tracked for state consistency
//   - dualStereo: whether to use dual stereo mode
//   - intensity: intensity stereo band threshold
//   - tfRes: time-frequency resolution per band
//   - totalBitsQ3: total bit budget in Q3 format
//   - balance: bit balance for allocation
//   - codedBands: number of bands that will be coded
//   - seed: RNG seed for noise filling
//   - complexity: encoder complexity (0-10)
//   - bandE: band energies for stereo decisions
//   - extEnc: extended encoder for high-precision mode (can be nil)
//   - extraBits: per-band QEXT budgets in Q3 bits (can be nil)
//
// Reference: libopus celt/bands.c quant_all_bands()
// quantAllBandsEncodeScratch is the scratch-aware version of quantAllBandsEncode.
func quantAllBandsEncodeScratch(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	x, y []celtNorm, pulses []int32, shortBlocks int, spread int, tapset int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32, complexity int,
	bandE []celtEner, extEnc *rangecoding.Encoder, extraBits []int32, scratch *bandEncodeScratch) (collapse []byte) {
	return quantAllBandsEncodeScratchWithMode(re, channels, frameSize, lm, start, end,
		x, y, pulses, shortBlocks, spread, tapset, dualStereo, intensity,
		tfRes, totalBitsQ3, balance, codedBands, disableInv, seed, complexity,
		bandE, extEnc, extraBits, scratch, nil, nil, nil, nil)
}

func quantAllBandsEncodeScratchWithMode(re *rangecoding.Encoder, channels, frameSize, lm int, start, end int,
	x, y []celtNorm, pulses []int32, shortBlocks int, spread int, tapset int, dualStereo, intensity int,
	tfRes []int32, totalBitsQ3 int, balance int, codedBands int, disableInv bool, seed *uint32, complexity int,
	bandE []celtEner, extEnc *rangecoding.Encoder, extraBits []int32, scratch *bandEncodeScratch,
	bandEdges []int, bandLogN []int, cacheIndex []int16, cacheBits []uint8) (collapse []byte) {
	if re == nil {
		return nil
	}
	if channels < 1 {
		channels = 1
	}
	if channels > 2 {
		channels = 2
	}
	if len(x) < frameSize {
		return nil
	}

	edges := bandEdges
	if len(edges) < 2 {
		edges = EBands[:]
	}
	maxBands := len(edges) - 1
	if maxBands <= 0 {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if end > maxBands {
		end = maxBands
	}
	if end <= start {
		return nil
	}

	M := 1 << lm
	B := max(shortBlocks, 1)

	// Use scratch buffers if available
	if scratch != nil {
		collapse = scratch.ensureCollapse(channels * maxBands)
		clear(collapse)
	} else {
		collapse = make([]byte, channels*maxBands)
	}

	normOffset := M * edges[start]
	normLen := max(M*edges[maxBands-1]-normOffset, 0)

	var norm []celtNorm
	if scratch != nil {
		// ensureNorm already zero-fills (via ensureNormSlice clear).
		norm = scratch.ensureNorm(channels * normLen)
	} else {
		norm = make([]celtNorm, channels*normLen)
	}
	var norm2 []celtNorm
	if channels == 2 {
		norm2 = norm[normLen:]
	}

	maxBand := max(M*(edges[end]-edges[end-1]), 0)

	var lowbandScratch []celtNorm
	if scratch != nil {
		lowbandScratch = scratch.ensureLowbandScratch(maxBand)
	} else {
		lowbandScratch = make([]celtNorm, maxBand)
	}

	lowbandOffset := 0
	updateLowband := true
	extraBands := extEnc != nil && extraBits != nil && start == 0 && len(edges) >= 2 && edges[0] > 0 && (end == nbQEXTBands || end == 2)
	thetaRDOEnabled := channels == 2 && dualStereo == 0 && complexity >= 8 && !extraBands
	var bandCaps [MaxBands]int32
	bandCapsSlice := []int32(nil)
	if channels == 2 && extEnc != nil && !extraBands {
		initCapsInto(bandCaps[:end], end, lm, channels)
		bandCapsSlice = bandCaps[:end]
	}
	ctx := bandCtx{
		re:            re,
		encode:        true,
		extEnc:        nil,
		extBudget:     0,
		extTotalBits:  0,
		extraBands:    extraBands,
		bandE:         bandE,
		nbBands:       end,
		channels:      channels,
		spread:        spread,
		remainingBits: 0,
		intensity:     intensity,
		bandEdges:     edges,
		bandLogN:      bandLogN,
		cacheIndex:    cacheIndex,
		cacheBits:     cacheBits,
		bandCaps:      bandCapsSlice,
		// Match libopus encode-side default: resynth only when theta RDO is active.
		// (decode path remains resynth=true).
		resynth:         thetaRDOEnabled,
		disableInv:      disableInv,
		avoidSplitNoise: B > 1,
		tapset:          tapset,
		encScratch:      scratch,
	}
	if seed != nil {
		ctx.seed = *seed
		ctx.seedActive = true
	}
	if ctx.channels > 0 && ctx.bandE != nil {
		ctx.nbBands = len(ctx.bandE) / ctx.channels
	}
	if extEnc != nil {
		ctx.extTotalBits = extEnc.StorageBits() << bitRes
	}
	extBalance := 0
	extTell := 0

	for i := start; i < end; i++ {
		ctx.band = i
		ctx.extBudget = 0
		if extEnc != nil && extraBits != nil {
			ctx.extEnc = extEnc
			if i != start && i-1 < len(extraBits) {
				extBalance += int(extraBits[i-1]) + extTell
			}
			extTell = extEnc.TellFrac()
			if i != start {
				extBalance -= extTell
			}
			if i <= codedBands-1 && i < len(extraBits) {
				extCurrBalance := celtSudiv(extBalance, min(3, codedBands-i))
				extRemaining := ctx.extTotalBits - extTell
				ctx.extBudget = max(0, min(16383, min(extRemaining, int(extraBits[i])+extCurrBalance)))
			}
		}
		last := i == end-1
		bandStart := edges[i] * M
		bandEnd := edges[i+1] * M
		nBand := bandEnd - bandStart
		if nBand <= 0 {
			continue
		}

		xBand := x[bandStart:bandEnd]
		var yBand []celtNorm
		if channels == 2 && len(y) >= bandEnd {
			yBand = y[bandStart:bandEnd]
		}

		tell := re.TellFrac()
		if i != start {
			balance -= tell
		}
		remaining := totalBitsQ3 - tell - 1
		ctx.remainingBits = remaining

		b := 0
		currBalance := 0
		if i <= codedBands-1 {
			currBalance = celtSudiv(balance, min(3, codedBands-i))
			b = max(0, min(16383, min(remaining+1, int(pulses[i])+currBalance)))
		}
		if ctx.resynth && (M*edges[i]-nBand >= M*edges[start] || i == start+1) && (updateLowband || lowbandOffset == 0) {
			lowbandOffset = i
		}
		if i == start+1 {
			specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0)
		}

		ctx.tfChange = 0
		if tfRes != nil && i < len(tfRes) {
			ctx.tfChange = int(tfRes[i])
		}

		effectiveLowband := -1
		xCM := 0
		yCM := 0
		if lowbandOffset != 0 && (spread != spreadAggressive || B > 1 || ctx.tfChange < 0) {
			effectiveLowband = max(0, M*edges[lowbandOffset]-normOffset-nBand)
			foldStart := lowbandOffset
			for {
				foldStart--
				if foldStart <= start {
					foldStart = start
					break
				}
				if M*edges[foldStart] <= effectiveLowband+normOffset {
					break
				}
			}
			foldEnd := lowbandOffset - 1
			for {
				foldEnd++
				if foldEnd >= i {
					break
				}
				if M*edges[foldEnd] >= effectiveLowband+normOffset+nBand {
					break
				}
			}
			for fold := foldStart; fold < foldEnd; fold++ {
				xCM |= int(collapse[fold*channels])
				if channels == 2 {
					yCM |= int(collapse[fold*channels+channels-1])
				}
			}
		} else {
			xCM = (1 << B) - 1
			yCM = xCM
		}

		if dualStereo != 0 && i == intensity {
			dualStereo = 0
			if ctx.resynth {
				mergeLimit := min(max(M*edges[i]-normOffset, 0), len(norm))
				if channels == 2 && mergeLimit > len(norm2) {
					mergeLimit = len(norm2)
				}
				for j := 0; j < mergeLimit; j++ {
					norm[j] = celtNorm(float32(0.5) * (float32(norm[j]) + float32(norm2[j])))
				}
			}
		}

		var lowbandX []celtNorm
		var lowbandY []celtNorm
		if effectiveLowband >= 0 && effectiveLowband+nBand <= len(norm) {
			lowbandX = norm[effectiveLowband : effectiveLowband+nBand]
			if channels == 2 && effectiveLowband+nBand <= len(norm2) {
				lowbandY = norm2[effectiveLowband : effectiveLowband+nBand]
			}
		}

		var lowbandOutX []celtNorm
		var lowbandOutY []celtNorm
		outStart := M*edges[i] - normOffset
		if !last && outStart >= 0 && outStart+nBand <= len(norm) {
			lowbandOutX = norm[outStart : outStart+nBand]
			if channels == 2 && outStart+nBand <= len(norm2) {
				lowbandOutY = norm2[outStart : outStart+nBand]
			}
		}

		if dualStereo != 0 {
			xCM = quantBandWithExtBudget(&ctx, xBand, nBand, b/2, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM, ctx.extBudget/2)
			if channels == 2 && yBand != nil {
				yCM = quantBandWithExtBudget(&ctx, yBand, nBand, b/2, B, lowbandY, lm, lowbandOutY, 1.0, lowbandScratch, yCM, ctx.extBudget/2)
			}
		} else {
			if channels == 2 && yBand != nil {
				// Theta RDO: Try both rounding directions and pick the one with lower distortion.
				// Enabled only for high complexity stereo (match libopus theta_rdo).
				// Reference: libopus bands.c quant_all_bands(), theta_rdo logic
				thetaRDO := thetaRDOEnabled && i < intensity
				if thetaRDO {
					// Compute channel weights for distortion measurement
					var leftE, rightE celtEner
					if bandE != nil && len(bandE) > ctx.nbBands+i {
						leftE = bandE[i]
						rightE = bandE[ctx.nbBands+i]
					}
					w0, w1 := computeChannelWeights(leftE, rightE)

					// Save original input data - use scratch if available
					var xSave, ySave []celtNorm
					if scratch != nil {
						xSave = scratch.ensureXSave(nBand)
						ySave = scratch.ensureYSave(nBand)
					} else {
						xSave = make([]celtNorm, nBand)
						ySave = make([]celtNorm, nBand)
					}
					copy(xSave, xBand)
					copy(ySave, yBand)
					var xTrial, yTrial []celtNorm
					if scratch != nil {
						xTrial = scratch.ensureThetaX(nBand)
						yTrial = scratch.ensureThetaY(nBand)
					} else {
						xTrial = make([]celtNorm, nBand)
						yTrial = make([]celtNorm, nBand)
					}

					// Save norm data if not last band
					var normSave []celtNorm
					if lowbandOutX != nil {
						if scratch != nil {
							normSave = scratch.ensureNormSave(nBand)
						} else {
							normSave = make([]celtNorm, nBand)
						}
						copy(normSave, lowbandOutX)
					}

					// Save encoder state - use scratch if available
					var ecSave *rangecoding.EncoderState
					if scratch != nil {
						re.SaveStateInto(&scratch.ecSave)
						ecSave = &scratch.ecSave
					} else {
						ecSave = re.SaveState()
					}
					var extECSave *rangecoding.EncoderState
					if ctx.extEnc != nil {
						if scratch != nil {
							ctx.extEnc.SaveStateInto(&scratch.extEcSave)
							extECSave = &scratch.extEcSave
						} else {
							extECSave = ctx.extEnc.SaveState()
						}
					}
					ctxSave := ctx

					// Try encoding with theta_round = -1 (bias toward 0/16384)
					ctx.thetaRound = -1
					cm := xCM | yCM
					xCM0 := quantBandStereoWithExtBudget(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, cm, ctx.extBudget)

					// Compute distortion for first trial
					copy(xTrial, xBand)
					copy(yTrial, yBand)
					dist0 := thetaRDODistortion(w0, w1, xSave, xTrial, ySave, yTrial)

					var ecSave0 *rangecoding.EncoderState
					if scratch != nil {
						re.SaveStateInto(&scratch.ecSave0)
						ecSave0 = &scratch.ecSave0
					} else {
						ecSave0 = re.SaveState()
					}
					var extECSave0 *rangecoding.EncoderState
					if ctx.extEnc != nil {
						if scratch != nil {
							ctx.extEnc.SaveStateInto(&scratch.extEcSave0)
							extECSave0 = &scratch.extEcSave0
						} else {
							extECSave0 = ctx.extEnc.SaveState()
						}
					}
					ctxSave0 := ctx
					cm0 := xCM0

					// Save first-trial result so we can restore it if it wins.
					var xSave0, ySave0 []celtNorm
					if scratch != nil {
						xSave0 = scratch.ensureXResult0(nBand)
						ySave0 = scratch.ensureYResult0(nBand)
					} else {
						xSave0 = make([]celtNorm, nBand)
						ySave0 = make([]celtNorm, nBand)
					}
					copy(xSave0, xBand)
					copy(ySave0, yBand)
					var normSave0 []celtNorm
					if lowbandOutX != nil {
						if scratch != nil {
							normSave0 = scratch.ensureNormResult0(nBand)
						} else {
							normSave0 = make([]celtNorm, nBand)
						}
						copy(normSave0, lowbandOutX)
					}

					// Restore coder and band state for the second trial.
					re.RestoreStateShallow(ecSave)
					if ctx.extEnc != nil && extECSave != nil {
						ctx.extEnc.RestoreStateShallow(extECSave)
					}
					ctx = ctxSave
					copy(xBand, xSave)
					copy(yBand, ySave)
					if i == start+1 {
						specialHybridFoldingWithEdges(norm, norm2, edges, start, M, dualStereo != 0)
					}
					if lowbandOutX != nil && normSave != nil {
						copy(lowbandOutX, normSave)
					}

					// Try encoding with theta_round = +1 (bias toward equal split)
					ctx.thetaRound = 1
					xCM1 := quantBandStereoWithExtBudget(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, cm, ctx.extBudget)

					// Compute distortion for second trial
					copy(xTrial, xBand)
					copy(yTrial, yBand)
					dist1 := thetaRDODistortion(w0, w1, xSave, xTrial, ySave, yTrial)

					// Pick the trial with lower distortion (higher inner product = lower distortion)
					if dist0 >= dist1 {
						// First trial (theta_round = -1) was better
						xCM = cm0
						re.RestoreState(ecSave0)
						if ctx.extEnc != nil && extECSave0 != nil {
							ctx.extEnc.RestoreState(extECSave0)
						}
						ctx = ctxSave0
						copy(xBand, xSave0)
						copy(yBand, ySave0)
						if lowbandOutX != nil && normSave0 != nil {
							copy(lowbandOutX, normSave0)
						}
					} else {
						// Second trial (theta_round = +1) was better
						xCM = xCM1
					}
					yCM = xCM
					ctx.thetaRound = 0 // Reset for subsequent bands
				} else {
					// No theta RDO: use standard encoding
					ctx.thetaRound = 0
					xCM = quantBandStereoWithExtBudget(&ctx, xBand, yBand, nBand, b, B, lowbandX, lm, lowbandOutX, lowbandScratch, xCM|yCM, ctx.extBudget)
					yCM = xCM
				}
			} else {
				xCM = quantBandWithExtBudget(&ctx, xBand, nBand, b, B, lowbandX, lm, lowbandOutX, 1.0, lowbandScratch, xCM|yCM, ctx.extBudget)
				yCM = xCM
			}
		}

		collapse[i*channels] = byte(xCM)
		if channels == 2 {
			collapse[i*channels+channels-1] = byte(yCM)
		}
		balance += int(pulses[i]) + tell

		updateLowband = b > (nBand << bitRes)
		ctx.avoidSplitNoise = false
	}
	if seed != nil {
		*seed = ctx.seed
	}

	return collapse
}
