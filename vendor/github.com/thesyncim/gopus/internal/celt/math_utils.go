package celt

import (
	"math/bits"

	"github.com/thesyncim/gopus/internal/opusmath"
)

const bitexactThetaMax = 16384

//go:generate go run ../../tools/gen_math_utils_tables.go -out math_utils_tables_static.go

func celtUdiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	if n < 0 {
		n = 0
	}
	return n / d
}

func celtSudiv(n, d int) int {
	if d <= 0 {
		return 0
	}
	if n < 0 {
		return -celtUdiv(-n, d)
	}
	return celtUdiv(n, d)
}

func fracMul16(a, b int) int {
	return int((16384 + int32(int16(a))*int32(int16(b))) >> 15)
}

func bitexactCos(x int) int {
	if uint(x) <= bitexactThetaMax {
		return bitexactCosTable[x]
	}
	return bitexactCosCalc(x)
}

func bitexactCosCalc(x int) int {
	tmp := (4096 + int32(x)*int32(x)) >> 13
	x2 := int(tmp)
	x2 = (32767 - x2) + fracMul16(x2, (-7651+fracMul16(x2, (8277+fracMul16(-626, x2)))))
	return int(int16(1 + x2))
}

func bitexactLog2tan(isin, icos int) int {
	return bitexactLog2tanCalc(isin, icos)
}

func bitexactLog2tanCalc(isin, icos int) int {
	lc := ilog32(uint32(icos))
	ls := ilog32(uint32(isin))
	if lc > 15 {
		lc = 15
	}
	if ls > 15 {
		ls = 15
	}
	icos <<= 15 - lc
	isin <<= 15 - ls
	return (ls-lc)*(1<<11) + fracMul16(isin, fracMul16(isin, -2597)+7932) - fracMul16(icos, fracMul16(icos, -2597)+7932)
}

func bitexactLog2tanTheta(itheta int) int {
	if uint(itheta) <= bitexactThetaMax {
		return bitexactLog2tanThetaTable[itheta]
	}
	imid := bitexactCos(itheta)
	iside := bitexactCos(16384 - itheta)
	return bitexactLog2tan(iside, imid)
}

func isqrt32(val uint32) uint32 {
	return opusmath.ISqrt32(val)
}

func ilog32(x uint32) int {
	return bits.Len32(x)
}
