//go:build gopus_fixed_point && !gopus_qext

package fixedpoint

import "github.com/thesyncim/gopus/internal/rangecoding"

func cubicQuantQEXT([]int32, int, int, int, *rangecoding.Encoder, int32, bool) uint32 {
	return 0
}

func cubicQuantPartitionEncodeQEXT(*bandEncCtx, []int32, int, int32, int, int, int32) uint {
	return 0
}
