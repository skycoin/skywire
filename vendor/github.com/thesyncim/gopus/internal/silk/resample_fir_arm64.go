//go:build arm64 && !purego

package silk

func firInterpol21846Core(dst []int16, buf []int16, nOut int)

func firInterpol32768Core(dst []int16, buf []int16, nOut int)

func firInterpol43691Core(dst []int16, buf []int16, nOut int)
