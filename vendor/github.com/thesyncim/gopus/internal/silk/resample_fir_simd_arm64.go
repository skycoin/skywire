//go:build arm64 && goexperiment.simd && !nosimd && !purego

package silk

import (
	"simd/archsimd"
	"unsafe"
)

var firInterpol21846SIMDCoefs = [3][8]int16{
	{189, -600, 617, 30567, 2996, -1375, 425, -46},
	{-48, 758, -3956, 23973, 15143, -3957, 967, -107},
	{-103, 896, -3487, 11950, 26341, -3350, 529, -4},
}

var firInterpol32768SIMDCoefs = [2][8]int16{
	{189, -600, 617, 30567, 2996, -1375, 425, -46},
	{-99, 972, -4222, 18278, 21254, -4235, 905, -80},
}

func firInterpol21846CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	_ = dst[nOut-1]
	_ = buf[(nOut-1)/3+7]

	coef0 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[0])
	coef4 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[1])
	coef8 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[2])
	coef0Hi := coef0.HiToLo()
	coef4Hi := coef4.HiToLo()
	coef8Hi := coef8.HiToLo()
	bufPtr := unsafe.Pointer(unsafe.SliceData(buf))
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))
	groups := nOut / 3
	for i := 0; i < groups; i++ {
		x := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(bufPtr, i*2)))
		xHi := x.HiToLo()
		res0 := firInterpolDotSIMD(x, xHi, coef0, coef0Hi)
		res4 := firInterpolDotSIMD(x, xHi, coef4, coef4Hi)
		res8 := firInterpolDotSIMD(x, xHi, coef8, coef8Hi)
		outIdx := i * 3
		*(*int16)(unsafe.Add(dstPtr, outIdx*2)) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, (outIdx+1)*2)) = sat16RShiftRound15(res4)
		*(*int16)(unsafe.Add(dstPtr, (outIdx+2)*2)) = sat16RShiftRound15(res8)
	}
	if tail := nOut - groups*3; tail != 0 {
		firInterpol21846CoreGo(dst[groups*3:], buf[groups:], tail)
	}
}

func firInterpolDotSIMD(xLo, xHi, coefLo, coefHi archsimd.Int16x8) int32 {
	sum := xLo.MulWidenLo(coefLo).Add(xHi.MulWidenLo(coefHi))
	return sum.ReduceSum()
}

func firInterpol32768CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	_ = dst[nOut-1]
	_ = buf[(nOut-1)/2+7]

	coef0 := archsimd.LoadInt16x8Array(&firInterpol32768SIMDCoefs[0])
	coef6 := archsimd.LoadInt16x8Array(&firInterpol32768SIMDCoefs[1])
	coef0Hi := coef0.HiToLo()
	coef6Hi := coef6.HiToLo()
	bufPtr := unsafe.Pointer(unsafe.SliceData(buf))
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))
	groups := nOut / 2
	for remaining := groups; remaining > 1; remaining-- {
		x := archsimd.LoadInt16x8Array((*[8]int16)(bufPtr))
		xHi := x.HiToLo()
		res0 := firInterpolDotSIMD(x, xHi, coef0, coef0Hi)
		res6 := firInterpolDotSIMD(x, xHi, coef6, coef6Hi)
		*(*int16)(dstPtr) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, 2)) = sat16RShiftRound15(res6)
		bufPtr = unsafe.Add(bufPtr, 2)
		dstPtr = unsafe.Add(dstPtr, 4)
	}
	if groups != 0 {
		x := archsimd.LoadInt16x8Array((*[8]int16)(bufPtr))
		xHi := x.HiToLo()
		res0 := firInterpolDotSIMD(x, xHi, coef0, coef0Hi)
		res6 := firInterpolDotSIMD(x, xHi, coef6, coef6Hi)
		*(*int16)(dstPtr) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, 2)) = sat16RShiftRound15(res6)
	}
	if tail := nOut - groups*2; tail != 0 {
		firInterpol32768CoreGo(dst[groups*2:], buf[groups:], tail)
	}
}

func firInterpol43691CoreSIMD(dst []int16, buf []int16, nOut int) {
	if nOut <= 0 {
		return
	}
	_ = dst[nOut-1]
	_ = buf[2*(nOut-1)/3+7]

	coef0 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[0])
	coef4 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[1])
	coef8 := archsimd.LoadInt16x8Array(&firInterpol21846SIMDCoefs[2])
	coef0Hi := coef0.HiToLo()
	coef4Hi := coef4.HiToLo()
	coef8Hi := coef8.HiToLo()
	bufPtr := unsafe.Pointer(unsafe.SliceData(buf))
	dstPtr := unsafe.Pointer(unsafe.SliceData(dst))
	groups := nOut / 3
	for remaining := groups; remaining > 1; remaining-- {
		x0 := archsimd.LoadInt16x8Array((*[8]int16)(bufPtr))
		x0Hi := x0.HiToLo()
		res0 := firInterpolDotSIMD(x0, x0Hi, coef0, coef0Hi)
		res8 := firInterpolDotSIMD(x0, x0Hi, coef8, coef8Hi)
		*(*int16)(dstPtr) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, 2)) = sat16RShiftRound15(res8)

		x1 := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(bufPtr, 2)))
		x1Hi := x1.HiToLo()
		res4 := firInterpolDotSIMD(x1, x1Hi, coef4, coef4Hi)
		*(*int16)(unsafe.Add(dstPtr, 4)) = sat16RShiftRound15(res4)

		bufPtr = unsafe.Add(bufPtr, 4)
		dstPtr = unsafe.Add(dstPtr, 6)
	}
	if groups != 0 {
		x0 := archsimd.LoadInt16x8Array((*[8]int16)(bufPtr))
		x0Hi := x0.HiToLo()
		res0 := firInterpolDotSIMD(x0, x0Hi, coef0, coef0Hi)
		res8 := firInterpolDotSIMD(x0, x0Hi, coef8, coef8Hi)
		*(*int16)(dstPtr) = sat16RShiftRound15(res0)
		*(*int16)(unsafe.Add(dstPtr, 2)) = sat16RShiftRound15(res8)

		x1 := archsimd.LoadInt16x8Array((*[8]int16)(unsafe.Add(bufPtr, 2)))
		x1Hi := x1.HiToLo()
		res4 := firInterpolDotSIMD(x1, x1Hi, coef4, coef4Hi)
		*(*int16)(unsafe.Add(dstPtr, 4)) = sat16RShiftRound15(res4)
	}
	if tail := nOut - groups*3; tail != 0 {
		firInterpol43691CoreGo(dst[groups*3:], buf[groups*2:], tail)
	}
}
